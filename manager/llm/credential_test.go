package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshConcurrentRequests(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		r.ParseForm()
		if r.Form.Get("refresh_token") != "old" || r.Form.Get("grant_type") != "refresh_token" {
			t.Error("invalid refresh")
		}
		io.WriteString(w, `{"access_token":"new","refresh_token":"rotated","expires_in":3600}`)
	}))
	defer upstream.Close()
	adapter := NewCodexAdapter(upstream.Client())
	raw, _ := json.Marshal(CodexCredential{AccessToken: "expired", RefreshToken: "old", TokenURL: upstream.URL, AccountID: "account", ExpiresAt: time.Now().Add(-time.Hour)})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			updated, err := adapter.PrepareCredential(context.Background(), "one", raw)
			if err != nil || !strings.Contains(string(updated), "rotated") {
				t.Errorf("refresh: %s %v", updated, err)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("refresh count %d", count.Load())
	}
}
func TestImportAuthJSON(t *testing.T) {
	raw := []byte(`{"tokens":{"access_token":"token","refresh_token":"refresh","account_id":"account"},"expires_at":"2099-01-01T00:00:00Z"}`)
	imported, err := ImportCredential(raw, "https://chatgpt.com/backend-api/codex")
	if err != nil || !strings.Contains(string(imported), "refresh") {
		t.Fatalf("%s %v", imported, err)
	}
	if _, err := ImportCredential([]byte(`{"access_token":"token","account_id":"account"}`), "https://example.com"); err == nil {
		t.Fatal("accepted token without expiry")
	}
}
func TestTruncatedChatStreamReturnsError(t *testing.T) {
	source := io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
	result := transformOpenAICodexResponsesStreamToChat(source, "model", false)
	defer result.Close()
	raw, err := io.ReadAll(result)
	if err == nil || strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("truncated stream reported success: %s %v", raw, err)
	}
}
