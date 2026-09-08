package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Device OAuth follows llm-token-router's administrative device-code flow.
// All exchange endpoints are server-owned; the browser only gets the user code.
type DeviceOAuthClient struct {
	Client      *http.Client
	AuthBaseURL string
}

const CodexDeviceVerificationURL = "https://auth.openai.com/codex/device"
const codexDeviceRedirect = "https://auth.openai.com/deviceauth/callback"

var ErrAuthorizationPending = errors.New("authorization pending")
var ErrOAuthSlowDown = errors.New("OAuth polling too fast")

type DeviceAuthorization struct {
	DeviceAuthID string
	UserCode     string
	ExpiresIn    time.Duration
	Interval     time.Duration
}
type DeviceGrant struct{ Code, Verifier string }

func NewDeviceOAuthClient(client *http.Client) *DeviceOAuthClient {
	return &DeviceOAuthClient{Client: client, AuthBaseURL: "https://auth.openai.com"}
}
func (o *DeviceOAuthClient) Start(ctx context.Context) (DeviceAuthorization, error) {
	payload, err := o.postJSON(ctx, "/api/accounts/deviceauth/usercode", map[string]any{"client_id": openAICodexOAuthClientID}, false)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	result := DeviceAuthorization{DeviceAuthID: stringValue(payload["device_auth_id"]), UserCode: stringValue(payload["user_code"])}
	if result.DeviceAuthID == "" || result.UserCode == "" {
		return result, errors.New("OpenAI 未返回完整的授权码")
	}
	seconds := int64Value(payload["expires_in"])
	if seconds <= 0 || seconds > 1800 {
		seconds = 900
	}
	result.ExpiresIn = time.Duration(seconds) * time.Second
	interval := int64Value(payload["interval"])
	if interval < 5 {
		interval = 5
	}
	if interval > 60 {
		interval = 60
	}
	result.Interval = time.Duration(interval) * time.Second
	return result, nil
}
func (o *DeviceOAuthClient) Poll(ctx context.Context, deviceID, userCode string) (DeviceGrant, error) {
	payload, err := o.postJSON(ctx, "/api/accounts/deviceauth/token", map[string]any{"client_id": openAICodexOAuthClientID, "device_auth_id": deviceID, "user_code": userCode}, true)
	if err != nil {
		return DeviceGrant{}, err
	}
	grant := DeviceGrant{Code: stringValue(payload["authorization_code"]), Verifier: stringValue(payload["code_verifier"])}
	if grant.Code == "" || grant.Verifier == "" {
		return grant, errors.New("OpenAI 授权结果不完整，请重新授权")
	}
	return grant, nil
}
func (o *DeviceOAuthClient) Exchange(ctx context.Context, grant DeviceGrant) ([]byte, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {openAICodexOAuthClientID}, "code": {grant.Code}, "code_verifier": {grant.Verifier}, "redirect_uri": {codexDeviceRedirect}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.AuthBaseURL, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	payload, err := o.execute(req, false)
	if err != nil {
		return nil, err
	}
	return credentialFromOAuthPayload(payload)
}
func (o *DeviceOAuthClient) postJSON(ctx context.Context, path string, body map[string]any, pending bool) (map[string]any, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.AuthBaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return o.execute(req, pending)
}
func (o *DeviceOAuthClient) execute(req *http.Request, pending bool) (map[string]any, error) {
	req.Header.Set("User-Agent", "codex_cli_rs/0.125.0")
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接 OpenAI 授权服务，请检查 Manager 出口网络")
	}
	defer resp.Body.Close()
	raw, err := readBounded(resp.Body, 1<<20)
	if err != nil {
		return nil, errors.New("读取 OpenAI 授权结果失败")
	}
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	code := stringValue(payload["error"])
	if detail, ok := payload["error"].(map[string]any); ok {
		code = firstNonEmpty(stringValue(detail["code"]), stringValue(detail["type"]))
	}
	// Explicit denial/expiry must not be mistaken for the device flow's normal
	// 403/404 pending response.
	switch code {
	case "unsupported_country_region_territory":
		return nil, errors.New("OpenAI 不支持当前 Manager 出口所在国家或地区（unsupported_country_region_territory）。请核对服务部署地区；重复授权无法解决此问题")
	case "access_denied":
		return nil, errors.New("OpenAI 授权被拒绝，请重新授权")
	case "expired_token", "invalid_grant":
		return nil, errors.New("OpenAI 授权已过期或失效，请重新授权")
	}
	if pending && (resp.StatusCode == 403 || resp.StatusCode == 404) {
		return nil, ErrAuthorizationPending
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if pending && code == "authorization_pending" {
			return nil, ErrAuthorizationPending
		}
		if pending && (code == "slow_down" || resp.StatusCode == 429) {
			return nil, ErrOAuthSlowDown
		}
		// Never include the upstream body: it may contain an authorization code.

		return nil, fmt.Errorf("OpenAI 授权请求失败（HTTP %d），请重试或重新授权", resp.StatusCode)
	}
	if payload == nil {
		return nil, errors.New("OpenAI 授权响应无效")
	}
	return payload, nil
}
func credentialFromOAuthPayload(payload map[string]any) ([]byte, error) {
	c := CodexCredential{AccessToken: stringValue(payload["access_token"]), RefreshToken: stringValue(payload["refresh_token"]), IDToken: stringValue(payload["id_token"]), BaseURL: defaultCodexBackendURL, TokenURL: openAICodexOAuthTokenURL}
	for _, token := range []string{c.IDToken, c.AccessToken} {
		if c.AccountID != "" {
			break
		}
		c.AccountID = firstNonEmpty(jwtNestedStringClaim(token, "https://api.openai.com/auth", "chatgpt_account_id"), stringValue(jwtClaims(token)["https://api.openai.com/auth.chatgpt_account_id"]))
	}
	if seconds := int64Value(payload["expires_in"]); seconds > 0 && seconds < 365*24*3600 {
		c.ExpiresAt = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	}
	if c.ExpiresAt.IsZero() {
		if exp := int64Value(jwtClaims(c.AccessToken)["exp"]); exp > 0 {
			c.ExpiresAt = time.Unix(exp, 0).UTC()
		}
	}
	if c.AccessToken == "" || c.RefreshToken == "" || c.AccountID == "" || c.ExpiresAt.IsZero() {
		return nil, errors.New("OpenAI 未返回完整的账号与刷新凭据，请重新授权")
	}
	return json.Marshal(c)
}

// CredentialMetadata exposes account identity and expiry, never tokens.
func CredentialMetadata(raw []byte) (accountID, email string, expiresAt time.Time) {
	var c CodexCredential
	if json.Unmarshal(raw, &c) != nil {
		return "", "", time.Time{}
	}
	return c.AccountID, stringValue(jwtClaims(c.IDToken)["email"]), c.ExpiresAt
}
