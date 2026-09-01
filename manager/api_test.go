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

func authenticateAdmin(service *Manager, request *http.Request) {
	service.adminAuth.allowed["admin@example.com"] = struct{}{}
	token, err := service.adminAuth.issueSession(adminIdentity{Subject: "google-subject", Email: "admin@example.com"})
	if err != nil {
		panic(err)
	}
	request.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: token})
}

func TestInfo(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/info", nil)
	service, _ := New("test", Config{}, nil)
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAdminRequiresGoogleSession(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	service.router().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestDeletePodRouteRequiresManagerDatabase(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/v1/admin/hymatrix/pods/pod-test", nil)
	authenticateAdmin(service, request)
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEvalPodRouteRequiresManagerDatabase(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/hymatrix/pods/pod-test/eval", strings.NewReader(`{"command":"date"}`))
	request.Header.Set("Content-Type", "application/json")
	authenticateAdmin(service, request)
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMiniProgramTaskDeletionInProgress(t *testing.T) {
	for _, status := range []string{schema.MiniProgramTaskSpawning, schema.MiniProgramTaskRefreshingQR, schema.MiniProgramTaskStartingAgent} {
		if !miniProgramTaskDeletionInProgress(status) {
			t.Errorf("status %q should block deletion", status)
		}
	}
	for _, status := range []string{schema.MiniProgramTaskWaitingForWeixin, schema.MiniProgramTaskRunning, schema.MiniProgramTaskQRExpired, schema.MiniProgramTaskFailed} {
		if miniProgramTaskDeletionInProgress(status) {
			t.Errorf("status %q should allow deletion", status)
		}
	}
}

func TestAdminResourceProxyUsesInternalKey(t *testing.T) {
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/google/accounts" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Admin-API-Key") != "internal-secret" {
			t.Errorf("internal key was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer resources.Close()
	service, _ := New("test", Config{Resources: ResourcesConfig{BaseURL: resources.URL, AdminAPIKey: "internal-secret", Timeout: time.Second}}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/google/accounts", nil)
	authenticateAdmin(service, request)
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminBrowserCloseProxyForwardsSessionID(t *testing.T) {
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/internal/browser/sessions/brw_123/close" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Admin-API-Key") != "internal-secret" {
			t.Errorf("internal key was not forwarded")
		}
		_, _ = io.WriteString(w, `{"closed":true}`)
	}))
	defer resources.Close()
	service, _ := New("test", Config{Resources: ResourcesConfig{BaseURL: resources.URL, AdminAPIKey: "internal-secret", Timeout: time.Second}}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/browser/sessions/brw_123/close", nil)
	authenticateAdmin(service, request)
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"closed":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayProxyForwardsGatewayKey(t *testing.T) {
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gw_sk_test" {
			t.Errorf("gateway key was not forwarded")
		}
		_, _ = io.WriteString(w, `{"browser":{"id":"brw_test"}}`)
	}))
	defer resources.Close()
	service, _ := New("test", Config{Resources: ResourcesConfig{BaseURL: resources.URL, Timeout: time.Second}}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/browser", nil)
	request.Header.Set("Authorization", "Bearer gw_sk_test")
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGoogleUserProxyForwardsPurpose(t *testing.T) {
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/google-user" || r.URL.Query().Get("purpose") != "xbox" {
			t.Errorf("request URL = %s", r.URL.String())
		}
		_, _ = io.WriteString(w, `{"googleUser":{"purpose":"xbox"}}`)
	}))
	defer resources.Close()
	service, _ := New("test", Config{Resources: ResourcesConfig{BaseURL: resources.URL, Timeout: time.Second}}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/google-user?purpose=xbox", nil)
	request.Header.Set("Authorization", "Bearer gw_sk_test")
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"purpose":"xbox"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSharedAdminFrontend(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	service, _ := New("test", Config{}, nil)
	authenticateAdmin(service, request)
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestAdminFrontendRedirectsToLoginWithoutSession(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	for _, path := range []string{"/admin", "/admin/users", "/admin/google", "/admin/browser", "/admin/xbot", "/admin/telegram", "/admin/weixin", "/admin/hymatrix", "/admin/test"} {
		recorder := httptest.NewRecorder()
		service.router().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/admin/login" {
			t.Fatalf("path=%s status=%d location=%q", path, recorder.Code, recorder.Header().Get("Location"))
		}
	}

	login := httptest.NewRecorder()
	service.router().ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), "Google 账号") {
		t.Fatalf("status=%d body=%s", login.Code, login.Body.String())
	}
}

func TestResolveTelegramBotLink(t *testing.T) {
	originalClient := telegramAPIHTTPClient
	telegramAPIHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/getMe") {
			t.Fatalf("unexpected Telegram request: %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"username":"vertr_523854_bot"}}`)),
		}, nil
	})}
	defer func() { telegramAPIHTTPClient = originalClient }()

	service, _ := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/telegram/bot-link", strings.NewReader(`{"botToken":"123456:secret"}`))
	authenticateAdmin(service, request)
	request.Header.Set("Content-Type", "application/json")
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"botLink":"https://t.me/vertr_523854_bot"`) {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestTelegramBotLinkNormalizesUsername(t *testing.T) {
	if got := telegramBotLink("@vertr_523854_bot"); got != "https://t.me/vertr_523854_bot" {
		t.Fatalf("bot link = %q", got)
	}
}

func TestResourcesTelegramBotDetailsIncludesUsername(t *testing.T) {
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, `{"telegramBot":{"botToken":"123456:secret","username":"vertr_523854_bot"}}`)
	}))
	defer resources.Close()
	client := NewResourcesClient(ResourcesConfig{BaseURL: resources.URL, Timeout: time.Second})
	bot, err := client.telegramBotDetails(t.Context(), "gw_sk_test")
	if err != nil {
		t.Fatal(err)
	}
	if bot.BotToken != "123456:secret" || bot.Username != "vertr_523854_bot" {
		t.Fatalf("unexpected Telegram bot: %#v", bot)
	}
}

func TestResourcesTelegramBotDetailsPreservesForbiddenStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"telegram resource is not allowed for this api key","resource":"telegram"}`)
	}))
	defer server.Close()

	client := NewResourcesClient(ResourcesConfig{BaseURL: server.URL, Timeout: time.Second})
	_, err := client.telegramBotDetails(t.Context(), "gw_sk_test")
	var resourcesError *ResourcesHTTPError
	if !errors.As(err, &resourcesError) {
		t.Fatalf("error = %v, want *ResourcesHTTPError", err)
	}
	if resourcesError.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resourcesError.StatusCode, http.StatusForbidden)
	}
}

func TestResourcesCreateAccessKeyForwardsScopes(t *testing.T) {
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		for _, expected := range []string{`"ownerUserId":"user-1"`, `"allowGoogle":true`, `"allowBrowser":false`, `"allowTelegram":true`} {
			if !strings.Contains(string(body), expected) {
				t.Errorf("request body %s is missing %s", body, expected)
			}
		}
		_, _ = io.WriteString(w, `{"accessKey":{"id":"key-1"},"gatewayApiKey":"gw_sk_test"}`)
	}))
	defer resources.Close()
	client := NewResourcesClient(ResourcesConfig{BaseURL: resources.URL, Timeout: time.Second})
	_, _, err := client.createAccessKey(t.Context(), "user-1", ResourceScopes{AllowGoogle: true, AllowBrowser: false, AllowTelegram: true})
	if err != nil {
		t.Fatal(err)
	}
}
