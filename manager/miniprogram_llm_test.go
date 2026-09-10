package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vertrai/hub/manager/schema"
)

func TestMiniProgramProvisionAllocatesLLMBeforePod(t *testing.T) {
	for _, template := range []string{miniProgramTemplateMicAI, miniProgramTemplateTax} {
		for _, configured := range []bool{true, false} {
			name := template + "/configured"
			if !configured {
				name = template + "/missing-settings"
			}
			t.Run(name, func(t *testing.T) {
				m, _ := setupLLMResources(t)
				if err := m.wdb.Db.AutoMigrate(&schema.AccessKey{}, &schema.HymatrixPod{}, &schema.MiniProgramAgentTask{}); err != nil {
					t.Fatal(err)
				}
				if !configured {
					if err := m.wdb.Db.Model(&schema.LLMResourceSettings{}).Where("id = ?", "default").Update("base_url", "").Error; err != nil {
						t.Fatal(err)
					}
				}
				resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Method + " " + r.URL.Path {
					case "POST /v1/internal/access-keys":
						var input struct {
							OwnerUserID string `json:"ownerUserId"`
						}
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.OwnerUserID != "wx-test" {
							t.Error("wrong Hub key owner")
						}
						io.WriteString(w, `{"accessKey":{"id":"resource-key","keyPrefix":"hub-test"},"gatewayApiKey":"hub-test"}`)
					case "GET /v1/access-key":
						if r.Header.Get("Authorization") != "Bearer hub-test" {
							t.Error("LLM allocation must authenticate with the newly created Hub key")
						}
						io.WriteString(w, `{"accessKey":{"id":"resource-key","ownerUserId":"wx-test","status":"active"}}`)
					default:
						t.Errorf("unexpected Resources request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer resources.Close()
				m.resources = NewResourcesClient(ResourcesConfig{BaseURL: resources.URL})
				previous := nodeInfoHTTPClient
				t.Cleanup(func() { nodeInfoHTTPClient = previous })
				nodeCalled := false
				nodeInfoHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					nodeCalled = true
					var count int64
					m.wdb.Db.Model(&schema.LLMKey{}).Where("hub_access_key_id = ?", "resource-key").Count(&count)
					if count != 1 {
						t.Error("Pod provisioning reached node before allocating its LLM key")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"Node":{"Acc-Id":"scheduler"}}`))}, nil
				})}
				// Empty runtime stops at Spawn validation, after the real Pod persistence,
				// without creating a remote container or requiring a node SDK test double.
				m.config.MiniProgram = MiniProgramConfig{NodeURL: "https://1.1.1.1", PrivateKey: strings.Repeat("0", 63) + "1", MicAIModule: "micai-module", TaxModule: "tax-module"}
				task := schema.MiniProgramAgentTask{ID: "task", UserID: "wx-test", Template: template, Status: schema.MiniProgramTaskSpawning}
				if err := m.wdb.Db.Create(&task).Error; err != nil {
					t.Fatal(err)
				}
				err := m.provisionMiniProgramPod(t.Context(), &task)
				if !configured {
					if err == nil || !strings.Contains(err.Error(), "allocate LLM resource") || nodeCalled || task.PodID != "" {
						t.Fatalf("missing LLM settings must stop before provisioning: err=%v node=%v pod=%s", err, nodeCalled, task.PodID)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "runtimeType is required") {
					t.Fatalf("expected local Spawn validation stop, got %v", err)
				}
				var pod schema.HymatrixPod
				if err := m.wdb.Db.First(&pod, "id = ?", task.PodID).Error; err != nil {
					t.Fatal(err)
				}
				resource := acquireResourceTest(t, m, "hub-test")
				if pod.LLMAPIKey != resource.APIKey || pod.LLMAPIKey == "hub-test" || pod.LLMBaseURL != resource.BaseURL || pod.LLMModel != "hub-chat" || pod.LLMProvider != "custom" || pod.GatewayAPIKey != "hub-test" {
					t.Fatal("Pod did not persist its Hub LLM configuration before Spawn")
				}
				if pod.Module != miniProgramModuleForTemplate(m.config.MiniProgram, template) {
					t.Fatal("wrong template module")
				}
				var count int64
				m.wdb.Db.Model(&schema.LLMKey{}).Count(&count)
				if count != 1 {
					t.Fatalf("reacquiring created duplicate LLM keys: %d", count)
				}
			})
		}
	}
}
