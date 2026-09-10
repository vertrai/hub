package manager

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/vertrai/hub/manager/schema"
)

func TestXboxChildAllocation(t *testing.T) {
	m, _ := setupLLMResources(t)
	if err := m.wdb.Db.AutoMigrate(&schema.XboxChild{}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, key string, admin bool) int {
		return llmTestRequest(m, method, path, body, key, admin).Code
	}
	const path = "/v1/admin/xbox/children"
	const body = `{"email":"child@example.com","password":" secret ","parentEmail":"parent@example.com"}`
	if code := request("POST", path, body, "", false); code != 401 {
		t.Fatalf("admin auth: %d", code)
	}
	if code := request("POST", path, `{"email":"invalid","password":"s","parentEmail":"p@example.com"}`, "", true); code != 400 {
		t.Fatalf("validation: %d", code)
	}
	if code := request("POST", path, body, "", true); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := request("POST", path, body, "", true); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}
	if code := request("GET", "/v1/xbox-child", "", "invalid", false); code != 401 {
		t.Fatalf("key auth: %d", code)
	}
	var wg sync.WaitGroup
	results := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := llmTestRequest(m, "POST", "/v1/xbox-child", "", "hub-a", false)
			results <- fmt.Sprintf("%d %s", r.Code, r.Body.String())
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		if !strings.HasPrefix(r, "200 ") || !strings.Contains(r, `"password":" secret "`) {
			t.Fatalf("repeat allocation: %s", r)
		}
	}
	if code := request("GET", "/v1/xbox-child", "", "hub-b", false); code != 409 {
		t.Fatalf("exclusive: %d", code)
	}
	var stored schema.XboxChild
	if err := m.wdb.Db.First(&stored).Error; err != nil || stored.UsedAt == nil || stored.HubAccessKeyID == nil || *stored.HubAccessKeyID != "hub-a" {
		t.Fatal("allocation not persisted")
	}
	r := llmTestRequest(m, "GET", path, "", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "secret") || !strings.Contains(r.Body.String(), "parent@example.com") {
		t.Fatal("admin list contract")
	}
	if code := request("POST", path, strings.Replace(body, "child@example.com", "second@example.com", 1), "", true); code != 201 {
		t.Fatal(code)
	}
	r = llmTestRequest(m, "GET", "/v1/xbox-child", "", "hub-b", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "second@example.com") || strings.Contains(r.Body.String(), "parentEmail") || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("second allocation contract")
	}
}

func TestXboxChildConcurrentOwners(t *testing.T) {
	m, _ := setupLLMResources(t)
	if err := m.wdb.Db.AutoMigrate(&schema.XboxChild{}); err != nil {
		t.Fatal(err)
	}
	item := schema.XboxChild{ID: "only", Email: "only@example.com", Password: "secret", ParentEmail: "parent@example.com"}
	if err := m.wdb.Db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	codes := make(chan int, 2)
	for _, key := range []string{"hub-a", "hub-b"} {
		go func(key string) { <-start; codes <- llmTestRequest(m, "POST", "/v1/xbox-child", "", key, false).Code }(key)
	}
	close(start)
	a, b := <-codes, <-codes
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("competing owners: %d, %d", a, b)
	}
}

func TestXboxChildAdminUpdateAndDelete(t *testing.T) {
	m, _ := setupLLMResources(t)
	if err := m.wdb.Db.AutoMigrate(&schema.XboxChild{}); err != nil {
		t.Fatal(err)
	}
	owner := "hub-a"
	item := schema.XboxChild{ID: "editable", Email: "edit@example.com", Password: "old", ParentEmail: "parent@example.com", HubAccessKeyID: &owner}
	if err := m.wdb.Db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/xbox/children/editable"
	for _, method := range []string{"PATCH", "DELETE"} {
		if r := llmTestRequest(m, method, path, `{"password":"new"}`, "hub-a", false); r.Code != 401 {
			t.Fatalf("auth %s: %d", method, r.Code)
		}
	}
	if r := llmTestRequest(m, "PATCH", path, `{"password":"  "}`, "", true); r.Code != 400 {
		t.Fatal("empty password accepted")
	}
	if r := llmTestRequest(m, "PATCH", path, `{"password":" new secret "}`, "", true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r := llmTestRequest(m, "GET", "/v1/xbox-child", "", "hub-a", false)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"password":" new secret "`) {
		t.Fatal("updated password unavailable to owner")
	}
	if r := llmTestRequest(m, "GET", "/v1/xbox-child", "", "hub-b", false); r.Code != 409 {
		t.Fatal("update released ownership")
	}
	if r := llmTestRequest(m, "DELETE", path, "", "", true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = llmTestRequest(m, "GET", "/v1/admin/xbox/children", "", "", true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "edit@example.com") {
		t.Fatal("deleted account remains")
	}
	if r := llmTestRequest(m, "GET", "/v1/xbox-child", "", "hub-a", false); r.Code != 409 {
		t.Fatal("deleted account returned")
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		if r := llmTestRequest(m, method, path, `{"password":"new"}`, "", true); r.Code != 404 {
			t.Fatalf("missing %s: %d", method, r.Code)
		}
	}
}
