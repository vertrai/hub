package web

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLLMPageRequiresAuthenticationAndServesScript(t *testing.T) {
	r := gin.New()
	RegisterRoutes(r, func(c *gin.Context) { c.AbortWithStatus(401) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/llm", nil))
	if rec.Code != 401 {
		t.Fatal("LLM management page is not protected")
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/assets/llm.js", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/javascript; charset=utf-8" {
		t.Fatal("LLM frontend script not served")
	}
}

func TestLLMTestPageRequiresAuthentication(t *testing.T) {
	r := gin.New()
	RegisterRoutes(r, func(c *gin.Context) { c.AbortWithStatus(401) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/llm/test", nil))
	if rec.Code != 401 {
		t.Fatalf("test page authentication: %d", rec.Code)
	}
	r = gin.New()
	RegisterRoutes(r, func(c *gin.Context) { c.Next() })
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/llm/test", nil))
	if rec.Code != 200 {
		t.Fatalf("test page status: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/assets/llm-test.js", nil))
	if rec.Code != 200 {
		t.Fatalf("test script status: %d", rec.Code)
	}
}
