package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNetEaseAccountPageRequiresAuthentication(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router, func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/admin/netease-accounts", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatal("account administration must require admin authentication")
	}
	router = gin.New()
	RegisterRoutes(router, allowAll)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/netease-accounts", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	for _, content := range []string{"网易游戏账户资源池", `id="username"`, `type="password"`, "/v1/admin/netease/accounts", `method:"PATCH"`, `method:"DELETE"`} {
		if !strings.Contains(response.Body.String(), content) {
			t.Fatalf("missing administration control %s", content)
		}
	}
}
