package manager

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWeixinOnboardingDoesNotExposePollSecret(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/get_bot_qrcode" || r.URL.Query().Get("bot_type") != "3" {
			t.Fatalf("unexpected provider request: %s", r.URL.String())
		}
		if r.Header.Get("iLink-App-Id") != "bot" {
			t.Fatal("missing iLink app header")
		}
		_, _ = w.Write([]byte(`{"qrcode":"secret-poll-token","qrcode_img_content":"https://weixin.qq.com/x/scan"}`))
	}))
	defer provider.Close()
	m, err := New("test", Config{Resources: ResourcesConfig{Timeout: time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.weixinBaseURL = provider.URL
	m.weixinClient = provider.Client()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/weixin/onboarding", bytes.NewBufferString(`{"userId":"user-a"}`))
	req.Header.Set("Content-Type", "application/json")
	authenticateAdmin(m, req)
	res := httptest.NewRecorder()
	m.router().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-poll-token") {
		t.Fatalf("poll secret leaked: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "data:image/png;base64,") {
		t.Fatalf("QR image missing: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"expireTime":120`) || !strings.Contains(res.Body.String(), `"expiresAt":`) {
		t.Fatalf("QR expiry metadata missing: %s", res.Body.String())
	}
}

func TestAllowedWeixinURLRejectsUntrustedHost(t *testing.T) {
	if _, err := allowedWeixinURL("https://attacker.example"); err == nil {
		t.Fatal("expected untrusted host rejection")
	}
	if got, err := allowedWeixinURL("https://ilinkai.weixin.qq.com"); err != nil || got != "https://ilinkai.weixin.qq.com" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestConfirmedWeixinCredentialsReturnHermesEnvironment(t *testing.T) {
	m, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.weixinAttempts["attempt"] = weixinAttempt{Credentials: &WeixinCredentials{AccountID: "bot@im.bot", Token: "secret-token", BaseURL: "https://ilinkai.weixin.qq.com", UserID: "wx-user"}, CredentialExpiresAt: time.Now().Add(time.Hour)}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/weixin/onboarding/attempt/credentials", nil)
	authenticateAdmin(m, req)
	res := httptest.NewRecorder()
	m.router().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, expected := range []string{"WEIXIN_ACCOUNT_ID", "bot@im.bot", "WEIXIN_TOKEN", "secret-token", "WEIXIN_DM_POLICY=allowlist", "WEIXIN_ALLOWED_USERS=wx-user"} {
		if !strings.Contains(res.Body.String(), expected) {
			t.Errorf("response missing %q: %s", expected, res.Body.String())
		}
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", res.Header().Get("Cache-Control"))
	}
}

func TestConnectedWeixinPollReturnsStoredBotIDWithoutToken(t *testing.T) {
	m, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.weixinAttempts["attempt"] = weixinAttempt{Credentials: &WeixinCredentials{BotID: "wxb_test", AccountID: "bot@im.bot", Token: "secret-token", BaseURL: "https://ilinkai.weixin.qq.com", UserID: "wx-user"}, CredentialExpiresAt: time.Now().Add(time.Hour)}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/weixin/onboarding/attempt", nil)
	authenticateAdmin(m, req)
	res := httptest.NewRecorder()
	m.router().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"botId":"wxb_test"`) {
		t.Fatalf("stored bot ID missing: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-token") {
		t.Fatalf("Weixin token leaked from poll response: %s", res.Body.String())
	}
}

func TestCancelledInFlightWeixinPollCannotConfirmAttempt(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"status":"confirmed","ilink_bot_id":"bot@im.bot","bot_token":"secret-token","baseurl":"https://ilinkai.weixin.qq.com","ilink_user_id":"wx-user"}`))
	}))
	defer provider.Close()
	m, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.weixinClient = provider.Client()
	m.weixinAttempts["attempt"] = weixinAttempt{ID: "attempt", UserID: "user-a", PollSecret: "poll-secret", ProviderBase: provider.URL, ExpiresAt: time.Now().Add(time.Hour)}
	pollResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/weixin/onboarding/attempt", nil)
		authenticateAdmin(m, req)
		res := httptest.NewRecorder()
		m.router().ServeHTTP(res, req)
		pollResponse <- res
	}()
	<-started
	cancelReq := httptest.NewRequest(http.MethodDelete, "/v1/admin/weixin/onboarding/attempt", nil)
	authenticateAdmin(m, cancelReq)
	cancelRes := httptest.NewRecorder()
	m.router().ServeHTTP(cancelRes, cancelReq)
	if cancelRes.Code != http.StatusNoContent {
		t.Fatalf("cancel status=%d body=%s", cancelRes.Code, cancelRes.Body.String())
	}
	close(release)
	res := <-pollResponse
	if res.Code != http.StatusGone {
		t.Fatalf("poll status=%d body=%s", res.Code, res.Body.String())
	}
	m.weixinMu.Lock()
	_, exists := m.weixinAttempts["attempt"]
	m.weixinMu.Unlock()
	if exists {
		t.Fatal("cancelled Weixin attempt was resurrected")
	}
}

func TestConfirmedWeixinCredentialsRejectDotenvInjection(t *testing.T) {
	m, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.weixinAttempts["attempt"] = weixinAttempt{Credentials: &WeixinCredentials{AccountID: "bot@im.bot", Token: "secret\nINJECTED=yes", BaseURL: "https://ilinkai.weixin.qq.com", UserID: "wx-user"}, CredentialExpiresAt: time.Now().Add(time.Hour)}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/weixin/onboarding/attempt/credentials", nil)
	authenticateAdmin(m, req)
	res := httptest.NewRecorder()
	m.router().ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "INJECTED=yes") {
		t.Fatalf("unsafe value leaked: %s", res.Body.String())
	}
}
