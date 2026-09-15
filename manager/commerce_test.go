package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
	var m *Manager
	if dsn := os.Getenv("HUB_TEST_COMMERCE_POSTGRES_DSN"); dsn != "" {
		m = newCommercePostgresManager(t, dsn)
	} else {
		m = newLLMTestManager(t)
	}
	if err := m.wdb.Db.AutoMigrate(&schema.User{}, &schema.AccessKey{}, &schema.HymatrixPod{}, &schema.AgentCatalogEntry{}, &schema.InviteCode{}, &schema.WebAgent{}, &schema.Billing{}, &schema.StripeEvent{}); err != nil {
		t.Fatal(err)
	}
	m.config.Deployment = DeploymentConfig{RuntimeType: "hermes", NodeURL: "https://node.example", PrivateKey: "configured", GatewayURL: "https://hub.example", HermesGatewayToken: "configured"}
	m.config.Stripe = StripeConfig{Enabled: true, SecretKey: "sk_test", WebhookSecret: "whsec_test", SuccessURL: "https://vertr.ai/success", CancelURL: "https://vertr.ai/cancel", PortalReturnURL: "https://vertr.ai/agents", StopAgentOnPaymentFailure: true}
	if err := m.wdb.Db.Create(&schema.AgentCatalogEntry{ID: "x", Name: "X", Module: "module_x", Published: true, ProductID: "x_agent", StripePriceID: "price_x"}).Error; err != nil {
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

func TestCommerceCancelStopsUncertainKnownPod(t *testing.T) {
	m := newCommerceTestManager(t)
	stops := 0
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/vms/stop" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		stops++
		w.Write([]byte(`{}`))
	}))
	defer node.Close()
	pod := schema.HymatrixPod{ID: "uncertain-pod", PID: "confirmed-remote-pid", Status: "spawned", AdminURL: node.URL, UserID: "one"}
	m.wdb.Db.Create(&pod)
	a := schema.WebAgent{ID: "uncertain-agent", PodID: pod.ID, Source: "uncertain", State: "needs_review", Desired: "stopped", Phase: "starting", Error: "start response lost"}
	m.wdb.Db.Create(&a)
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stops != 1 {
		t.Fatal("canceled uncertain pod was not stopped")
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	if a.State != "needs_review" || a.Error == "" {
		t.Fatal("lost reconciliation requirement")
	}
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stops != 1 {
		t.Fatal("repeated stop unnecessarily")
	}
}
func TestCommerceWebsiteAgentContract(t *testing.T) {
	m := newCommerceTestManager(t)
	token := userToken(t, m, "one")
	a := schema.WebAgent{ID: "agent_contract", UserID: "google_one", Product: "x_agent", State: "queued", Source: "contract"}
	m.wdb.Db.Create(&a)
	end := time.Now().Add(60 * 24 * time.Hour).UTC().Truncate(time.Second)
	m.wdb.Db.Create(&schema.Billing{ID: "bill_contract", UserID: a.UserID, AgentID: a.ID, CurrentPeriodEnd: end})
	w := webRequest(m, "GET", "/v1/agents", "", token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var data struct {
		Items []struct {
			Status           string
			CurrentPeriodEnd time.Time
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Items) != 1 || data.Items[0].Status != "starting" || !data.Items[0].CurrentPeriodEnd.Equal(end) {
		t.Fatal(w.Body.String())
	}
}

func TestCommerceProvisionStopAndResumeKeepsPodAndKeys(t *testing.T) {
	m := newCommerceTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.LLMResourceSettings{}, &schema.LLMProvider{}, &schema.LLMRoute{}, &schema.LLMKey{}); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.Create(&schema.LLMResourceSettings{ID: "default", BaseURL: "https://hub.example/llm/v1", AllowedModels: `["model"]`, DefaultModel: "model"})
	m.wdb.Db.Create(&schema.LLMProvider{ID: "provider", Enabled: true, Kind: "openai", Models: `["model"]`, Credential: []byte(`{"apiKey":"test"}`)})
	m.wdb.Db.Create(&schema.LLMRoute{ID: "model", ProviderID: "provider", UpstreamModel: "model"})
	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/telegram-bot":
			w.Write([]byte(`{"botToken":"bot-secret","username":"example_bot"}`))
		case "/v1/access-key":
			w.Write([]byte(`{"accessKey":{"id":"resource-key","ownerUserId":"google_one","status":"active"}}`))
		default:
			t.Errorf("unexpected resource call %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer resources.Close()
	m.resources = NewResourcesClient(ResourcesConfig{BaseURL: resources.URL})
	actions := []string{}
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions = append(actions, r.URL.Path)
		w.Write([]byte(`{}`))
	}))
	defer node.Close()
	sdk := &recordingPodSDK{}
	m.commerceHymatrix = func(cfg HymatrixConfig) (*HymatrixClient, error) { return &HymatrixClient{config: cfg, sdk: sdk}, nil }
	a := schema.WebAgent{ID: "lifecycle", UserID: "google_one", Source: "lifecycle", State: "queued", Desired: "running", AccessKeyID: "key", PodID: "pod", Module: "module"}
	pod := schema.HymatrixPod{ID: "pod", UserID: a.UserID, PID: "pending_" + a.ID, Status: schema.PodStatusSpawned, AccessKeyID: "key", AdminURL: node.URL, Module: "module"}
	key := schema.AccessKey{ID: "key", UserID: a.UserID, ResourceKeyID: "resource-key", Secret: "gateway-secret", Status: "assigned", AssignedPodID: &pod.ID}
	for _, record := range []any{&a, &pod, &key} {
		if err := m.wdb.Db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	m.wdb.Db.First(&pod, "id = ?", pod.ID)
	if a.State != "running" || pod.PID != "pid-new" || sdk.startTarget != "pid-new" {
		t.Fatalf("not started: state=%s phase=%s err=%s pid=%s calls=%v", a.State, a.Phase, a.Error, pod.PID, sdk.calls)
	}
	m.wdb.Db.Model(&a).Update("desired", "stopped")
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.Model(&a).Update("desired", "running")
	if err := m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	m.wdb.Db.First(&key, "id = ?", key.ID)
	if a.State != "running" || a.PodID != "pod" || key.AssignedPodID == nil || *key.AssignedPodID != "pod" || len(sdk.calls) != 2 {
		t.Fatal("resume replaced allocation", a.State, sdk.calls)
	}
	if strings.Join(actions, ",") != "/admin/vms/stop,/admin/vms/resume" {
		t.Fatal(actions)
	}
}

func TestCommerceAdminCreatesWebsiteCompatibleCodesAndRevokes(t *testing.T) {
	m := newCommerceTestManager(t)
	request := httptest.NewRequest("POST", "/v1/admin/invite-codes", strings.NewReader(`{"count":10,"product":"x_agent","note":"campaign"}`))
	request.Header.Set("Content-Type", "application/json")
	authenticateAdmin(m, request)
	w := httptest.NewRecorder()
	m.router().ServeHTTP(w, request)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var data struct{ Codes []schema.InviteCode }
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Codes) != 10 {
		t.Fatal(len(data.Codes))
	}
	seen := map[string]bool{}
	for _, code := range data.Codes {
		if len(code.Code) != 6 || seen[code.Code] {
			t.Fatal("incompatible or duplicate code", code.Code)
		}
		seen[code.Code] = true
	}
	request = httptest.NewRequest("DELETE", "/v1/admin/invite-codes/"+data.Codes[0].Code, nil)
	authenticateAdmin(m, request)
	w = httptest.NewRecorder()
	m.router().ServeHTTP(w, request)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	token := userToken(t, m, "redeemer")
	if w := webRequest(m, "POST", "/v1/agents/x", `{"inviteCode":"`+data.Codes[0].Code+`"}`, token); w.Code != 403 {
		t.Fatal("revoked code accepted", w.Code)
	}
}
