package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

func availabilityRequest(m *Manager, query string) *httptest.ResponseRecorder {
	r := gin.New()
	r.GET("/status", m.resourceAvailability)
	out := httptest.NewRecorder()
	r.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/status"+query, nil))
	return out
}
func TestResourceAvailabilityOnlyQueriesRequestedTables(t *testing.T) {
	m := newLLMTestManager(t)
	// Deliberately omit the NetEase table: single-pool checks must still succeed.
	if err := m.wdb.Db.AutoMigrate(&schema.XboxChild{}); err != nil {
		t.Fatal(err)
	}
	var queried []string
	if err := m.wdb.Db.Callback().Query().Before("gorm:query").Register("test_inventory_queries", func(db *gorm.DB) { queried = append(queried, db.Statement.Table) }); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?resources=", "?resources=unknown", "?resources=xbox-child,unknown", "?resources=xbox-child,"} {
		r := availabilityRequest(m, query)
		if r.Code != 400 {
			t.Fatalf("%s: %d %s", query, r.Code, r.Body.String())
		}
	}
	if len(queried) != 0 {
		t.Fatal("invalid selection queried database", queried)
	}
	r := availabilityRequest(m, "?resources=xbox-child,xbox-child&resources=xbox-child")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"exhausted"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	if len(queried) != 1 || queried[0] != "manager_xbox_children" {
		t.Fatal("unexpected queries", queried)
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("inventory should not be cached")
	}
	if err := m.wdb.Db.Create(&schema.XboxChild{ID: "secret-id", Email: "private@example.com", Password: "private-password"}).Error; err != nil {
		t.Fatal(err)
	}
	r = availabilityRequest(m, "?resources=xbox-child")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"allAvailable":true`) {
		t.Fatal(r.Body.String())
	}
	if strings.Contains(r.Body.String(), "private") || strings.Contains(r.Body.String(), "secret-id") {
		t.Fatal("private inventory exposed")
	}
	if err := m.wdb.Db.Model(&schema.XboxChild{}).Where("id = ?", "secret-id").Update("hub_access_key_id", "owner").Error; err != nil {
		t.Fatal(err)
	}
	r = availabilityRequest(m, "?resources=xbox-child")
	if !strings.Contains(r.Body.String(), `"allAvailable":false`) {
		t.Fatal("stale availability", r.Body.String())
	}
	r = availabilityRequest(m, "?resources=netease")
	if r.Code != 503 || !strings.Contains(r.Body.String(), `"status":"unknown"`) || strings.Contains(r.Body.String(), "exhausted") {
		t.Fatal("database failure misreported", r.Body.String())
	}
}
func TestResourceAvailabilityCombinedAndNoDatabase(t *testing.T) {
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.XboxChild{}, &schema.NetEaseAccount{}); err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Create(&schema.NetEaseAccount{ID: "n", Username: "n@example.com", Password: "secret"}).Error; err != nil {
		t.Fatal(err)
	}
	r := llmTestRequest(m, "GET", "/v1/resource-availability?resources=netease,xbox-child", "", "", false)
	var body struct {
		AllAvailable bool                       `json:"allAvailable"`
		Resources    []resourceAvailabilityItem `json:"resources"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || body.AllAvailable || len(body.Resources) != 2 || body.Resources[0].Status != "available" || body.Resources[1].Code != "RESOURCE_POOL_EXHAUSTED" {
		t.Fatal(r.Body.String())
	}
	m.wdb = nil
	r = availabilityRequest(m, "?resources=netease")
	if r.Code != 503 {
		t.Fatal(r.Code)
	}
}
func TestResourceExhaustionErrorsAndExistingAllocations(t *testing.T) {
	for _, tc := range []struct{ resource, path string }{{"xbox-child", "/v1/xbox-child"}, {"netease", "/v1/netease-account"}} {
		t.Run(tc.resource, func(t *testing.T) {
			m, _ := setupLLMResources(t)
			if err := m.wdb.Db.AutoMigrate(&schema.XboxChild{}, &schema.NetEaseAccount{}); err != nil {
				t.Fatal(err)
			}
			r := llmTestRequest(m, "GET", tc.path, "", "hub-a", false)
			if r.Code != 409 || !strings.Contains(r.Body.String(), `"code":"RESOURCE_POOL_EXHAUSTED"`) || !strings.Contains(r.Body.String(), `"resource":"`+tc.resource+`"`) || !strings.Contains(r.Body.String(), "wait_for_restock") {
				t.Fatal(r.Code, r.Body.String())
			}
			model := any(&schema.XboxChild{ID: "one", Email: "one@example.com", Password: "secret"})
			if tc.resource == "netease" {
				model = &schema.NetEaseAccount{ID: "one", Username: "one@example.com", Password: "secret"}
			}
			if err := m.wdb.Db.Create(model).Error; err != nil {
				t.Fatal(err)
			}
			r = llmTestRequest(m, "GET", tc.path, "", "hub-a", false)
			if r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
			r = availabilityRequest(m, "?resources="+tc.resource)
			if !strings.Contains(r.Body.String(), `"allAvailable":false`) {
				t.Fatal(r.Body.String())
			}
			r = llmTestRequest(m, "GET", tc.path, "", "hub-a", false)
			if r.Code != 200 {
				t.Fatal("existing owner was blocked", r.Body.String())
			}
		})
	}
}
