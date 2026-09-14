package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
	"github.com/vertrai/hub/manager/schema"
)

func newCommerceTestManager(t *testing.T) *Manager {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.User{}, &schema.AccessKey{}, &schema.HymatrixPod{}, &schema.AgentCatalogEntry{}, &schema.InviteCode{}, &schema.WebAgent{}, &schema.Billing{}, &schema.StripeEvent{}); err != nil {
		t.Fatal(err)
	}
	m.config.Commerce = CommerceConfig{Products: map[string]CommerceProduct{"x_agent": {CatalogID: "x", PriceID: "price_x"}}, NodeURL: "https://node.example", PrivateKey: "configured", GatewayURL: "https://hub.example", HermesGatewayToken: "configured"}
	m.config.Stripe = StripeConfig{Enabled: true, SecretKey: "sk_test", WebhookSecret: "whsec_test", SuccessURL: "https://vertr.ai/success", CancelURL: "https://vertr.ai/cancel", PortalReturnURL: "https://vertr.ai/agents", StopAgentOnPaymentFailure: true}
	if err := m.wdb.Db.Create(&schema.AgentCatalogEntry{ID: "x", Name: "X", Module: "module_x", Published: true}).Error; err != nil {
		t.Fatal(err)
	}
	return m
}
func webRequest(m *Manager, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	m.router().ServeHTTP(w, r)
	return w
}
func userToken(t *testing.T, m *Manager, sub string) string {
	t.Helper()
	m.adminAuth.validator = googleTokenValidatorStub{adminIdentity{Subject: sub, Email: sub + "@example.com", Name: sub}}
	w := webRequest(m, "POST", "/v1/auth/google", `{"id_token":"valid"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct{ AccessToken string }
	json.Unmarshal(w.Body.Bytes(), &out)
	return out.AccessToken
}
func TestCommerceGoogleIdentityAndAdminBoundary(t *testing.T) {
	m := newCommerceTestManager(t)
	token := userToken(t, m, "ordinary")
	if w := webRequest(m, "GET", "/v1/me", "", token); w.Code != 200 || !strings.Contains(w.Body.String(), "google_ordinary") {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/v1/admin/invite-codes", nil)
	r.AddCookie(adminCookie(token, false, 3600))
	w := httptest.NewRecorder()
	m.router().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("ordinary login granted admin: %d", w.Code)
	}
	m.adminAuth.allowed["ordinary@example.com"] = struct{}{}
	w = httptest.NewRecorder()
	m.router().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	delete(m.adminAuth.allowed, "ordinary@example.com")
	w = httptest.NewRecorder()
	m.router().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("allowlist removal did not revoke administrator")
	}
	userToken(t, m, "ordinary")
	var n int64
	m.wdb.Db.Model(&schema.User{}).Count(&n)
	if n != 1 {
		t.Fatalf("repeat login created %d users", n)
	}
	m.wdb.Db.Model(&schema.User{}).Where("id = ?", "google_ordinary").Update("status", "disabled")
	if w := webRequest(m, "GET", "/v1/me", "", token); w.Code != 401 {
		t.Fatal("disabled user accepted")
	}
}
func TestCommerceInviteAtomicRedemptionAndOwnership(t *testing.T) {
	m := newCommerceTestManager(t)
	one := userToken(t, m, "one")
	two := userToken(t, m, "two")
	code := schema.InviteCode{Code: "TEST-CODE"}
	m.wdb.Db.Create(&code)
	body := `{"inviteCode":"test-code"}`
	first := webRequest(m, "POST", "/v1/agents/x", body, one)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	second := webRequest(m, "POST", "/v1/agents/x", body, one)
	if second.Code != 200 || second.Body.String() != first.Body.String() {
		t.Fatal("redemption retry changed instance")
	}
	if w := webRequest(m, "POST", "/v1/agents/x", body, two); w.Code != 403 {
		t.Fatal("another user redeemed used code", w.Code)
	}
	var n int64
	m.wdb.Db.Model(&schema.WebAgent{}).Count(&n)
	if n != 1 {
		t.Fatal(n)
	}
	if w := webRequest(m, "GET", "/v1/agents", "", two); strings.Contains(w.Body.String(), "agent_") {
		t.Fatal("agent leaked to other user")
	}
	expired := time.Now().Add(-time.Hour)
	m.wdb.Db.Create(&schema.InviteCode{Code: "EXPIRED", ExpiresAt: &expired})
	if w := webRequest(m, "POST", "/v1/agents/x", `{"inviteCode":"EXPIRED"}`, one); w.Code != 403 {
		t.Fatal(w.Code)
	}
	m.wdb.Db.Create(&schema.InviteCode{Code: "WRONG", Product: "other"})
	if w := webRequest(m, "POST", "/v1/agents/x", `{"inviteCode":"WRONG"}`, one); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

type fakeStripe struct {
	calls        int
	fail         bool
	keys         []string
	subscription *stripe.Subscription
}

func (f *fakeStripe) Checkout(_ context.Context, p *stripe.CheckoutSessionCreateParams) (*stripe.CheckoutSession, error) {
	f.calls++
	f.keys = append(f.keys, stripe.StringValue(p.IdempotencyKey))
	if f.fail {
		return nil, errors.New("lost response")
	}
	return &stripe.CheckoutSession{ID: "cs_test", URL: "https://checkout.stripe.com/test", Customer: &stripe.Customer{ID: "cus_test"}}, nil
}
func (f *fakeStripe) Portal(context.Context, string, string) (string, error) {
	return "https://billing.stripe.com/test", nil
}
func (f *fakeStripe) Subscription(context.Context, string) (*stripe.Subscription, error) {
	if f.fail {
		return nil, errors.New("unavailable")
	}
	return f.subscription, nil
}
func TestCommerceCheckoutRetryAndOwnership(t *testing.T) {
	m := newCommerceTestManager(t)
	token := userToken(t, m, "one")
	other := userToken(t, m, "two")
	f := &fakeStripe{fail: true}
	m.stripeAPI = f
	body := `{"product":"x_agent","quantity":1}`
	if w := webRequest(m, "POST", "/v1/billing/checkout-sessions", body, token); w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	f.fail = false
	if w := webRequest(m, "POST", "/v1/billing/checkout-sessions", body, token); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(f.keys) != 2 || f.keys[0] != f.keys[1] {
		t.Fatal("retry lost Stripe idempotency key", f.keys)
	}
	webRequest(m, "POST", "/v1/billing/checkout-sessions", body, token)
	if f.calls != 2 {
		t.Fatal("duplicate checkout request reached Stripe")
	}
	if w := webRequest(m, "GET", "/v1/billing/checkout-sessions/cs_test", "", other); w.Code != 404 {
		t.Fatal("another user read checkout")
	}
	if w := webRequest(m, "POST", "/v1/billing/portal-sessions", `{}`, other); w.Code != 404 {
		t.Fatal("another user opened portal")
	}
	if w := webRequest(m, "POST", "/v1/billing/checkout-sessions", `{"product":"x_agent","quantity":2}`, token); w.Code != 400 {
		t.Fatal("quantity accepted")
	}
}
func billingFixture(t *testing.T, m *Manager) (*fakeStripe, schema.Billing) {
	t.Helper()
	b := schema.Billing{ID: "bill_test", UserID: "google_one", Product: "x_agent", CatalogID: "x", Module: "module_x", PriceID: "price_x", Status: "checkout_pending"}
	if err := m.wdb.Db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	s := &stripe.Subscription{ID: "sub_test", Status: stripe.SubscriptionStatusActive, Metadata: map[string]string{"billing_id": b.ID, "user_id": b.UserID, "product": b.Product}, Customer: &stripe.Customer{ID: "cus_test"}, LatestInvoice: &stripe.Invoice{ID: "in_test", Status: stripe.InvoiceStatusPaid}, Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{Price: &stripe.Price{ID: "price_x"}, Quantity: 1, CurrentPeriodStart: 100, CurrentPeriodEnd: 200}}}}
	f := &fakeStripe{subscription: s}
	m.stripeAPI = f
	return f, b
}
func subscriptionEvent(id string, s *stripe.Subscription) stripe.Event {
	raw, _ := json.Marshal(s)
	return stripe.Event{ID: id, Type: stripe.EventTypeCustomerSubscriptionUpdated, Created: time.Now().Unix(), Data: &stripe.EventData{Raw: raw}}
}
func TestCommerceStripeReplayCancellationRecoveryAndRollback(t *testing.T) {
	m := newCommerceTestManager(t)
	f, b := billingFixture(t, m)
	ctx := context.Background()
	event := subscriptionEvent("evt_first", f.subscription)
	if err := m.processStripeEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := m.processStripeEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	var count int64
	m.wdb.Db.Model(&schema.WebAgent{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate agent", count)
	}
	m.wdb.Db.First(&b, "id = ?", b.ID)
	if b.AgentID == "" {
		t.Fatal("missing paid agent")
	}
	f.subscription.Status = stripe.SubscriptionStatusCanceled
	if err := m.processStripeEvent(ctx, subscriptionEvent("evt_cancel", f.subscription)); err != nil {
		t.Fatal(err)
	}
	var a schema.WebAgent
	m.wdb.Db.First(&a, "id = ?", b.AgentID)
	if a.Desired != "stopped" {
		t.Fatal(a.Desired)
	}
	// A late paid-shaped event still observes the current canceled subscription.
	if err := m.processStripeEvent(ctx, subscriptionEvent("evt_late", f.subscription)); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	if a.Desired != "stopped" {
		t.Fatal("stale event resumed canceled subscription")
	}
	f.subscription.Status = stripe.SubscriptionStatusActive
	f.fail = true
	recovery := subscriptionEvent("evt_recover", f.subscription)
	if err := m.processStripeEvent(ctx, recovery); err == nil {
		t.Fatal("expected transient failure")
	}
	m.wdb.Db.Model(&schema.StripeEvent{}).Where("id = ?", recovery.ID).Count(&count)
	if count != 0 {
		t.Fatal("failed event marked processed")
	}
	f.fail = false
	if err := m.processStripeEvent(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	if a.Desired != "running" {
		t.Fatal("payment did not resume")
	}
	f.subscription.Items.Data[0].Price.ID = "wrong"
	if err := m.processStripeEvent(ctx, subscriptionEvent("evt_bad_price", f.subscription)); err == nil {
		t.Fatal("wrong price accepted")
	}
}
func TestCommerceWebhookSignatureAndNoExternalEffectsOnForgery(t *testing.T) {
	m := newCommerceTestManager(t)
	raw := `{"object":"event","id":"evt_unknown","type":"irrelevant","api_version":"` + stripe.APIVersion + `","data":{"object":{}}}`
	if w := webRequest(m, "POST", "/v1/stripe/webhook", raw, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: []byte(raw), Secret: m.config.Stripe.WebhookSecret})
	r := httptest.NewRequest("POST", "/v1/stripe/webhook", strings.NewReader(raw))
	r.Header.Set("Stripe-Signature", signed.Header)
	w := httptest.NewRecorder()
	m.router().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestCommerceWorkerStopsUnstartedAndFlagsInterrupted(t *testing.T) {
	m := newCommerceTestManager(t)
	a := schema.WebAgent{ID: "stop", Source: "stop", State: "queued", Desired: "stopped"}
	m.wdb.Db.Create(&a)
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	if a.State != "stopped" {
		t.Fatal(a.State)
	}
	past := time.Now().Add(-time.Minute)
	stuck := schema.WebAgent{ID: "stuck", Source: "stuck", State: "processing", Phase: "spawning", Desired: "running", LeaseUntil: &past}
	m.wdb.Db.Create(&stuck)
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.First(&stuck, "id = ?", stuck.ID)
	if stuck.State != "needs_review" {
		t.Fatal("interrupted spawn retried", stuck.State)
	}
}
