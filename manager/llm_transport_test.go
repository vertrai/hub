package manager

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestLLMProxyConnectionError(t *testing.T) {
	m, chat := setupLLMResources(t)
	resource := acquireResourceTest(t, m, "hub-a")
	m.llmClient = &http.Client{Transport: llmRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: "https://secret.invalid/token", Err: &net.OpError{Op: "proxyconnect", Net: "tcp", Err: syscall.ECONNREFUSED}}
	})}
	r := chat(resource.APIKey, "hub-chat")
	if r.Code != 502 || !strings.Contains(r.Body.String(), "HTTP_PROXY") || strings.Contains(r.Body.String(), "secret.invalid") {
		t.Fatal(r.Body.String())
	}
	if strings.Contains(llmTransportErrorMessage(errors.New("credential-secret")), "credential-secret") {
		t.Fatal("raw transport error exposed")
	}
}
