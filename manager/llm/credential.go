package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ImportCredential accepts the reference router's credential JSON or a Codex
// auth.json export. Tokens stay on the server; listings never expose them.
func ImportCredential(raw []byte, baseURL string) ([]byte, error) {
	var c CodexCredential
	if json.Unmarshal(raw, &c) != nil {
		return nil, errors.New("expected credential JSON")
	}
	var auth struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	_ = json.Unmarshal(raw, &auth)
	if c.AccessToken == "" {
		c.AccessToken = auth.Tokens.AccessToken
		c.RefreshToken = auth.Tokens.RefreshToken
		c.IDToken = auth.Tokens.IDToken
		c.AccountID = auth.Tokens.AccountID
	}
	if c.AccountID == "" {
		c.AccountID = jwtNestedStringClaim(c.AccessToken, "https://api.openai.com/auth", "chatgpt_account_id")
	}
	if strings.TrimSpace(c.AccessToken) == "" || strings.TrimSpace(c.AccountID) == "" {
		return nil, errors.New("access_token and account_id required")
	}
	if c.ExpiresAt.IsZero() {
		if exp := int64Value(jwtClaims(c.AccessToken)["exp"]); exp > 0 {
			c.ExpiresAt = time.Unix(exp, 0).UTC()
		} else {
			return nil, errors.New("expires_at required when token has no exp claim")
		}
	}
	c.BaseURL = baseURL
	c.TokenURL = openAICodexOAuthTokenURL
	return json.Marshal(c)
}
func (a *CodexAdapter) PrepareCredential(ctx context.Context, id string, raw []byte) ([]byte, error) {
	var c CodexCredential
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	c, _, err := a.validCredential(ctx, id, c)
	if err != nil {
		return nil, err
	}
	// Always return the cached token too, so a previous persistence failure can
	// be retried without replaying a consumed refresh token.
	return json.Marshal(c)
}
