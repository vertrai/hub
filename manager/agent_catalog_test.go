package manager

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func catalogFixture(t *testing.T) *Manager {
	t.Helper()
	dsn := os.Getenv("HUB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set HUB_TEST_POSTGRES_DSN to an isolated database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	if err = db.AutoMigrate(&schema.User{}, &schema.MiniProgramAgentTask{}, &schema.AgentCatalogEntry{}, &schema.AgentCatalogImage{}, &schema.HymatrixPod{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	t.Cleanup(func() { tx.Rollback() })
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	if err = seedAgentCatalog(tx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"wx_catalog_owner", "wx_catalog_other"} {
		if err = tx.Create(&schema.User{ID: id, Name: id, Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{wdb: &Wdb{Db: tx}, config: Config{MiniProgram: MiniProgramConfig{AppSecret: "test"}}}
}
func catalogRequest(m *Manager, method, path, body, user string, handler gin.HandlerFunc, params gin.Params) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if user != "" {
		c.Request.Header.Set("Authorization", "Bearer "+m.signMiniProgramSession(user, time.Now().Add(time.Hour)))
	}
	c.Params = params
	handler(c)
	return rec
}
func TestCatalogLifecycleAndOwnership(t *testing.T) {
	m := catalogFixture(t)
	entry := schema.AgentCatalogEntry{ID: "third-agent", Name: "第三个助手", LogoURL: "https://example.com/icon.png", Intro: "助手介绍", Capabilities: []string{"整理"}, Module: "third-module", Published: true}
	body, _ := json.Marshal(entry)
	rec := catalogRequest(m, "POST", "/", string(body), "", m.adminSaveAgentCatalog, nil)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	task, err := m.reserveMiniProgramAgentTask("wx_catalog_owner", entry.ID, "hash")
	if err != nil {
		t.Fatal(err)
	}
	if task.Template != entry.ID || task.ModuleSnapshot != "third-module" || task.NameSnapshot != entry.Name {
		t.Fatalf("wrong routing: %+v", task)
	}
	if _, err = m.reserveMiniProgramAgentTask("wx_catalog_owner", entry.ID, "hash"); !errors.Is(err, errMiniProgramAgentAlreadyActive) {
		t.Fatalf("duplicate creation: %v", err)
	}
	entry.Published = false
	entry.Module = "next-module"
	body, _ = json.Marshal(entry)
	rec = catalogRequest(m, "PUT", "/", string(body), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if _, err = m.reserveMiniProgramAgentTask("wx_catalog_other", entry.ID, "hash"); err != nil {
		t.Fatalf("unlisted agent could not be created directly: %v", err)
	}
	var persisted schema.MiniProgramAgentTask
	m.wdb.Db.First(&persisted, "id = ?", task.ID)
	if persisted.ModuleSnapshot != "third-module" {
		t.Fatal("editing changed existing instance module")
	}
	rec = catalogRequest(m, "GET", "/", "", "", m.listAgentCatalog, nil)
	if strings.Contains(rec.Body.String(), "third-agent") || strings.Contains(rec.Body.String(), "module") {
		t.Fatal(rec.Body.String())
	}
	rec = catalogRequest(m, "GET", "/", "", "wx_catalog_owner", m.listMyMiniProgramAgents, nil)
	if !strings.Contains(rec.Body.String(), task.ID) || strings.Contains(rec.Body.String(), "third-module") {
		t.Fatal(rec.Body.String())
	}
	rec = catalogRequest(m, "GET", "/", "", "wx_catalog_other", m.listMyMiniProgramAgents, nil)
	if strings.Contains(rec.Body.String(), task.ID) {
		t.Fatal("cross-user instance leak")
	}
	for _, tc := range []struct {
		user string
		want int
	}{{"wx_catalog_owner", 200}, {"wx_catalog_other", 200}, {"", 200}} {
		rec = catalogRequest(m, "GET", "/", "", tc.user, m.getAgentCatalogEntry, gin.Params{{Key: "id", Value: entry.ID}})
		if rec.Code != tc.want {
			t.Fatalf("detail user=%s status=%d body=%s", tc.user, rec.Code, rec.Body.String())
		}
	}
	rec = catalogRequest(m, "DELETE", "/", "", "", m.adminDeleteAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if rec.Code != 409 {
		t.Fatal("deleted an owned catalog entry")
	}
	rec = catalogRequest(m, "GET", "/", "", "wx_catalog_other", m.getMiniProgramAgent, gin.Params{{Key: "taskId", Value: task.ID}})
	if rec.Code != 404 {
		t.Fatal("cross-user task access")
	}
}
func TestCatalogValidation(t *testing.T) {
	good := schema.AgentCatalogEntry{ID: "new-agent", Name: "助手", LogoURL: "https://example.com/icon.png", Intro: "介绍", Capabilities: []string{"整理"}, Module: "module"}
	if err := validateCatalogEntry(good); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*schema.AgentCatalogEntry){func(a *schema.AgentCatalogEntry) { a.ID = "../bad" }, func(a *schema.AgentCatalogEntry) { a.Module = "" }, func(a *schema.AgentCatalogEntry) { a.LogoURL = "javascript:alert(1)" }, func(a *schema.AgentCatalogEntry) { a.Capabilities = nil }} {
		a := good
		mutate(&a)
		if validateCatalogEntry(a) == nil {
			t.Fatalf("accepted invalid entry: %+v", a)
		}
	}
}
func TestCatalogAdminRoutesRequireAuthentication(t *testing.T) {
	m, err := New("test", Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := m.router()
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		path := "/v1/admin/agent-catalog"
		if method == "PUT" || method == "DELETE" {
			path += "/test"
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != 401 {
			t.Fatalf("%s %s = %d", method, path, rec.Code)
		}
	}
}

func TestCatalogCurrentRequiresAgentID(t *testing.T) {
	m := catalogFixture(t)
	rec := catalogRequest(m, "GET", "/?template=tax-agent", "", "wx_catalog_owner", m.getCurrentMiniProgramAgent, nil)
	if rec.Code != 400 {
		t.Fatalf("legacy parameter accepted: %d %s", rec.Code, rec.Body.String())
	}
	rec = catalogRequest(m, "GET", "/?agentId=tax-agent", "", "wx_catalog_owner", m.getCurrentMiniProgramAgent, nil)
	if rec.Code != 200 {
		t.Fatalf("agentId rejected: %d %s", rec.Code, rec.Body.String())
	}
	payload := m.miniProgramTaskResponse(schema.MiniProgramAgentTask{Template: "tax-agent"}, "")
	if payload["agentId"] != "tax-agent" {
		t.Fatal("missing agentId")
	}
	if _, exists := payload["template"]; exists {
		t.Fatal("legacy response field retained")
	}
}

func TestCatalogDeleteFailedAndRecreate(t *testing.T) {
	m := catalogFixture(t)
	task, err := m.reserveMiniProgramAgentTask("wx_catalog_owner", miniProgramTemplateTax, "hash")
	if err != nil {
		t.Fatal(err)
	}
	params := gin.Params{{Key: "taskId", Value: task.ID}}
	rec := catalogRequest(m, "DELETE", "/", "", "wx_catalog_other", m.deleteFailedMiniProgramAgent, params)
	if rec.Code != 404 {
		t.Fatal("cross-user deletion accepted")
	}
	rec = catalogRequest(m, "DELETE", "/", "", "wx_catalog_owner", m.deleteFailedMiniProgramAgent, params)
	if rec.Code != 409 {
		t.Fatal("active task deletion accepted")
	}
	m.wdb.Db.Model(&task).Updates(map[string]any{"status": schema.MiniProgramTaskFailed, "pod_id": "failed-pod"})
	rec = catalogRequest(m, "DELETE", "/", "", "wx_catalog_owner", m.deleteFailedMiniProgramAgent, params)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = catalogRequest(m, "GET", "/", "", "wx_catalog_owner", m.listMyMiniProgramAgents, nil)
	if strings.Contains(rec.Body.String(), task.ID) {
		t.Fatal("deleted task remains visible")
	}
	rec = catalogRequest(m, "GET", "/?agentId=tax-agent", "", "wx_catalog_owner", m.getCurrentMiniProgramAgent, nil)
	if !strings.Contains(rec.Body.String(), `"task":null`) {
		t.Fatal(rec.Body.String())
	}
	replacement, err := m.reserveMiniProgramAgentTask("wx_catalog_owner", miniProgramTemplateTax, "hash")
	if err != nil || replacement.ID == task.ID {
		t.Fatalf("cannot recreate: %v", err)
	}
	var retained schema.MiniProgramAgentTask
	if err := m.wdb.Db.Unscoped().First(&retained, "id = ?", task.ID).Error; err != nil || !retained.DeletedAt.Valid {
		t.Fatal("failure history lost")
	}
	m.wdb.Db.Model(&replacement).Update("status", schema.MiniProgramTaskFailed)
	catalogRequest(m, "DELETE", "/", "", "wx_catalog_owner", m.deleteFailedMiniProgramAgent, gin.Params{{Key: "taskId", Value: replacement.ID}})
	third, err := m.reserveMiniProgramAgentTask("wx_catalog_owner", miniProgramTemplateTax, "hash")
	if err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.Model(&third).Update("status", schema.MiniProgramTaskFailed)
	catalogRequest(m, "DELETE", "/", "", "wx_catalog_owner", m.deleteFailedMiniProgramAgent, gin.Params{{Key: "taskId", Value: third.ID}})
	if _, err := m.reserveMiniProgramAgentTask("wx_catalog_owner", miniProgramTemplateTax, "hash"); !errors.Is(err, errMiniProgramProvisionRateLimited) {
		t.Fatal("deletion bypassed rate limit")
	}
}

const miniProgramTemplateTax = "tax-agent"
const miniProgramTemplateMicAI = "micai-agent"

// Test data only: production starts with an empty catalog.
func seedAgentCatalog(db *gorm.DB) error {
	for _, id := range []string{miniProgramTemplateTax, miniProgramTemplateMicAI} {
		entry := schema.AgentCatalogEntry{ID: id, Name: id, Intro: "test", LogoURL: "https://example.com/icon.png", Capabilities: []string{"test"}, Module: "configured-" + id, Published: true}
		if err := db.Create(&entry).Error; err != nil {
			return err
		}
	}
	return nil
}

func TestCatalogFormerBuiltinEntriesFollowOrdinaryRules(t *testing.T) {
	m := catalogFixture(t)
	for _, id := range []string{"tax-agent", "micai-agent"} {
		var entry schema.AgentCatalogEntry
		if err := m.wdb.Db.First(&entry, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		entry.Module = ""
		if validateCatalogEntry(entry) == nil {
			t.Fatal("missing module accepted for former built-in")
		}
		m.wdb.Db.Model(&entry).Update("published", false)
		rec := catalogRequest(m, "DELETE", "/", "", "", m.adminDeleteAgentCatalog, gin.Params{{Key: "id", Value: id}})
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
}
