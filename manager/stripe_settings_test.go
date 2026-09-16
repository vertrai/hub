package manager

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"strings"
	"testing"
	"time"
)

func TestInviteAdminIncludesRedemptionUserAndAgent(t *testing.T) {
	m := newCommerceTestManager(t)
	token := userToken(t, m, "redeemer")
	if err := m.wdb.Db.Create(&schema.InviteCode{Code: "ABCDEF"}).Error; err != nil {
		t.Fatal(err)
	}
	r := webRequest(m, "POST", "/v1/invite-codes/redeem", `{"inviteCode":"ABCDEF","product":"x_agent"}`, token)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	result := catalogRequest(m, "GET", "/", "", "", m.listInviteCodes, nil)
	var out struct {
		Codes []struct {
			Code, Product, AgentID string
			UsedAt                 *time.Time
			UsedByUser             *schema.User
		}
		Total, Used int64
	}
	if err := json.Unmarshal(result.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Codes) != 1 || out.Used != 1 || out.Total != 1 {
		t.Fatal(result.Body.String())
	}
	code := out.Codes[0]
	if code.UsedByUser == nil || code.UsedByUser.Email != "redeemer@example.com" || code.UsedByUser.Name != "redeemer" || code.AgentID == "" || code.UsedAt == nil || code.Product != "x_agent" {
		t.Fatal(result.Body.String())
	}
	agents := catalogRequest(m, "GET", "/", "", "", m.adminWebAgents, nil)
	if !strings.Contains(agents.Body.String(), "redeemer@example.com") {
		t.Fatal(agents.Body.String())
	}
}

func TestStripeConfigAndProductPrices(t *testing.T) {
	m := newCommerceTestManager(t)
	if err := m.wdb.Db.Create(&schema.StripeSettings{ID: "stripe", EncryptedConfig: []byte("obsolete settings")}).Error; err != nil {
		t.Fatal(err)
	}
	cfg, _, err := m.stripeRuntime()
	if err != nil || cfg != m.config.Stripe {
		t.Fatal("database overrode config", err)
	}
	save := func(id, body string) int {
		return catalogRequest(m, "PATCH", "/", body, "", m.saveStripeProduct, gin.Params{{Key: "id", Value: id}}).Code
	}
	if status := save("x", `{"productId":"x_agent","stripePriceId":"price_new"}`); status != 200 {
		t.Fatal(status)
	}
	var entry schema.AgentCatalogEntry
	if err := m.wdb.Db.First(&entry, "id = ?", "x").Error; err != nil {
		t.Fatal(err)
	}
	if entry.StripePriceID != "price_new" || entry.Name != "X" || entry.Module != "module_x" || !entry.Published {
		t.Fatal("price update altered other fields", entry)
	}
	if save("x", `{"productId":"changed","stripePriceId":"price_new"}`) != 409 {
		t.Fatal("changed stable product")
	}
	if save("x", `{"productId":"x_agent","stripePriceId":"invalid"}`) != 400 {
		t.Fatal("accepted invalid price")
	}
	if save("x", `{"productId":"x_agent","stripePriceId":""}`) != 200 {
		t.Fatal("cannot clear price")
	}
	if err := m.wdb.Db.Create(&schema.AgentCatalogEntry{ID: "new", Name: "New"}).Error; err != nil {
		t.Fatal(err)
	}
	if save("new", `{"productId":"x_agent","stripePriceId":"price_one"}`) != 409 {
		t.Fatal("duplicate product allowed")
	}
	if save("new", `{"productId":"new_agent","stripePriceId":"price_one"}`) != 200 {
		t.Fatal("cannot configure new agent")
	}
	if r := webRequest(m, "PATCH", "/v1/admin/stripe/products/x", `{}`, ""); r.Code != 401 {
		t.Fatal("unprotected price update")
	}
}
