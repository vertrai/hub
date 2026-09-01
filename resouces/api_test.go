package resouces

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/resouces/schema"
)

func TestInfo(t *testing.T) {
	g := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/info", nil)
	g.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestResourceScopeMiddlewareRejectsDisabledResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &Resouces{}
	for _, test := range []struct {
		scope string
		key   schema.AccessKey
	}{
		{scope: resourceScopeGoogle, key: schema.AccessKey{AllowGoogle: false, AllowBrowser: true, AllowTelegram: true}},
		{scope: resourceScopeBrowser, key: schema.AccessKey{AllowGoogle: true, AllowBrowser: false, AllowTelegram: true}},
		{scope: resourceScopeTelegram, key: schema.AccessKey{AllowGoogle: true, AllowBrowser: true, AllowTelegram: false}},
	} {
		router := gin.New()
		router.GET("/", func(c *gin.Context) {
			c.Set(gatewayPrincipalContext, gatewayPrincipal{AccessKey: test.key})
		}, g.requireResourceScope(test.scope), func(c *gin.Context) { c.Status(http.StatusNoContent) })
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusForbidden {
			t.Errorf("scope %s status = %d", test.scope, recorder.Code)
		}
	}
}

func TestResourceScopeMiddlewareAllowsEnabledResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &Resouces{}
	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		c.Set(gatewayPrincipalContext, gatewayPrincipal{AccessKey: schema.AccessKey{AllowGoogle: true}})
	}, g.requireResourceScope(resourceScopeGoogle), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminPageIsNotMounted(t *testing.T) {
	g := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	g.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestTelegramAdminPage(t *testing.T) {
	g := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/telegram", nil)
	g.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("telegram admin page is unavailable")
	}
}

func TestAdminTestPage(t *testing.T) {
	g := New("test", Config{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	g.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAdminRequiresKey(t *testing.T) {
	g := New("test", Config{AdminAPIKey: "secret"}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/internal/access-keys", nil)
	g.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestTelegramAdminRoutesRequireKey(t *testing.T) {
	g := New("test", Config{AdminAPIKey: "secret"}, nil)
	for _, path := range []string{"/v1/internal/telegram/auth/init", "/v1/internal/telegram/bots/create"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, nil)
		g.router().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("POST %s status = %d", path, recorder.Code)
		}
	}
}

func TestBrowserAdminRouteRequiresKey(t *testing.T) {
	g := New("test", Config{AdminAPIKey: "secret"}, nil)
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/v1/internal/browser/sessions"},
		{http.MethodPost, "/v1/internal/browser/sessions/brw_test/close"},
		{http.MethodPost, "/v1/internal/xbox/bots"},
		{http.MethodGet, "/v1/internal/xbox/bots"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		g.router().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d", test.method, test.path, recorder.Code)
		}
	}
}

func TestUserRoutesRequireGatewayAPIKey(t *testing.T) {
	g := New("test", Config{}, nil)
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/google-user"},
		{method: http.MethodGet, path: "/v1/google-user/access-token"},
		{method: http.MethodGet, path: "/v1/browser"},
		{method: http.MethodPost, path: "/v1/browser/reset"},
		{method: http.MethodPost, path: "/v1/browser/close"},
		{method: http.MethodGet, path: "/v1/telegram-bot"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		g.router().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d", test.method, test.path, recorder.Code)
		}
	}
}
