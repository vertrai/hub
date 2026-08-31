package starthermes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type weixinResetCredentials struct {
	AccountID     string `json:"accountId"`
	Token         string `json:"token"`
	BaseURL       string `json:"baseUrl"`
	AllowedUserID string `json:"allowedUserId"`
}

func ResetWeixin(encoded string) error {
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode Weixin credentials: %w", err)
	}
	var credentials weixinResetCredentials
	if err := json.Unmarshal(payload, &credentials); err != nil {
		return fmt.Errorf("parse Weixin credentials: %w", err)
	}
	if credentials.AccountID == "" || credentials.Token == "" || credentials.BaseURL == "" || credentials.AllowedUserID == "" {
		return fmt.Errorf("Weixin credentials are incomplete")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	hermes, err := hermesExecutable(home)
	if err != nil {
		return err
	}
	envPath := filepath.Join(home, ".hermes", ".env")
	original, err := os.ReadFile(envPath)
	if err != nil {
		return fmt.Errorf("read Hermes environment: %w", err)
	}
	if err := writeHermesEnv(home, map[string]string{"WEIXIN_ACCOUNT_ID": credentials.AccountID, "WEIXIN_TOKEN": credentials.Token, "WEIXIN_BASE_URL": credentials.BaseURL, "WEIXIN_ALLOWED_USERS": credentials.AllowedUserID, "WEIXIN_DM_POLICY": "allowlist"}); err != nil {
		return err
	}
	if output, err := exec.Command(hermes, "gateway", "restart").CombinedOutput(); err != nil {
		if restoreErr := os.WriteFile(envPath, original, 0o600); restoreErr != nil {
			return fmt.Errorf("restart Hermes gateway: %w: %s; restore environment: %v", err, string(output), restoreErr)
		}
		return fmt.Errorf("restart Hermes gateway: %w: %s", err, string(output))
	}
	return nil
}
