package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type llmRoundTripFunc func(*http.Request) (*http.Response, error)

func (f llmRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLLMOrdinaryProviderPresetsAndRelay(t *testing.T) {
	for _, tc := range []struct{ id, base string }{
		{"deepseek", "https://api.deepseek.com"},
		{"aliyun-tokenplan", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
		{"aliyun-codingplan", "https://coding.dashscope.aliyuncs.com/v1"},
		{"aliyun-dashscope", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			m := newLLMTestManager(t)
			r := llmTestRequest(m, "PUT", "/v1/admin/llm/providers/vendor", fmt.Sprintf(`{"kind":"openai","preset":%q,"name":"Vendor","apiKey":"vendor-secret","models":["model-a"],"enabled":true}`, tc.id), "", true)
			if r.Code != 200 {
				t.Fatal(r.Body.String())
			}
			listed := llmTestRequest(m, "GET", "/v1/admin/llm/providers", "", "", true)
			if !strings.Contains(listed.Body.String(), tc.base) || strings.Contains(listed.Body.String(), "vendor-secret") {
				t.Fatal(listed.Body.String())
			}
			// Settings edits with no apiKey retain the secret.
			r = llmTestRequest(m, "PUT", "/v1/admin/llm/providers/vendor", fmt.Sprintf(`{"kind":"openai","preset":%q,"models":["model-a"],"enabled":true}`, tc.id), "", true)
			if r.Code != 200 {
				t.Fatal(r.Body.String())
			}
			publishLLMTestRoute(t, m, "vendor", "model-a")
			r = llmTestRequest(m, "POST", "/v1/admin/llm/keys", `{"name":"client"}`, "", true)
			var key map[string]any
			json.Unmarshal(r.Body.Bytes(), &key)
			for _, stream := range []bool{false, true} {
				m.llmClient = &http.Client{Transport: llmRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.String() != tc.base+"/chat/completions" || req.Header.Get("Authorization") != "Bearer vendor-secret" {
						t.Errorf("invalid upstream request: %s", req.URL)
					}
					if req.Header.Get("Cookie") != "" {
						t.Error("forwarded admin credentials")
					}
					var body map[string]any
					json.NewDecoder(req.Body).Decode(&body)
					if body["model"] != "model-a" || body["stream"] != stream || body["enable_thinking"] != false || body["max_tokens"] != float64(17) || body["tools"] == nil {
						t.Errorf("request parameters lost: %v", body)
					}
					contentType := "application/json"
					response := `{"choices":[{"message":{"tool_calls":[{"id":"call1","type":"function","function":{"name":"weather","arguments":"{}"}}]}}],"usage":{"total_tokens":3}}`
					if stream {
						contentType = "text/event-stream"
						response = "data: " + response + "\n\ndata: [DONE]\n\n"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				r = llmTestRequest(m, "POST", "/llm/v1/chat/completions", fmt.Sprintf(`{"model":"model-a","messages":[{"role":"user","content":"hi"}],"stream":%t,"max_tokens":17,"enable_thinking":false,"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]}`, stream), key["apiKey"].(string), false)
				if r.Code != 200 || !strings.Contains(r.Body.String(), "tool_calls") {
					t.Fatal(r.Body.String())
				}
				if stream && !strings.Contains(r.Body.String(), "[DONE]") {
					t.Fatal("stream not relayed")
				}
			}
			m.llmClient = &http.Client{Transport: llmRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"30"}}, Body: io.NopCloser(bytes.NewBufferString(`{"error":{"message":"quota exceeded"}}`))}, nil
			})}
			r = llmTestRequest(m, "POST", "/llm/v1/chat/completions", `{"model":"model-a","messages":[]}`, key["apiKey"].(string), false)
			if r.Code != 429 || r.Header().Get("Retry-After") != "30" || !strings.Contains(r.Body.String(), "quota exceeded") {
				t.Fatal("upstream failure not preserved")
			}
		})
	}
}

func TestLLMProviderAutomaticIDs(t *testing.T) {
	m := newLLMTestManager(t)
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		r := llmTestRequest(m, "POST", "/v1/admin/llm/providers", `{"kind":"openai","preset":"aliyun-tokenplan","name":"My plan","apiKey":"test-key","models":["model-a"],"enabled":true}`, "", true)
		var result struct {
			ProviderID string `json:"providerId"`
		}
		json.Unmarshal(r.Body.Bytes(), &result)
		if r.Code != 200 || !strings.HasPrefix(result.ProviderID, "aliyun-tokenplan-") || ids[result.ProviderID] {
			t.Fatalf("invalid generated ID: %s", r.Body.String())
		}
		ids[result.ProviderID] = true
		r = llmTestRequest(m, "PUT", "/v1/admin/llm/providers/"+result.ProviderID, `{"kind":"openai","preset":"aliyun-tokenplan","name":"Renamed plan","models":["model-a"],"enabled":true}`, "", true)
		if r.Code != 200 || !strings.Contains(r.Body.String(), result.ProviderID) {
			t.Fatal("rename changed routing ID")
		}
	}
}
