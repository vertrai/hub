package manager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vertrai/hub/manager/llm"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

type oauthFixture struct {
	issued          atomic.Int32
	polls           atomic.Int32
	exchanges       atomic.Int32
	pending         atomic.Bool
	accountOverride atomic.Value
}

func testOAuthJWT(account string) string {
	raw, _ := json.Marshal(map[string]any{"email": account + "@example.com", "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": account}, "exp": time.Now().Add(time.Hour).Unix()})
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}
func setupOAuthFixture(t *testing.T, m *Manager) *oauthFixture {
	t.Helper()
	f := &oauthFixture{}
	f.accountOverride.Store("")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			n := f.issued.Add(1)
			fmt.Fprintf(w, `{"device_auth_id":"device-%d","user_code":"CODE-%d","expires_in":900,"interval":"5"}`, n, n)
		case "/api/accounts/deviceauth/token":
			f.polls.Add(1)
			if f.pending.Load() {
				w.WriteHeader(403)
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["user_code"] == "" || req["device_auth_id"] == "" {
				t.Error("missing device parameters")
			}
			fmt.Fprintf(w, `{"authorization_code":%q,"code_verifier":"verifier"}`, req["device_auth_id"])
		case "/oauth/token":
			f.exchanges.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != "https://auth.openai.com/deviceauth/callback" {
				t.Errorf("invalid grant exchange: %v", r.Form)
			}
			account := r.Form.Get("code")
			if override := f.accountOverride.Load().(string); override != "" {
				account = override
			}
			fmt.Fprintf(w, `{"access_token":"access-%s","refresh_token":"refresh-%s","id_token":%q,"expires_in":3600}`, account, account, testOAuthJWT(account))
		default:
			t.Errorf("unexpected OAuth path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	m.codexOAuth = llm.NewDeviceOAuthClient(upstream.Client())
	m.codexOAuth.AuthBaseURL = upstream.URL
	return f
}
func startOAuthTest(t *testing.T, m *Manager, id string, reauthorize bool) string {
	t.Helper()
	r := llmTestRequest(m, "POST", "/v1/admin/llm/oauth/device/start", fmt.Sprintf(`{"providerId":%q,"name":%q,"models":["model-a"],"reauthorize":%t}`, id, "Account "+id, reauthorize), "", true)
	if r.Code != 200 {
		t.Fatalf("start: %d %s", r.Code, r.Body.String())
	}
	var result struct {
		State, UserCode, VerificationURL string
		Interval                         int
	}
	_ = json.Unmarshal(r.Body.Bytes(), &result)
	if result.State == "" || result.UserCode == "" || result.VerificationURL != llm.CodexDeviceVerificationURL || strings.Contains(r.Body.String(), "device_auth_id") {
		t.Fatalf("bad start result: %s", r.Body.String())
	}
	return result.State
}
func readyOAuthPoll(m *Manager, state string) {
	m.llmOAuthMu.Lock()
	m.llmOAuthSessions[state].NextPoll = time.Now().Add(-time.Second)
	m.llmOAuthMu.Unlock()
}
func completeOAuthTest(m *Manager, state string) *httptest.ResponseRecorder {
	return llmTestRequest(m, "POST", "/v1/admin/llm/oauth/device/complete", fmt.Sprintf(`{"state":%q}`, state), "", true)
}
func TestLLMOAuthMultipleAccountsAndPending(t *testing.T) {
	m := newLLMTestManager(t)
	f := setupOAuthFixture(t, m)
	r := llmTestRequest(m, "POST", "/v1/admin/llm/oauth/device/start", `{}`, "", false)
	if r.Code != 401 {
		t.Fatal(r.Code)
	}
	first := startOAuthTest(t, m, "codex-one", false)
	r = completeOAuthTest(m, first)
	if r.Code != 202 || f.polls.Load() != 0 {
		t.Fatal("polling interval not enforced")
	}
	readyOAuthPoll(m, first)
	f.pending.Store(true)
	r = completeOAuthTest(m, first)
	if r.Code != 202 || f.exchanges.Load() != 0 {
		t.Fatal(r.Body.String())
	}
	readyOAuthPoll(m, first)
	f.pending.Store(false)
	r = completeOAuthTest(m, first)
	if r.Code != 200 || strings.Contains(r.Body.String(), "access-") {
		t.Fatal(r.Body.String())
	}
	if r = completeOAuthTest(m, first); r.Code != 404 {
		t.Fatal("consumed state reused")
	}
	second := startOAuthTest(t, m, "codex-two", false)
	readyOAuthPoll(m, second)
	if r = completeOAuthTest(m, second); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var rows []schema.LLMProvider
	m.wdb.Db.Order("id").Find(&rows)
	if len(rows) != 2 {
		t.Fatalf("accounts %d", len(rows))
	}
	a, _, _ := llm.CredentialMetadata(rows[0].Credential)
	b, _, _ := llm.CredentialMetadata(rows[1].Credential)
	if a == b {
		t.Fatal("accounts not independent")
	}
	r = llmTestRequest(m, "GET", "/v1/admin/llm/providers", "", "", true)
	if !strings.Contains(r.Body.String(), "@example.com") || strings.Contains(r.Body.String(), "access-") || strings.Contains(r.Body.String(), "refresh-") {
		t.Fatal(r.Body.String())
	}
}
func TestLLMOAuthStateOwnershipExpiryCancelAndCollision(t *testing.T) {
	m := newLLMTestManager(t)
	f := setupOAuthFixture(t, m)
	state := startOAuthTest(t, m, "codex-one", false)
	m.llmOAuthMu.Lock()
	m.llmOAuthSessions[state].Owner = "other-admin@example.com"
	m.llmOAuthMu.Unlock()
	if r := completeOAuthTest(m, state); r.Code != 404 {
		t.Fatal("other admin can consume state")
	}
	if r := llmTestRequest(m, "DELETE", "/v1/admin/llm/oauth/device/"+state, "", "", true); r.Code != 404 {
		t.Fatal("other admin can cancel state")
	}
	m.llmOAuthMu.Lock()
	m.llmOAuthSessions[state].Owner = "admin@example.com"
	m.llmOAuthSessions[state].ExpiresAt = time.Now().Add(-time.Second)
	m.llmOAuthMu.Unlock()
	if r := completeOAuthTest(m, state); r.Code != 404 || f.exchanges.Load() != 0 {
		t.Fatal("expired state accepted")
	}
	state = startOAuthTest(t, m, "codex-one", false)
	if r := llmTestRequest(m, "DELETE", "/v1/admin/llm/oauth/device/"+state, "", "", true); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := completeOAuthTest(m, state); r.Code != 404 {
		t.Fatal("cancelled state accepted")
	}
	state = startOAuthTest(t, m, "codex-one", false)
	readyOAuthPoll(m, state)
	p := schema.LLMProvider{ID: "codex-one", Kind: "openai", Models: `["model-a"]`, Credential: []byte(`{"api_key":"keep-me"}`)}
	m.wdb.Db.Create(&p)
	if r := completeOAuthTest(m, state); r.Code != 409 {
		t.Fatal("existing provider overwritten")
	}
	m.wdb.Db.First(&p, "id = ?", p.ID)
	if !strings.Contains(string(p.Credential), "keep-me") {
		t.Fatal("credential overwritten")
	}
}
func TestLLMOAuthPersistenceRetryAndReauthorize(t *testing.T) {
	m := newLLMTestManager(t)
	f := setupOAuthFixture(t, m)
	state := startOAuthTest(t, m, "codex-one", false)
	readyOAuthPoll(m, state)
	m.wdb.Db.Callback().Create().Before("gorm:create").Register("test:fail-llm-save", func(db *gorm.DB) {
		if db.Statement.Table == "manager_llm_providers" {
			db.AddError(errors.New("temporary database error"))
		}
	})
	if r := completeOAuthTest(m, state); r.Code != 409 {
		t.Fatal(r.Body.String())
	}
	m.wdb.Db.Callback().Create().Remove("test:fail-llm-save")
	if r := completeOAuthTest(m, state); r.Code != 200 || f.exchanges.Load() != 1 {
		t.Fatalf("grant exchanged again: %d %s", f.exchanges.Load(), r.Body.String())
	}
	var original schema.LLMProvider
	m.wdb.Db.First(&original, "id = ?", "codex-one")
	wrong := startOAuthTest(t, m, "codex-one", true)
	readyOAuthPoll(m, wrong)
	f.accountOverride.Store("wrong-account")
	if r := completeOAuthTest(m, wrong); r.Code != 409 {
		t.Fatal("reauthorized wrong account")
	}
	var current schema.LLMProvider
	m.wdb.Db.First(&current, "id = ?", "codex-one")
	if string(current.Credential) != string(original.Credential) {
		t.Fatal("wrong account credentials saved")
	}
	f.accountOverride.Store("device-1")
	good := startOAuthTest(t, m, "codex-one", true)
	readyOAuthPoll(m, good)
	m.wdb.Db.Model(&schema.LLMProvider{}).Where("id = ?", "codex-one").Updates(map[string]any{"enabled": false, "name": "Edited while authorizing", "models": `["model-b"]`})
	if r := completeOAuthTest(m, good); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	m.wdb.Db.First(&current, "id = ?", "codex-one")
	if current.Enabled || current.Models != `["model-b"]` || current.Name != "Edited while authorizing" {
		t.Fatal("reauthorization overwrote settings")
	}
}

func TestLLMOAuthAutomaticID(t *testing.T) {
	m := newLLMTestManager(t)
	setupOAuthFixture(t, m)
	r := llmTestRequest(m, "POST", "/v1/admin/llm/oauth/device/start", `{"name":"New account","models":["model-a"]}`, "", true)
	var result struct {
		ProviderID string `json:"providerId"`
		State      string `json:"state"`
	}
	json.Unmarshal(r.Body.Bytes(), &result)
	if r.Code != 200 || !strings.HasPrefix(result.ProviderID, "codex-") {
		t.Fatal(r.Body.String())
	}
	readyOAuthPoll(m, result.State)
	if r = completeOAuthTest(m, result.State); r.Code != 200 || !strings.Contains(r.Body.String(), result.ProviderID) {
		t.Fatal(r.Body.String())
	}
}
