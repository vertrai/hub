package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type googleTokenValidatorStub struct{ identity adminIdentity }

func (s googleTokenValidatorStub) Validate(context.Context, string, string) (adminIdentity, error) {
	return s.identity, nil
}

func TestAdminGoogleLoginAllowsConfiguredEmail(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	service.adminAuth.clientID = "google-client"
	service.adminAuth.validator = googleTokenValidatorStub{identity: adminIdentity{Subject: "google-subject", Email: "admin@example.com"}}
	service.adminAuth.allowed["admin@example.com"] = struct{}{}
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/auth/google", strings.NewReader(`{"id_token":"credential"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	found := false
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == adminSessionCookie && cookie.Value != "" && cookie.HttpOnly {
			found = true
		}
	}
	if !found {
		t.Fatal("login did not issue an HttpOnly JWT session")
	}
}

func TestAdminGoogleLoginRejectsEmailOutsideAllowlist(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	service.adminAuth.clientID = "google-client"
	service.adminAuth.validator = googleTokenValidatorStub{identity: adminIdentity{Subject: "google-subject", Email: "outsider@example.com"}}
	service.adminAuth.allowed["admin@example.com"] = struct{}{}
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/auth/google", strings.NewReader(`{"id_token":"credential"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"admin_not_allowed"`) || !strings.Contains(recorder.Body.String(), "未被授权为管理员") {
		t.Fatalf("missing non-admin error response: %s", recorder.Body.String())
	}
}

func TestAdminGoogleConfigurationMustBeComplete(t *testing.T) {
	_, err := New("test", Config{AdminGoogle: AdminGoogleConfig{ClientID: "client"}}, nil)
	if err == nil {
		t.Fatal("expected incomplete Google administrator configuration to fail")
	}
}

func TestAdminSessionRejectsTampering(t *testing.T) {
	service, _ := New("test", Config{}, nil)
	service.adminAuth.allowed["admin@example.com"] = struct{}{}
	token, err := service.adminAuth.issueSession(adminIdentity{Subject: "google-subject", Email: "admin@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if identity, ok := service.adminAuth.verifySession(token); !ok || identity.Email != "admin@example.com" {
		t.Fatal("valid JWT session was rejected")
	}
	if _, ok := service.adminAuth.verifySession(token + "x"); ok {
		t.Fatal("tampered JWT session was accepted")
	}
}
