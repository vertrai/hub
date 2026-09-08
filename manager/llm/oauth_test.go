package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceOAuthErrorStates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"pending", 403, `{}`, ErrAuthorizationPending},
		{"slow_down", 429, `{"error":"slow_down"}`, ErrOAuthSlowDown},
		{"denied", 403, `{"error":"access_denied","secret":"do-not-leak"}`, nil},
		{"expired", 400, `{"error":"expired_token","secret":"do-not-leak"}`, nil},
		{"upstream_failure", 500, `{"access_token":"do-not-leak"}`, nil},
		{"malformed", 200, `{`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer server.Close()
			client := NewDeviceOAuthClient(server.Client())
			client.AuthBaseURL = server.URL
			_, err := client.Poll(context.Background(), "device", "code")
			if err == nil || strings.Contains(err.Error(), "do-not-leak") {
				t.Fatalf("invalid error %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if tc.want == nil && errors.Is(err, ErrAuthorizationPending) {
				t.Fatal("terminal failure reported as pending")
			}
		})
	}
}

func TestDeviceOAuthUnsupportedRegion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" || r.Header.Get("User-Agent") != "codex_cli_rs/0.125.0" {
			t.Error("request differs from reference flow")
		}
		w.WriteHeader(403)
		io.WriteString(w, `{"error":{"code":"unsupported_country_region_territory","message":"upstream detail"}}`)
	}))
	defer server.Close()
	client := NewDeviceOAuthClient(server.Client())
	client.AuthBaseURL = server.URL
	_, err := client.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported_country_region_territory") || strings.Contains(err.Error(), "upstream detail") {
		t.Fatalf("unexpected error: %v", err)
	}
}
