package manager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
)

func webCatalogFixture(t *testing.T) (*Manager, schema.AgentCatalogEntry) {
	t.Helper()
	m := newCommerceTestManager(t)
	entry := schema.AgentCatalogEntry{ID: "arbitrary-new-agent", Name: "共用名称", Intro: "共用介绍", Summary: "共用详情", LogoURL: "https://example.com/icon.png", Capabilities: []string{"共用能力"}, Module: "private-module", ProductID: "arbitrary_product", StripePriceID: "price_example", Published: false,
		Web: &schema.WebCatalogConfig{Published: true, InviteEnabled: true, Content: map[string]schema.WebCatalogCopy{"en": {Name: "New agent", Intro: "A new task", Capabilities: []string{"One", "Two"}}, "zh": {Name: "网页名称"}}},
	}
	if err := m.wdb.Db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	return m, entry
}
func TestWebCatalogLeavesWeChatContractUnchanged(t *testing.T) {
	m, entry := webCatalogFixture(t)
	// Include a listed WeChat-only entry and an unlisted web-only entry.
	before := webRequest(m, "GET", "/v1/wechat/catalog", "", "")
	detailBefore := webRequest(m, "GET", "/v1/wechat/catalog/"+entry.ID, "", "")
	r := webRequest(m, "GET", "/v1/catalog?channel=web&locale=en", "", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"name":"New agent"`) || strings.Contains(r.Body.String(), `"id":"x"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, private := range []string{"private-module", "stripePriceId", "price_example", "loginCopy", "creationDetail"} {
		if strings.Contains(r.Body.String(), private) {
			t.Fatal("private or WeChat-specific field leaked", private)
		}
	}
	entry.Web.Content["en"] = schema.WebCatalogCopy{Name: "Edited website"}
	entry.Web.Published = false
	body, _ := json.Marshal(entry)
	saved := catalogRequest(m, "PUT", "/", string(body), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	after := webRequest(m, "GET", "/v1/wechat/catalog", "", "")
	detailAfter := webRequest(m, "GET", "/v1/wechat/catalog/"+entry.ID, "", "")
	if before.Body.String() != after.Body.String() || detailBefore.Body.String() != detailAfter.Body.String() {
		t.Fatal("website edit changed WeChat response")
	}
	if r := webRequest(m, "GET", "/v1/catalog/"+entry.ID, "", ""); r.Code != 404 {
		t.Fatal("unlisted web entry remains public")
	}
	for _, q := range []string{"?channel=wechat", "?channel=unknown", "?locale=invalid"} {
		if r := webRequest(m, "GET", "/v1/catalog"+q, "", ""); r.Code != 400 {
			t.Fatal(q, r.Code)
		}
	}
}
func TestWebCatalogGenericCreationAndOwnedUnlistedAgent(t *testing.T) {
	m, entry := webCatalogFixture(t)
	token := userToken(t, m, "web-buyer")
	if err := m.wdb.Db.Create(&schema.InviteCode{Code: "WEBNEW", Product: entry.ProductID}).Error; err != nil {
		t.Fatal(err)
	}
	r := webRequest(m, "POST", "/v1/invite-codes/redeem", `{"product":"arbitrary_product","inviteCode":"WEBNEW","consentAccepted":true}`, token)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	// Same route works for a newly added ID; instance keeps its independent identity.
	var result struct{ Agent struct{ AgentID string } }
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil || result.Agent.AgentID == "" {
		t.Fatal(r.Body.String())
	}
	m.wdb.Db.Model(&schema.WebAgent{}).Where("id = ?", result.Agent.AgentID).Updates(map[string]any{"state": "running", "bot_username": "test_bot"})
	entry.Web.Published = false
	if err := m.wdb.Db.Save(&entry).Error; err != nil {
		t.Fatal(err)
	}
	r = webRequest(m, "GET", "/v1/agents?locale=zh", "", token)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"catalogId":"arbitrary-new-agent"`) || !strings.Contains(r.Body.String(), `"name":"网页名称"`) || !strings.Contains(r.Body.String(), `"url":"https://t.me/test_bot"`) {
		t.Fatal(r.Body.String())
	}
	m.wdb.Db.Model(&schema.WebAgent{}).Where("id = ?", result.Agent.AgentID).Update("state", "stopped")
	r = webRequest(m, "GET", "/v1/agents", "", token)
	if !strings.Contains(r.Body.String(), `"connection":{"type":"telegram","url":""}`) {
		t.Fatal("stopped agent has active connection", r.Body.String())
	}
	if _, err := m.commerceProduct(entry.ProductID); err == nil {
		t.Fatal("unlisted web product remains purchasable")
	}
	if r := webRequest(m, "POST", "/v1/billing/checkout-sessions", `{"product":"arbitrary_product"}`, token); r.Code != 503 {
		t.Fatal(r.Code, r.Body.String())
	}
}
func TestWebCatalogConsentAndPublicationAtRedemption(t *testing.T) {
	m, entry := webCatalogFixture(t)
	entry.Web.Content["en"] = schema.WebCatalogCopy{ConsentText: "Read before use"}
	m.wdb.Db.Save(&entry)
	token := userToken(t, m, "consent-buyer")
	m.wdb.Db.Create(&schema.InviteCode{Code: "CONSNT", Product: entry.ProductID})
	for _, path := range []string{"/v1/invite-codes/redeem", "/v1/billing/checkout-sessions"} {
		r := webRequest(m, "POST", path, `{"product":"arbitrary_product","inviteCode":"CONSNT"}`, token)
		if r.Code != 400 {
			t.Fatal("missing consent accepted", r.Code, r.Body.String())
		}
	}
	if r := webRequest(m, "POST", "/v1/invite-codes/redeem", `{"product":"arbitrary_product","inviteCode":"CONSNT","consentAccepted":true}`, token); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	stale := entry
	staleWeb := *entry.Web
	stale.Web = &staleWeb
	entry.Web.InviteEnabled = false
	m.wdb.Db.Save(&entry)
	m.wdb.Db.Create(&schema.InviteCode{Code: "SECOND", Product: entry.ProductID})
	if _, err := m.reserveInviteAgent("SECOND", "another", entry.ProductID, stale, true); err == nil {
		t.Fatal("stale entry bypasses disabled invitations")
	}
	var invite schema.InviteCode
	m.wdb.Db.First(&invite, "code = ?", "SECOND")
	if invite.UsedAt != nil {
		t.Fatal("failed request consumed invitation")
	}
}
func TestWebCatalogOldAdminAndLanguageFallback(t *testing.T) {
	m, entry := webCatalogFixture(t)
	oldClient := entry
	oldClient.Web = nil
	body, _ := json.Marshal(oldClient)
	r := catalogRequest(m, "PUT", "/", string(body), "", m.adminSaveAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var stored schema.AgentCatalogEntry
	m.wdb.Db.First(&stored, "id = ?", entry.ID)
	if stored.Web == nil || !stored.Web.Published {
		t.Fatal("old admin cleared web settings")
	}
	view := m.webCatalogEntry(stored, "zh")
	if view["name"] != "网页名称" || view["intro"] != "A new task" {
		t.Fatal(view)
	}
	if _, ok := publicCatalogEntry(stored)["web"]; ok {
		t.Fatal("web configuration leaked to WeChat")
	}
}

func TestWebCatalogCheckoutAndAvailability(t *testing.T) {
	m, entry := webCatalogFixture(t)
	token := userToken(t, m, "checkout-buyer")
	m.stripeAPI = &fakeStripe{}
	// A web-only listing must be purchasable without changing WeChat publication.
	r := webRequest(m, "POST", "/v1/billing/checkout-sessions", `{"product":"arbitrary_product","quantity":1,"consentAccepted":true}`, token)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"url":`) {
		t.Fatal(r.Code, r.Body.String())
	}
	r = webRequest(m, "GET", "/v1/products", "", token)
	if !strings.Contains(r.Body.String(), `"product":"arbitrary_product"`) {
		t.Fatal("web-only product absent", r.Body.String())
	}
	r = catalogRequest(m, "DELETE", "/", "", "", m.adminDeleteAgentCatalog, gin.Params{{Key: "id", Value: entry.ID}})
	if r.Code != 409 {
		t.Fatal("deleted web-published entry", r.Code)
	}
	m.config.Deployment.NodeURL = ""
	r = webRequest(m, "GET", "/v1/catalog", "", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"invite":{"enabled":false}`) || !strings.Contains(r.Body.String(), `"subscription":{"enabled":false}`) {
		t.Fatal("catalog should remain readable while creation is unavailable", r.Code, r.Body.String())
	}
	entry.ProductID = ""
	if validateWebCatalog(entry) == nil {
		t.Fatal("published web entry without a product accepted")
	}
}

func TestFreeCreationModesAndRetry(t *testing.T) {
	m, entry := webCatalogFixture(t)
	token := userToken(t, m, "free-buyer")
	no := false
	entry.Web.SubscriptionEnabled = &no
	entry.Web.InviteEnabled = false
	if err := m.wdb.Db.Save(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if r := webRequest(m, "POST", "/v1/agents/free", `{"product":"arbitrary_product"}`, token); r.Code != 400 {
		t.Fatal("missing consent allowed")
	}
	body := `{"product":"arbitrary_product","consentAccepted":true}`
	r := webRequest(m, "POST", "/v1/agents/free", body, token)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	again := webRequest(m, "POST", "/v1/agents/free", body, token)
	if again.Code != 200 || again.Body.String() != r.Body.String() {
		t.Fatal("free retry not idempotent", again.Body.String())
	}
	for _, mode := range []struct{ invite, subscription bool }{{true, false}, {false, true}, {true, true}} {
		entry.Web.InviteEnabled = mode.invite
		entry.Web.SubscriptionEnabled = &mode.subscription
		if err := m.wdb.Db.Save(&entry).Error; err != nil {
			t.Fatal(err)
		}
		if r := webRequest(m, "POST", "/v1/agents/free", body, token); r.Code != 403 {
			t.Fatal("paid/invite mode allowed free", r.Code)
		}
	}
}
