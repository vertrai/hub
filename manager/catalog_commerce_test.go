package manager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
)

func TestCatalogCommerceConfigurationAndOrderSnapshots(t *testing.T) {
	m := newCommerceTestManager(t)
	m.wdb.Db.AutoMigrate(&schema.MiniProgramAgentTask{})
	entry := schema.AgentCatalogEntry{ID: "website-assistant", Name: "网站助手", LogoURL: "https://example.com/icon.png", Intro: "说明", Capabilities: []string{"帮助"}, Module: "module-one", Published: true, ProductID: "new_product", StripePriceID: "price_one"}
	save := func(method string, e schema.AgentCatalogEntry) int {
		t.Helper()
		b, _ := json.Marshal(e)
		r := catalogRequest(m, method, "/", string(b), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: e.ID}})
		if r.Code >= 500 {
			t.Fatal(r.Body.String())
		}
		return r.Code
	}
	if status := save("POST", entry); status != 200 {
		t.Fatal(status)
	}
	token := userToken(t, m, "buyer")
	fake := &fakeStripe{}
	m.stripeAPI = fake
	if r := webRequest(m, "POST", "/v1/billing/checkout-sessions", `{"product":"new_product"}`, token); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	entry.StripePriceID = "price_two"
	entry.Module = "module-two"
	if status := save("PUT", entry); status != 200 {
		t.Fatal(status)
	}
	current, err := m.commerceProduct("new_product")
	if err != nil || current.StripePriceID != "price_two" || current.Module != "module-two" {
		t.Fatal("catalog change not used", err)
	}
	var bill schema.Billing
	if err := m.wdb.Db.First(&bill, "product = ?", "new_product").Error; err != nil {
		t.Fatal(err)
	}
	if bill.PriceID != "price_one" || bill.Module != "module-one" {
		t.Fatal("existing order snapshot changed")
	}
	duplicate := entry
	duplicate.ID = "duplicate"
	duplicate.Name = "重复"
	if status := save("POST", duplicate); status != 409 {
		t.Fatal("duplicate product accepted", status)
	}
	duplicate.ProductID = "other_product"
	if status := save("POST", duplicate); status != 200 {
		t.Fatal(status)
	}
	duplicate.ProductID = entry.ProductID
	if status := save("PUT", duplicate); status != 409 {
		t.Fatal("product identity changed", status)
	}
	entry.StripePriceID = ""
	if status := save("PUT", entry); status != 200 {
		t.Fatal(status)
	}
	r := webRequest(m, "GET", "/v1/products", "", token)
	if !strings.Contains(r.Body.String(), `"subscriptionAvailable":false`) {
		t.Fatal(r.Body.String())
	}
	if r := webRequest(m, "POST", "/v1/billing/checkout-sessions", `{"product":"new_product"}`, token); r.Code != 503 {
		t.Fatal("checkout accepted without price")
	}
	entry.Published = false
	if status := save("PUT", entry); status != 200 {
		t.Fatal(status)
	}
	if _, err := m.commerceProduct("new_product"); err == nil {
		t.Fatal("unpublished product available")
	}
	r = catalogRequest(m, "DELETE", "/", "", "", m.adminDeleteAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if r.Code != 409 {
		t.Fatal("deleted catalog with existing order", r.Code, r.Body.String())
	}
	public := publicCatalogEntry(entry)
	if _, ok := public["stripePriceId"]; ok {
		t.Fatal("payment config leaked into mini-program catalog")
	}
}
func TestCatalogCommerceValidation(t *testing.T) {
	valid := schema.AgentCatalogEntry{ID: "valid", Name: "助手", LogoURL: "https://example.com/icon.png", Intro: "说明", Capabilities: []string{"帮助"}, Module: "module"}
	for _, fields := range [][2]string{{"Bad Product", "price_one"}, {"", "price_one"}, {"product", "prod_wrong"}} {
		entry := valid
		entry.ProductID, entry.StripePriceID = fields[0], fields[1]
		if validateCatalogEntry(entry) == nil {
			t.Fatal("invalid commerce config accepted", fields)
		}
	}
	if err := validateCatalogEntry(valid); err != nil {
		t.Fatal("non-commerce catalog rejected", err)
	}
}

func TestCatalogCommerceRedemptionRechecksCatalog(t *testing.T) {
	m := newCommerceTestManager(t)
	entry, err := m.commerceProduct("x_agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Create(&schema.InviteCode{Code: "ABCDEF", Product: "x_agent"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Model(&entry).Update("module", "updated_module").Error; err != nil {
		t.Fatal(err)
	}
	entry.Module = "stale_module"
	agent, err := m.reserveInviteAgent("ABCDEF", "buyer", "x_agent", entry, false)
	if err != nil || agent.Module != "updated_module" {
		t.Fatalf("redemption used stale catalog: %+v, %v", agent, err)
	}
	if err := m.wdb.Db.Create(&schema.InviteCode{Code: "GHJKLM"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.wdb.Db.Model(&entry).Update("published", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := m.reserveInviteAgent("GHJKLM", "buyer", "x_agent", entry, false); err == nil {
		t.Fatal("redeemed unpublished product from stale catalog")
	}
	var invite schema.InviteCode
	if err := m.wdb.Db.First(&invite, "code = ?", "GHJKLM").Error; err != nil || invite.UsedAt != nil {
		t.Fatal("failed redemption consumed invitation", err)
	}
}
