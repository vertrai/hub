package starthermes

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type GatewayConfig struct {
	URL    string
	APIKey string
}

func GatewayConfigFromEnv() (GatewayConfig, error) {
	current := GatewayConfig{URL: env("HUB_GATEWAY_URL"), APIKey: env("HUB_GATEWAY_API_KEY")}
	if current.URL != "" && current.APIKey != "" {
		return current, nil
	}
	legacy := GatewayConfig{URL: env("AGENT_ACCESS_GATEWAY_URL"), APIKey: env("AGENT_ACCESS_GATEWAY_API_KEY")}
	if legacy.URL != "" && legacy.APIKey != "" {
		return legacy, nil
	}
	return GatewayConfig{}, fmt.Errorf("HUB_GATEWAY_URL and HUB_GATEWAY_API_KEY are required as a complete pair")
}

func WriteHermesGatewayEnv(home string, config GatewayConfig) error {
	values := map[string]string{
		"HUB_GATEWAY_URL":              config.URL,
		"HUB_GATEWAY_API_KEY":          config.APIKey,
		"AGENT_ACCESS_GATEWAY_URL":     config.URL,
		"AGENT_ACCESS_GATEWAY_API_KEY": config.APIKey,
		"TELEGRAM_BOT_TOKEN":           "",
		"TELEGRAM_ALLOW_ALL_USERS":     "",
		"GATEWAY_ALLOW_ALL_USERS":      "",
		"WEIXIN_ACCOUNT_ID":            "",
		"WEIXIN_TOKEN":                 "",
		"WEIXIN_BASE_URL":              "",
		"WEIXIN_DM_POLICY":             "",
		"WEIXIN_ALLOWED_USERS":         "",
	}
	if token := envFirst("HERMES_AGENT_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN", "Bot_Token"); token != "" {
		values["TELEGRAM_BOT_TOKEN"] = token
		values["TELEGRAM_ALLOW_ALL_USERS"] = "true"
		values["GATEWAY_ALLOW_ALL_USERS"] = "true"
	}
	weixin := map[string]string{
		"WEIXIN_ACCOUNT_ID":    envFirst("HERMES_AGENT_WEIXIN_ACCOUNT_ID", "WEIXIN_ACCOUNT_ID"),
		"WEIXIN_TOKEN":         envFirst("HERMES_AGENT_WEIXIN_TOKEN", "WEIXIN_TOKEN"),
		"WEIXIN_BASE_URL":      envFirst("HERMES_AGENT_WEIXIN_BASE_URL", "WEIXIN_BASE_URL"),
		"WEIXIN_ALLOWED_USERS": envFirst("HERMES_AGENT_WEIXIN_ALLOWED_USERS", "WEIXIN_ALLOWED_USERS"),
	}
	configured := 0
	for _, value := range weixin {
		if value != "" {
			configured++
		}
	}
	if configured != 0 && configured != len(weixin) {
		return fmt.Errorf("Weixin channel requires account ID, token, base URL and allowed users as a complete set")
	}
	if configured == len(weixin) {
		for key, value := range weixin {
			values[key] = value
		}
		values["WEIXIN_DM_POLICY"] = "allowlist"
	}
	return writeHermesEnv(home, values)
}

func writeHermesEnv(home string, values map[string]string) error {
	for key, value := range values {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("environment value for %s cannot contain newlines", key)
		}
	}
	directory := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Hermes directory: %w", err)
	}
	path := filepath.Join(directory, ".env")
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(values))
	for index, line := range lines {
		key := envLineKey(line)
		if value, ok := values[key]; ok {
			lines[index] = key + "=" + value
			seen[key] = true
		}
	}
	for _, key := range sortedKeys(values) {
		if !seen[key] {
			lines = append(lines, key+"="+values[key])
		}
	}
	content := []byte(strings.Join(lines, "\n") + "\n")
	temporary, err := os.CreateTemp(directory, ".hub-gateway-env-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func envFirst(names ...string) string {
	for _, name := range names {
		if value := env(name); value != "" {
			return value
		}
	}
	return ""
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func envLineKey(line string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
	if index := strings.Index(trimmed, "="); index > 0 {
		return strings.TrimSpace(trimmed[:index])
	}
	return ""
}
