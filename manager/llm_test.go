package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

func newLLMTestManager(t *testing.T) *Manager {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&schema.LLMRoute{}, &schema.LLMResourceSettings{}, &schema.LLMProvider{}, &schema.LLMKey{}); err != nil {
		t.Fatal(err)
	}
	m, err := New("test", Config{}, &Wdb{Db: db})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func llmTestRequest(m *Manager, method, path, body, key string, admin bool) *httptest.ResponseRecorder {
	// Tests explicitly issue keys for the currently configured public models.
	if method == "POST" && path == "/v1/admin/llm/keys" {
		var input map[string]any
		if json.Unmarshal([]byte(body), &input) == nil {
			if _, set := input["allowedModels"]; !set {
				models, err := m.availableLLMModels()
				if err != nil {
					panic(err)
				}
				allowed := []string{}
				for _, model := range models {
					allowed = append(allowed, model.ID)
				}
				input["allowedModels"] = allowed
				if len(allowed) > 0 {
					input["defaultModel"] = allowed[0]
				}
				raw, _ := json.Marshal(input)
				body = string(raw)
			}
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if admin {
		authenticateAdmin(m, req)
	}
	rec := httptest.NewRecorder()
	m.router().ServeHTTP(rec, req)
	return rec
}
func TestLLMProviderAndKeyLifecycle(t *testing.T) {
	m := newLLMTestManager(t)
	if r := llmTestRequest(m, "GET", "/v1/admin/llm/providers", "", "", false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := llmTestRequest(m, "GET", "/llm/v1/models", "", "", false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Errorf("wrong upstream request")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "model-a" {
			t.Errorf("wrong model: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"hello"}}]}`)
	}))
	defer upstream.Close()
	payload := fmt.Sprintf(`{"kind":"openai","baseUrl":%q,"models":["model-a"],"credential":{"api_key":"upstream-secret"},"enabled":true}`, upstream.URL+"/v1")
	if r := llmTestRequest(m, "PUT", "/v1/admin/llm/providers/demo", payload, "", true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r := llmTestRequest(m, "GET", "/v1/admin/llm/providers", "", "", true)
	if strings.Contains(r.Body.String(), "upstream-secret") || !strings.Contains(r.Body.String(), "model-a") {
		t.Fatal(r.Body.String())
	}
	publishLLMTestRoute(t, m, "demo", "model-a")
	r = llmTestRequest(m, "POST", "/v1/admin/llm/keys", `{"name":"test"}`, "", true)
	var issued struct {
		APIKey string        `json:"apiKey"`
		Key    schema.LLMKey `json:"key"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &issued); err != nil || issued.APIKey == "" {
		t.Fatal(r.Body.String())
	}
	key := issued.APIKey
	r = llmTestRequest(m, "GET", "/llm/v1/models", "", key, false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "model-a") {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "POST", "/llm/v1/chat/completions", `{"model":"model-a","messages":[{"role":"user","content":"hi"}]}`, key, false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "hello") {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "POST", "/llm/v1/responses", `{"model":"unknown"}`, key, false)
	if r.Code != 403 {
		t.Fatal(r.Code)
	}
	payload = strings.Replace(payload, `"enabled":true`, `"enabled":false`, 1)
	r = llmTestRequest(m, "PUT", "/v1/admin/llm/providers/demo", payload, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "POST", "/llm/v1/responses", `{"model":"model-a"}`, key, false)
	if r.Code != 404 {
		t.Fatal(r.Code)
	}
	r = llmTestRequest(m, "DELETE", "/v1/admin/llm/keys/"+issued.Key.ID, "", "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "GET", "/llm/v1/models", "", key, false)
	if r.Code != 401 {
		t.Fatal(r.Code)
	}
}
func TestLLMCodexChatAndResponses(t *testing.T) {
	m := newLLMTestManager(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer codex-token" || r.Header.Get("ChatGPT-Account-Id") != "account" {
			t.Error("wrong Codex headers or path")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "model-a" || body["stream"] != true || body["store"] != false || body["max_tokens"] != nil {
			t.Errorf("invalid Codex request: %v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call1\",\"name\":\"weather\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{}\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp1\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"call1\",\"name\":\"weather\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n")
	}))
	defer upstream.Close()
	payload := fmt.Sprintf(`{"kind":"openai-codex","baseUrl":%q,"models":["model-a"],"credential":{"access_token":"codex-token","account_id":"account","expires_at":%q},"enabled":true}`, upstream.URL, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	r := llmTestRequest(m, "PUT", "/v1/admin/llm/providers/codex", payload, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	publishLLMTestRoute(t, m, "codex", "model-a")
	r = llmTestRequest(m, "POST", "/v1/admin/llm/keys", `{"name":"test"}`, "", true)
	var issued map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &issued)
	key := issued["apiKey"].(string)
	for _, stream := range []bool{false, true} {
		for _, endpoint := range []string{"chat/completions", "responses"} {
			input := `"messages":[{"role":"user","content":"hi"}]`
			want := "tool_calls"
			if endpoint == "responses" {
				input = `"input":"hi"`
				want = "function_call"
			}
			r = llmTestRequest(m, "POST", "/llm/v1/"+endpoint, fmt.Sprintf(`{"model":"model-a",%s,"stream":%t,"max_tokens":100}`, input, stream), key, false)
			if r.Code != 200 || !strings.Contains(r.Body.String(), want) {
				t.Fatalf("%s stream=%v: %d %s", endpoint, stream, r.Code, r.Body.String())
			}
			if stream && !strings.Contains(r.Header().Get("Content-Type"), "text/event-stream") {
				t.Error("not SSE")
			}
		}
	}
}

func publishLLMTestRoute(t *testing.T, m *Manager, provider, model string) {
	t.Helper()
	body, _ := json.Marshal(schema.LLMRoute{ID: model, ProviderID: provider, UpstreamModel: model})
	r := llmTestRequest(m, "PUT", "/v1/admin/llm/routes", string(body), "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
}
