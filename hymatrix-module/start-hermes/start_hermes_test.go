package starthermes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayConfigFromEnvUsesCompletePair(t *testing.T) {
	t.Setenv("HUB_GATEWAY_URL", "https://hub.example")
	t.Setenv("HUB_GATEWAY_API_KEY", "hub-key")
	t.Setenv("AGENT_ACCESS_GATEWAY_URL", "https://legacy.example")
	t.Setenv("AGENT_ACCESS_GATEWAY_API_KEY", "legacy-key")
	config, err := GatewayConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.URL != "https://hub.example" || config.APIKey != "hub-key" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestGatewayConfigFromEnvFallsBackAsPair(t *testing.T) {
	t.Setenv("HUB_GATEWAY_URL", "https://partial.example")
	t.Setenv("HUB_GATEWAY_API_KEY", "")
	t.Setenv("AGENT_ACCESS_GATEWAY_URL", "https://legacy.example")
	t.Setenv("AGENT_ACCESS_GATEWAY_API_KEY", "legacy-key")
	config, err := GatewayConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.URL != "https://legacy.example" || config.APIKey != "legacy-key" {
		t.Fatalf("mixed credential pair: %#v", config)
	}
}

func TestInstallSkills(t *testing.T) {
	home := t.TempDir()
	if err := InstallSkills(home); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway-google-account", "gateway-google-auth", "gateway-google-workspace", "gateway-remote-browser"} {
		if _, err := os.Stat(filepath.Join(home, ".hermes", "skills", name, "SKILL.md")); err != nil {
			t.Errorf("skill %s was not installed: %v", name, err)
		}
	}
}

func TestWriteHermesGatewayEnvPreservesUnrelatedValues(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, ".env")
	if err := os.WriteFile(path, []byte("KEEP_ME=yes\nHUB_GATEWAY_URL=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteHermesGatewayEnv(home, GatewayConfig{URL: "https://hub.example", APIKey: "gw_sk_test"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"KEEP_ME=yes", "HUB_GATEWAY_URL=https://hub.example", "HUB_GATEWAY_API_KEY=gw_sk_test"} {
		if !containsLine(text, expected) {
			t.Errorf("missing %q in %q", expected, text)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("environment file mode = %v, err = %v", info.Mode().Perm(), err)
	}
}

func TestWriteHermesGatewayEnvRejectsNewlines(t *testing.T) {
	home := t.TempDir()
	err := WriteHermesGatewayEnv(home, GatewayConfig{URL: "https://hub.example\nINJECTED=yes", APIKey: "gw_sk_test"})
	if err == nil {
		t.Fatal("expected newline validation error")
	}
}

func TestWriteHermesGatewayEnvConfiguresTelegramAndWeixinTogether(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_AGENT_TELEGRAM_BOT_TOKEN", "telegram-token")
	t.Setenv("HERMES_AGENT_WEIXIN_ACCOUNT_ID", "bot@im.bot")
	t.Setenv("HERMES_AGENT_WEIXIN_TOKEN", "weixin-token")
	t.Setenv("HERMES_AGENT_WEIXIN_BASE_URL", "https://ilinkai.weixin.qq.com")
	t.Setenv("HERMES_AGENT_WEIXIN_ALLOWED_USERS", "user@im.wechat")
	if err := WriteHermesGatewayEnv(home, GatewayConfig{URL: "https://hub.example", APIKey: "gw_sk_test"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".hermes", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"TELEGRAM_BOT_TOKEN=telegram-token", "WEIXIN_ACCOUNT_ID=bot@im.bot",
		"WEIXIN_TOKEN=weixin-token", "WEIXIN_BASE_URL=https://ilinkai.weixin.qq.com",
		"WEIXIN_DM_POLICY=allowlist", "WEIXIN_ALLOWED_USERS=user@im.wechat",
	} {
		if !containsLine(string(content), expected) {
			t.Errorf("missing %q in Hermes environment", expected)
		}
	}
}

func TestWriteHermesGatewayEnvRejectsPartialWeixinConfig(t *testing.T) {
	t.Setenv("HERMES_AGENT_WEIXIN_TOKEN", "weixin-token")
	if err := WriteHermesGatewayEnv(t.TempDir(), GatewayConfig{URL: "https://hub.example", APIKey: "gw_sk_test"}); err == nil {
		t.Fatal("expected incomplete Weixin configuration error")
	}
}

func TestWriteHermesGatewayEnvClearsUnselectedChannels(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := "TELEGRAM_BOT_TOKEN=old-telegram\nWEIXIN_TOKEN=old-weixin\nWEIXIN_ACCOUNT_ID=old-account\n"
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteHermesGatewayEnv(home, GatewayConfig{URL: "https://hub.example", APIKey: "gw_sk_test"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(directory, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"TELEGRAM_BOT_TOKEN=", "WEIXIN_TOKEN=", "WEIXIN_ACCOUNT_ID="} {
		if !containsLine(string(content), expected) {
			t.Errorf("managed channel value was not cleared: %q", expected)
		}
	}
}

func TestConfigureTelegramAutoHomeChannelInstallsPlugin(t *testing.T) {
	home := t.TempDir()
	if err := ConfigureTelegramAutoHomeChannel(home, "/usr/bin/true"); err != nil {
		t.Fatal(err)
	}
	pluginDirectory := filepath.Join(home, ".hermes", "plugins", telegramAutoHomePluginName)
	for _, name := range []string{"plugin.yaml", "__init__.py"} {
		path := filepath.Join(pluginDirectory, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("plugin file %s was not installed: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("plugin file %s mode = %o", name, info.Mode().Perm())
		}
	}
}

func TestTelegramAutoHomePluginPythonCompiles(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	path := filepath.Join(t.TempDir(), "telegram_auto_home.py")
	if err := os.WriteFile(path, []byte(telegramAutoHomePluginCode()), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "-m", "py_compile", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated Telegram auto-home plugin must compile: %v\n%s", err, output)
	}
}

func TestTelegramAutoHomePluginDefaultsToDMOnly(t *testing.T) {
	code := telegramAutoHomePluginCode()
	for _, expected := range []string{
		`chat_type != "dm"`,
		`HERMES_AUTO_TELEGRAM_HOME_ALLOW_GROUPS`,
		`ctx.register_hook("pre_gateway_dispatch", auto_set_telegram_home)`,
	} {
		if !strings.Contains(code, expected) {
			t.Errorf("plugin is missing %q", expected)
		}
	}
}

func containsLine(content, expected string) bool {
	for _, line := range splitLines(content) {
		if line == expected {
			return true
		}
	}
	return false
}

func splitLines(content string) []string {
	var lines []string
	start := 0
	for index, value := range content {
		if value == '\n' {
			lines = append(lines, content[start:index])
			start = index + 1
		}
	}
	return lines
}
