package manager

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
)

func TestAgentResourceConfiguration(t *testing.T) {
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.AgentCatalogEntry{}); err != nil {
		t.Fatal(err)
	}
	entry := schema.AgentCatalogEntry{ID: "resource-agent", Name: "助手", LogoURL: "https://example.com/a.png", Intro: "介绍", Module: "module", Capabilities: []string{"聊天"}, RequiredResources: []string{"netease", "xbox-child"}}
	body, _ := json.Marshal(entry)
	r := catalogRequest(m, "POST", "/", string(body), "", m.adminSaveAgentCatalog, nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var stored schema.AgentCatalogEntry
	if err := m.wdb.Db.First(&stored, "id = ?", entry.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.RequiredResources) != 2 {
		t.Fatal(stored.RequiredResources)
	}
	if len(publicCatalogEntry(stored)["requiredResources"].([]string)) != 2 {
		t.Fatal("missing public dependencies")
	}
	entry.RequiredResources = nil
	body, _ = json.Marshal(entry)
	r = catalogRequest(m, "PUT", "/", string(body), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	m.wdb.Db.First(&stored, "id = ?", entry.ID)
	if len(stored.RequiredResources) != 2 {
		t.Fatal("legacy save erased resources")
	}
	entry.RequiredResources = []string{}
	body, _ = json.Marshal(entry)
	r = catalogRequest(m, "PUT", "/", string(body), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	m.wdb.Db.First(&stored, "id = ?", entry.ID)
	if len(stored.RequiredResources) != 0 {
		t.Fatal("explicit clear failed")
	}
	entry.RequiredResources = []string{"custom-resource"}
	if validateCatalogEntry(entry) == nil {
		t.Fatal("unknown resource accepted")
	}
	entry.RequiredResources = []string{"netease"}
	merged, err := mergeCatalogScope(entry, schema.AgentCatalogEntry{Name: entry.Name, LogoURL: entry.LogoURL, Intro: entry.Intro, Module: entry.Module, RequiredResources: []string{"xbox-child"}}, "core")
	if err != nil || len(merged.RequiredResources) != 1 || merged.RequiredResources[0] != "xbox-child" {
		t.Fatal(merged, err)
	}
}

func TestRequiredAgentResourcesFailClosedAndSkipLegacy(t *testing.T) {
	m := newLLMTestManager(t)
	if err := checkRequiredAgentResources(m.wdb.Db, nil); err != nil {
		t.Fatal(err)
	}
	if err := checkRequiredAgentResources(m.wdb.Db, []string{"netease"}); err == nil {
		t.Fatal("missing table allowed")
	}
	if err := m.wdb.Db.AutoMigrate(&schema.NetEaseAccount{}, &schema.XboxChild{}); err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Create(&schema.NetEaseAccount{ID: "n", Username: "n", Password: "secret"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := checkRequiredAgentResources(m.wdb.Db, []string{"netease"}); err != nil {
		t.Fatal(err)
	}
	var exhausted *agentResourceError
	if err := checkRequiredAgentResources(m.wdb.Db, []string{"netease", "xbox-child"}); !errors.As(err, &exhausted) || exhausted.resource != "xbox-child" {
		t.Fatal(err)
	}
	if err := checkRequiredAgentResources(m.wdb.Db, []string{"unknown"}); err == nil {
		t.Fatal("unknown resource allowed")
	}
}

func TestResourceExhaustionDoesNotReserveAgentTask(t *testing.T) {
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.User{}, &schema.AgentCatalogEntry{}, &schema.MiniProgramAgentTask{}, &schema.NetEaseAccount{}); err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Create(&schema.User{ID: "wx_resources", Name: "Resource test", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	entry := schema.AgentCatalogEntry{ID: "resource-agent", Name: "助手", Module: "module", RequiredResources: []string{"netease"}}
	if err := m.wdb.Db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	_, err := m.reserveMiniProgramAgentTask("wx_resources", entry.ID, "hash")
	var exhausted *agentResourceError
	if !errors.As(err, &exhausted) || exhausted.resource != "netease" {
		t.Fatal(err)
	}
	var count int64
	if err := m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := m.wdb.Db.Create(&schema.NetEaseAccount{ID: "available", Username: "test", Password: "secret"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := m.reserveMiniProgramAgentTask("wx_resources", entry.ID, "hash"); err != nil {
		t.Fatal(err)
	}
}
