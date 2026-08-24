package manager

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
