package manager

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vertrai/hub/manager/schema"
)

func TestMiniProgramTaskTokenIsOpaqueAndVerifiable(t *testing.T) {
	token, hash, err := newMiniProgramTaskToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || token == hash || !validMiniProgramTaskToken(hash, token) {
		t.Fatalf("invalid token pair token=%q hash=%q", token, hash)
	}
	if validMiniProgramTaskToken(hash, token+"tampered") {
		t.Fatal("tampered task token was accepted")
	}
}

func TestMiniProgramSessionRoundTripAndExpiry(t *testing.T) {
	service, err := New("test", Config{MiniProgram: MiniProgramConfig{AppSecret: "session-secret"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := service.signMiniProgramSession("wx_user123", time.Now().Add(time.Hour))
	if userID, ok := service.parseMiniProgramSession(token); !ok || userID != "wx_user123" {
		t.Fatalf("session user=%q ok=%v", userID, ok)
	}
	if _, ok := service.parseMiniProgramSession(token + "tampered"); ok {
		t.Fatal("tampered session accepted")
	}
	expired := service.signMiniProgramSession("wx_user123", time.Now().Add(-time.Second))
	if _, ok := service.parseMiniProgramSession(expired); ok {
		t.Fatal("expired session accepted")
	}
}

func TestMiniProgramUserIDDoesNotExposeOpenID(t *testing.T) {
	got := miniProgramUserID("wx-app", "sensitive-openid")
	if !strings.HasPrefix(got, "wx_") || strings.Contains(got, "sensitive-openid") {
		t.Fatalf("user ID = %q", got)
	}
	if got != miniProgramUserID("wx-app", "sensitive-openid") {
		t.Fatal("user ID is not stable")
	}
}

func TestExchangeMiniProgramCodeUsesCode2Session(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sns/jscode2session" || request.URL.Query().Get("appid") != "wx-app" || request.URL.Query().Get("secret") != "wx-secret" || request.URL.Query().Get("js_code") != "login-code" {
			t.Fatalf("unexpected code2Session request: %s", request.URL.String())
		}
		_, _ = io.WriteString(w, `{"openid":"openid-1","session_key":"must-not-leave-manager"}`)
	}))
	defer provider.Close()
	service, err := New("test", Config{MiniProgram: MiniProgramConfig{AppID: "wx-app", AppSecret: "wx-secret", WeixinAPIBase: provider.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.miniProgramHTTPClient = provider.Client()
	openid, err := service.exchangeMiniProgramCode(t.Context(), "login-code")
	if err != nil || openid != "openid-1" {
		t.Fatalf("openid=%q err=%v", openid, err)
	}
}

func TestMiniProgramConfigRejectsMissingServerSecrets(t *testing.T) {
	if err := validateMiniProgramConfig(MiniProgramConfig{}); err == nil || !strings.Contains(err.Error(), "miniProgram.") {
		t.Fatalf("expected actionable config error, got %v", err)
	}
}

func TestMiniProgramPodRuntimeMustRemainHermes(t *testing.T) {
	cfg := MiniProgramConfig{
		AppID: "app", AppSecret: "secret", WeixinAPIBase: "https://api.weixin.qq.com",
		NodeURL: "https://node", PrivateKey: "key", RuntimeType: "docker",
		GatewayURL: "https://gateway", HermesGatewayToken: "token",
	}
	if err := validateMiniProgramConfig(cfg); err == nil || !strings.Contains(err.Error(), "must be hermes") {
		t.Fatalf("expected non-Hermes runtime rejection, got %v", err)
	}
	cfg.RuntimeType = "hermes"
	if err := validateMiniProgramConfig(cfg); err != nil {
		t.Fatalf("Hermes runtime rejected: %v", err)
	}
}

func TestMiniProgramQRCanBeRenewedBeforeSharingOrAfterExpiry(t *testing.T) {
	for _, status := range []string{schema.MiniProgramTaskWaitingForWeixin, schema.MiniProgramTaskQRExpired} {
		if !canRenewMiniProgramQR(status) {
			t.Fatalf("status %q should allow QR renewal", status)
		}
	}
	for _, status := range []string{schema.MiniProgramTaskSpawning, schema.MiniProgramTaskRefreshingQR, schema.MiniProgramTaskStartingAgent, schema.MiniProgramTaskRunning, schema.MiniProgramTaskFailed} {
		if canRenewMiniProgramQR(status) {
			t.Fatalf("status %q should not allow QR renewal", status)
		}
	}
}

func TestMiniProgramPodWaitsForUserBeforeCreatingWeixinQR(t *testing.T) {
	task := schema.MiniProgramAgentTask{
		Status:          schema.MiniProgramTaskSpawning,
		WeixinAttemptID: "old-attempt",
		QRCodeData:      "old-qr",
		QRExpiresAt:     time.Now().UTC().Add(time.Minute),
		Error:           "old error",
	}

	prepareMiniProgramTaskForWeixin(&task)

	if task.Status != schema.MiniProgramTaskWaitingForWeixin {
		t.Fatalf("expected task to wait for user, got %q", task.Status)
	}
	if task.WeixinAttemptID != "" || task.QRCodeData != "" || !task.QRExpiresAt.IsZero() || task.Error != "" {
		t.Fatalf("task generated or retained WeChat QR data before user action: %+v", task)
	}
}

func TestMiniProgramLoginTransportErrorDoesNotExposeCredentials(t *testing.T) {
	service, err := New("test", Config{MiniProgram: MiniProgramConfig{AppID: "wx-app", AppSecret: "private-test-secret"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.miniProgramHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused")
	})}
	_, err = service.exchangeMiniProgramCode(t.Context(), "private-login-code")
	if err == nil {
		t.Fatal("expected transport failure")
	}
	for _, secret := range []string{"private-test-secret", "private-login-code", "secret=", "jscode2session?"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("login error exposes request credentials")
		}
	}
}

func TestMiniProgramOfficialAPIBypassesEnvironmentProxy(t *testing.T) {
	service, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := service.miniProgramHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("login client must have an explicit transport")
	}
	req, _ := http.NewRequest("GET", "https://api.weixin.qq.com/sns/jscode2session", nil)
	proxy, err := transport.Proxy(req)
	if err != nil || proxy != nil {
		t.Fatal("official WeChat API must connect directly")
	}
}
