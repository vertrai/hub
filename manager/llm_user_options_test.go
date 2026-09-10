package manager

import (
	"encoding/json"
	"github.com/vertrai/hub/manager/schema"
	"strings"
	"testing"
)

func TestLLMUserOptionsWithoutResources(t *testing.T) {
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.User{}, &schema.AccessKey{}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []schema.User{{ID: "a", Name: "Alice", Status: "active"}, {ID: "b", Name: "Bob", Status: "active"}, {ID: "c", Name: "Disabled", Status: "disabled"}} {
		if err := m.wdb.Db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []schema.AccessKey{
		{ID: "local1", UserID: "a", ResourceKeyID: "remote1", Status: "available", Secret: "must-not-leak"},
		{ID: "local2", UserID: "a", ResourceKeyID: "remote2", Status: "assigned", Secret: "must-not-leak"},
		{ID: "local3", UserID: "a", ResourceKeyID: "remote3", Status: "revoked", Secret: "must-not-leak"},
	} {
		if err := m.wdb.Db.Create(&key).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Resources is deliberately unreachable: selecting a user must not depend on it.
	m.resources = NewResourcesClient(ResourcesConfig{BaseURL: "http://127.0.0.1:1"})
	r := llmTestRequest(m, "GET", "/v1/admin/llm/user-options", "", "", true)
	if r.Code != 200 {
		t.Fatalf("%d: %s", r.Code, r.Body.String())
	}
	var result struct {
		Items []struct {
			ID         string
			AccessKeys []struct{ ID string }
		}
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].ID != "a" || len(result.Items[0].AccessKeys) != 2 || len(result.Items[1].AccessKeys) != 0 {
		t.Fatal(r.Body.String())
	}
	for _, unwanted := range []string{"must-not-leak", "local1", "remote3", "Disabled"} {
		if strings.Contains(r.Body.String(), unwanted) {
			t.Fatalf("unexpected %s: %s", unwanted, r.Body.String())
		}
	}
}
