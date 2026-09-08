package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vertrai/hub/manager/schema"
)

func setupLLMResources(t *testing.T) (*Manager, func(string, string) *httptest.ResponseRecorder) {
	t.Helper()
	m := newLLMTestManager(t)
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/access-key" {
			t.Errorf("unexpected resource endpoint: %s", r.URL.Path)
		}
		secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if secret != "hub-a" && secret != "hub-b" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, `{"accessKey":{"id":%q,"ownerUserId":%q,"status":"active"}}`, secret, "owner-"+secret)
	}))
	t.Cleanup(resources.Close)
	m.resources = NewResourcesClient(ResourcesConfig{BaseURL: resources.URL})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Authorization") != "Bearer upstream" {
			t.Error("wrong upstream credential")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": body["model"], "choices": []any{map[string]any{"message": map[string]any{"content": body["model"]}}}})
	}))
	t.Cleanup(upstream.Close)
	r := llmTestRequest(m, "PUT", "/v1/admin/llm/providers/vendor", fmt.Sprintf(`{"kind":"openai","baseUrl":%q,"models":["model-a","model-b"],"apiKey":"upstream","enabled":true}`, upstream.URL), "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	publishLLMTestRoute(t, m, "vendor", "model-a")
	publishLLMTestRoute(t, m, "vendor", "model-b")
	r = llmTestRequest(m, "PUT", "/v1/admin/llm/resource-settings", `{"baseUrl":"https://hub.example/llm/v1","allowedModels":["model-a"],"defaultModel":"model-a"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	return m, func(secret, model string) *httptest.ResponseRecorder {
		return llmTestRequest(m, "POST", "/llm/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model), secret, false)
	}
}
func acquireResourceTest(t *testing.T, m *Manager, hubKey string) llmResourceConfig {
	t.Helper()
	r := llmTestRequest(m, "GET", "/v1/llm", "", hubKey, false)
	if r.Code != 200 {
		t.Fatalf("acquire %d: %s", r.Code, r.Body.String())
	}
	var resource llmResourceConfig
	if err := json.Unmarshal(r.Body.Bytes(), &resource); err != nil {
		t.Fatal(err)
	}
	return resource
}
func TestLLMResourceIdempotencyPolicyAndLiveUpgrade(t *testing.T) {
	m, chat := setupLLMResources(t)
	a := acquireResourceTest(t, m, "hub-a")
	again := acquireResourceTest(t, m, "hub-a")
	if a.APIKey == "" || a.APIKey != again.APIKey || a.KeyID != again.KeyID || a.Model != "hub-chat" || a.Provider != "custom" || a.BaseURL != "https://hub.example/llm/v1" || len(a.Models) != 1 {
		t.Fatal("invalid resource contract")
	}
	if r := chat(a.APIKey, "hub-chat"); r.Code != 200 || !strings.Contains(r.Body.String(), "model-a") {
		t.Fatal(r.Body.String())
	}
	for _, model := range []string{"model-b", "vendor/model-b", "vendor/model-a"} {
		if r := chat(a.APIKey, model); r.Code != 403 {
			t.Fatalf("unauthorized model %s: %d", model, r.Code)
		}
	}
	r := llmTestRequest(m, "GET", "/llm/v1/models", "", a.APIKey, false)
	if !strings.Contains(r.Body.String(), "hub-chat") || strings.Contains(r.Body.String(), "model-b") {
		t.Fatal("model catalog leaks unauthorized model")
	}
	r = llmTestRequest(m, "PATCH", "/v1/admin/llm/keys/"+a.KeyID+"/policy", `{"allowedModels":["model-a"],"defaultModel":"model-b"}`, "", true)
	if r.Code != 400 {
		t.Fatal("default outside policy accepted")
	}
	r = llmTestRequest(m, "PATCH", "/v1/admin/llm/keys/"+a.KeyID+"/policy", `{"allowedModels":["model-a","model-b"],"defaultModel":"model-b"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r = chat(a.APIKey, "hub-chat"); r.Code != 200 || !strings.Contains(r.Body.String(), "model-b") {
		t.Fatal("live upgrade did not change routing")
	}
	upgraded := acquireResourceTest(t, m, "hub-a")
	if upgraded.APIKey != a.APIKey || upgraded.DefaultModel != "model-b" {
		t.Fatal("upgrade rotated key")
	}
	r = llmTestRequest(m, "PATCH", "/v1/admin/llm/keys/"+a.KeyID+"/policy", `{"allowedModels":[],"defaultModel":""}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r = chat(a.APIKey, "model-a"); r.Code != 403 {
		t.Fatal("empty policy granted model access")
	}
}
func TestLLMResourceDefaultsOwnershipAndRevocation(t *testing.T) {
	m, _ := setupLLMResources(t)
	a := acquireResourceTest(t, m, "hub-a")
	r := llmTestRequest(m, "PUT", "/v1/admin/llm/resource-settings", `{"baseUrl":"https://hub.example/llm/v1","allowedModels":["model-b"],"defaultModel":"model-b"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	b := acquireResourceTest(t, m, "hub-b")
	again := acquireResourceTest(t, m, "hub-a")
	if b.APIKey == a.APIKey || b.DefaultModel != "model-b" || again.DefaultModel != "model-a" {
		t.Fatal("defaults modified existing allocation or ownership mixed")
	}
	r = llmTestRequest(m, "GET", "/v1/llm", "", "unknown-hub-key", false)
	if r.Code != 401 {
		t.Fatal("invalid hub key accepted")
	}
	r = llmTestRequest(m, "PATCH", "/v1/admin/llm/keys/"+a.KeyID+"/policy", `{"allowedModels":["model-b"],"defaultModel":"model-b"}`, "hub-a", false)
	if r.Code != 401 {
		t.Fatal("holder could upgrade own permissions")
	}
	r = llmTestRequest(m, "DELETE", "/v1/admin/llm/keys/"+a.KeyID, "", "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "GET", "/v1/llm", "", "hub-a", false)
	if r.Code != 403 {
		t.Fatal("revoked allocation was recreated")
	}
	r = llmTestRequest(m, "GET", "/llm/v1/models", "", a.APIKey, false)
	if r.Code != 401 {
		t.Fatal("revoked key accepted")
	}
	r = llmTestRequest(m, "GET", "/v1/admin/llm/keys", "", "", true)
	if strings.Contains(r.Body.String(), b.APIKey) || strings.Contains(r.Body.String(), a.APIKey) {
		t.Fatal("admin listing exposed resource secrets")
	}
}
func TestLLMResourceConcurrentAllocation(t *testing.T) {
	m, _ := setupLLMResources(t)
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resource, err := m.acquireLLMResource(t.Context(), "hub-a")
			if err != nil {
				t.Error(err)
				return
			}
			results <- resource.APIKey
		}()
	}
	wg.Wait()
	close(results)
	secret := ""
	for value := range results {
		if secret != "" && secret != value {
			t.Error("duplicate allocation")
		}
		secret = value
	}
	var count int64
	m.wdb.Db.Model(&schema.LLMKey{}).Count(&count)
	if count != 1 {
		t.Fatalf("allocation count %d", count)
	}
}
func TestHermesLLMResourceUsesHubChatAndChecksExplicitModel(t *testing.T) {
	m, _ := setupLLMResources(t)
	resource, err := m.hermesLLMResource(t.Context(), "hub-a", "")
	if err != nil || resource.Model != "hub-chat" || resource.Provider != "custom" {
		t.Fatalf("bad Hermes config: %v", err)
	}
	explicit, err := m.hermesLLMResource(t.Context(), "hub-a", "model-a")
	if err != nil || explicit.Model != "model-a" {
		t.Fatal("explicit allowed model rejected")
	}
	if _, err := m.hermesLLMResource(t.Context(), "hub-a", "model-b"); err == nil {
		t.Fatal("unauthorized explicit model accepted")
	}
}
func TestManualLLMKeyRequiresExplicitPolicy(t *testing.T) {
	m, _ := setupLLMResources(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/llm/keys", strings.NewReader(`{"name":"no policy"}`))
	req.Header.Set("Content-Type", "application/json")
	authenticateAdmin(m, req)
	rec := httptest.NewRecorder()
	m.router().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatal("manual key created without explicit policy")
	}
}

func TestLLMOnlyExplicitRoutesArePublished(t *testing.T) {
	m := newLLMTestManager(t)
	provider := `{"kind":"openai","baseUrl":"https://example.com/v1","models":["source-a","source-b"],"apiKey":"test","enabled":true}`
	save := func() {
		t.Helper()
		r := llmTestRequest(m, "PUT", "/v1/admin/llm/providers/source", provider, "", true)
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
	}
	save()
	models, err := m.availableLLMModels()
	if err != nil || len(models) != 0 {
		t.Fatalf("provider published models: %v %v", models, err)
	}
	r := llmTestRequest(m, "PUT", "/v1/admin/llm/routes", `{"id":"public-chat","providerId":"source","upstreamModel":"source-a"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	save()
	models, err = m.availableLLMModels()
	if err != nil || len(models) != 1 || models[0].ID != "public-chat" {
		t.Fatalf("unexpected public catalog: %v %v", models, err)
	}
	r = llmTestRequest(m, "POST", "/v1/admin/llm/keys", `{"name":"test","allowedModels":["source-a"],"defaultModel":"source-a"}`, "", true)
	if r.Code != 400 {
		t.Fatalf("source model accepted: %s", r.Body.String())
	}
	r = llmTestRequest(m, "POST", "/v1/admin/llm/keys", `{"name":"test","allowedModels":["public-chat"],"defaultModel":"public-chat"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var issued struct {
		APIKey string `json:"apiKey"`
	}
	json.Unmarshal(r.Body.Bytes(), &issued)
	r = llmTestRequest(m, "GET", "/llm/v1/models", "", issued.APIKey, false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "public-chat") || strings.Contains(r.Body.String(), "source-a") {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "DELETE", "/v1/admin/llm/routes/public-chat", "", "", false)
	if r.Code != 401 {
		t.Fatalf("route deletion not protected: %d", r.Code)
	}
	r = llmTestRequest(m, "DELETE", "/v1/admin/llm/routes/public-chat", "", "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	save()
	models, err = m.availableLLMModels()
	if err != nil || len(models) != 0 {
		t.Fatalf("deleted route was recreated: %v %v", models, err)
	}
	for _, model := range []string{"hub-chat", "public-chat", "source-a", "source/source-a"} {
		r = llmTestRequest(m, "POST", "/llm/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[]}`, model), issued.APIKey, false)
		if r.Code < 400 {
			t.Fatalf("unpublished model %s accepted: %d", model, r.Code)
		}
	}
}

func TestAdminBoundLLMCreation(t *testing.T) {
	m, chat := setupLLMResources(t)
	if err := m.wdb.Db.AutoMigrate(&schema.AccessKey{}); err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Create(&schema.AccessKey{ID: "local-a", ResourceKeyID: "hub-a", UserID: "owner-hub-a", Secret: "hub-a", Status: "available"}).Error; err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/access-keys/hub-a/llm-resource"
	r := llmTestRequest(m, "GET", path+"?userId=owner-hub-a", "", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"exists":false`) {
		t.Fatal(r.Body.String())
	}
	var count int64
	m.wdb.Db.Model(&schema.LLMKey{}).Count(&count)
	if count != 0 {
		t.Fatal("read allocated key")
	}
	body := `{"userId":"owner-hub-a","allowedModels":["model-b"],"defaultModel":"model-b"}`
	r = llmTestRequest(m, "POST", path, strings.ReplaceAll(body, "owner-hub-a", "wrong-user"), "", true)
	if r.Code != 404 {
		t.Fatal("owner check failed", r.Code)
	}
	r = llmTestRequest(m, "POST", path, body, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var result struct {
		APIKey string `json:"apiKey"`
	}
	json.Unmarshal(r.Body.Bytes(), &result)
	if result.APIKey == "" {
		t.Fatal("missing key")
	}
	r = chat(result.APIKey, "hub-chat")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "model-b") {
		t.Fatal(r.Body.String())
	}
	r = chat(result.APIKey, "model-a")
	if r.Code != 403 {
		t.Fatal("manual policy ignored")
	}
	r = llmTestRequest(m, "POST", path, body, "", true)
	if r.Code != 409 {
		t.Fatal("duplicate allocation", r.Code)
	}
	r = llmTestRequest(m, "GET", path+"?userId=owner-hub-a", "", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), result.APIKey) {
		t.Fatal("existing resource not returned", r.Body.String())
	}
	resource := acquireResourceTest(t, m, "hub-a")
	if resource.APIKey != result.APIKey {
		t.Fatal("automatic retrieval differs from manual allocation")
	}
}

func TestLLMSettingsIndependentSave(t *testing.T) {
	m, _ := setupLLMResources(t)
	r := llmTestRequest(m, "PUT", "/v1/admin/llm/resource-settings", `{"baseUrl":"https://new.example/llm/v1"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	s, err := m.llmSettings()
	if err != nil || s.DefaultModel != "model-a" || s.AllowedModels != `["model-a"]` {
		t.Fatalf("address save changed policy: %+v %v", s, err)
	}
	r = llmTestRequest(m, "PUT", "/v1/admin/llm/resource-settings", `{"allowedModels":["model-b"],"defaultModel":"model-b"}`, "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	s, err = m.llmSettings()
	if err != nil || s.BaseURL != "https://new.example/llm/v1" || s.DefaultModel != "model-b" {
		t.Fatalf("policy save changed address: %+v %v", s, err)
	}
}
