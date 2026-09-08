package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalAdminAccessBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, env, host, peer, header, value string
		allowed                              bool
	}{
		{name: "direct local", env: "local", host: "127.0.0.1:8086", peer: "127.0.0.1:43210", allowed: true},
		{name: "no port", env: "local", host: "127.0.0.1", peer: "127.0.0.1:43210", allowed: true},
		{name: "same origin", env: "local", host: "127.0.0.1:8086", peer: "127.0.0.1:43210", header: "Origin", value: "http://127.0.0.1:8086", allowed: true},
		{name: "production", env: "production", host: "127.0.0.1:8086", peer: "127.0.0.1:43210"},
		{name: "remote forged host", env: "local", host: "127.0.0.1:8086", peer: "192.168.1.10:43210"},
		{name: "public domain", env: "local", host: "hub.example.com", peer: "127.0.0.1:43210"},
		{name: "hostname suffix", env: "local", host: "127.0.0.1.example.com", peer: "127.0.0.1:43210"},
		{name: "forwarded request", env: "local", host: "127.0.0.1:8086", peer: "127.0.0.1:43210", header: "X-Forwarded-For", value: "192.168.1.10"},
		{name: "forwarded host", env: "local", host: "127.0.0.1:8086", peer: "127.0.0.1:43210", header: "X-Forwarded-Host", value: "hub.example.com"},
		{name: "cross origin", env: "local", host: "127.0.0.1:8086", peer: "127.0.0.1:43210", header: "Origin", value: "https://example.com"},
		{name: "cross site", env: "local", host: "127.0.0.1:8086", peer: "127.0.0.1:43210", header: "Sec-Fetch-Site", value: "cross-site"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := New("test", Config{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			m.env = tc.env
			for _, path := range []string{"/admin", "/v1/admin/session", "/v1/admin/llm/presets"} {
				req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+path, nil)
				req.RemoteAddr = tc.peer
				if tc.header != "" {
					req.Header.Set(tc.header, tc.value)
				}
				rec := httptest.NewRecorder()
				m.router().ServeHTTP(rec, req)
				want := http.StatusUnauthorized
				if path == "/admin" {
					want = http.StatusFound
				}
				if tc.allowed {
					want = http.StatusOK
				}
				if rec.Code != want {
					t.Fatalf("%s status=%d want=%d", path, rec.Code, want)
				}
				if tc.allowed && path == "/v1/admin/session" && !strings.Contains(rec.Body.String(), "local-admin@localhost") {
					t.Fatal("missing stable local identity")
				}
				if len(rec.Result().Cookies()) > 0 {
					t.Fatal("local access must not issue a reusable credential")
				}
			}
		})
	}
}
func TestLocalLoginDiscovery(t *testing.T) {
	m, _ := New("test", Config{}, nil)
	m.env = "local"
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8086/v1/admin/auth/info", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	m.router().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"localBypass":true`) {
		t.Fatal(rec.Body.String())
	}
}
