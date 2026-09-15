package manager

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
	"github.com/vertrai/hub/manager/schema"
)

func TestStripeSettingsPersistenceAndSecretProtection(t *testing.T) {
	m := newCommerceTestManager(t)
	body := `{"enabled":true,"secretKey":"sk_test_saved","webhookSecret":"whsec_saved","successURL":"https://vertr.ai/success","cancelURL":"https://vertr.ai/cancel","portalReturnURL":"https://vertr.ai/agents","stopAgentOnPaymentFailure":true}`
	if r := webRequest(m, "PUT", "/v1/admin/stripe/settings", body, ""); r.Code != 401 {
		t.Fatal("anonymous settings write", r.Code)
	}
	token := userToken(t, m, "ordinary")
	requestAuth := httptest.NewRequest("GET", "/v1/admin/stripe/settings", nil)
	requestAuth.AddCookie(adminCookie(token, false, 3600))
	responseAuth := httptest.NewRecorder()
	m.router().ServeHTTP(responseAuth, requestAuth)
	if responseAuth.Code != 401 {
		t.Fatal("ordinary user settings read", responseAuth.Code)
	}
	save := catalogRequest(m, "PUT", "/", body, "", m.saveStripeSettings, nil)
	if save.Code != 200 {
		t.Fatal(save.Code, save.Body.String())
	}
	if strings.Contains(save.Body.String(), "sk_test_saved") || strings.Contains(save.Body.String(), "whsec_saved") {
		t.Fatal("response disclosed secrets")
	}
	var row schema.StripeSettings
	if err := m.wdb.Db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.EncryptedConfig, []byte("sk_test_saved")) {
		t.Fatal("stored plaintext secret")
	}
	cfg, _, err := m.stripeRuntime()
	if err != nil || cfg.SecretKey != "sk_test_saved" {
		t.Fatal("saved settings not applied", err)
	}
	// A fresh Manager with the same persistent signing key reads the saved settings.
	restarted, err := New("test", Config{}, m.wdb)
	if err != nil {
		t.Fatal(err)
	}
	restarted.adminAuth = m.adminAuth
	cfg, _, err = restarted.stripeRuntime()
	if err != nil || cfg.WebhookSecret != "whsec_saved" {
		t.Fatal("settings lost across restart", err)
	}
	var request map[string]any
	json.Unmarshal([]byte(body), &request)
	request["secretKey"] = ""
	request["webhookSecret"] = ""
	request["enabled"] = false
	payload, _ := json.Marshal(request)
	save = catalogRequest(m, "PUT", "/", string(payload), "", m.saveStripeSettings, nil)
	if save.Code != 200 {
		t.Fatal(save.Body.String())
	}
	cfg, _, err = m.stripeRuntime()
	if err != nil || cfg.Enabled || cfg.SecretKey != "sk_test_saved" || cfg.WebhookSecret != "whsec_saved" {
		t.Fatal("blank secret did not preserve saved value", err)
	}
	if r := webRequest(m, "POST", "/v1/billing/checkout-sessions", `{"product":"x_agent"}`, token); r.Code != 503 {
		t.Fatal("disabled Stripe accepted checkout", r.Code)
	}

	raw := `{"object":"event","id":"evt_saved_secret","type":"irrelevant","api_version":"` + stripe.APIVersion + `","data":{"object":{}}}`
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: []byte(raw), Secret: "whsec_saved"})
	hook := httptest.NewRequest("POST", "/v1/stripe/webhook", strings.NewReader(raw))
	hook.Header.Set("Stripe-Signature", signed.Header)
	hookResponse := httptest.NewRecorder()
	m.router().ServeHTTP(hookResponse, hook)
	if hookResponse.Code != 200 {
		t.Fatal("disabled checkout stopped existing webhook or ignored saved secret", hookResponse.Code, hookResponse.Body.String())
	}
	request["enabled"] = true
	request["successURL"] = "javascript:alert(1)"
	payload, _ = json.Marshal(request)
	if r := catalogRequest(m, "PUT", "/", string(payload), "", m.saveStripeSettings, nil); r.Code != 400 {
		t.Fatal("invalid URL accepted", r.Code)
	}
	cfg, _, _ = m.stripeRuntime()
	if cfg.Enabled {
		t.Fatal("invalid save altered settings")
	}
}

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
