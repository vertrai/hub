package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vertrai/hub/manager/schema"
)

func alertTestManager(t *testing.T) *Manager {
	t.Helper()
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.ResourceAlertConfig{}, &schema.ResourceAlertDelivery{}, &schema.XboxChild{}, &schema.NetEaseAccount{}); err != nil {
		t.Fatal(err)
	}
	return m
}
func saveAlertTestConfig(t *testing.T, m *Manager, cfg resourceAlertSettings) {
	t.Helper()
	b, _ := json.Marshal(cfg)
	if err := m.wdb.Db.Save(&schema.ResourceAlertConfig{ID: 1, Config: string(b)}).Error; err != nil {
		t.Fatal(err)
	}
}
func TestResourceAlertThresholdCooldownRecoveryAndChannelRetry(t *testing.T) {
	m := alertTestManager(t)
	cfg := defaultResourceAlerts()
	cfg.Rules[0] = resourceAlertRule{Pool: "xbox-child", Enabled: true, Threshold: 0, RepeatHours: 2, Telegram: true, Email: true}
	saveAlertTestConfig(t, m, cfg)
	now := time.Now().UTC()
	calls := map[string]int{}
	failEmail := true
	send := func(_ context.Context, _ resourceAlertSettings, ch, body string) error {
		calls[ch]++
		if !strings.Contains(body, "当前可用：0") {
			t.Fatal(body)
		}
		if ch == "email" && failEmail {
			return errors.New("private server detail")
		}
		return nil
	}
	check := func(at time.Time) {
		t.Helper()
		if err := m.checkResourceAlerts(context.Background(), at, send); err != nil {
			t.Fatal(err)
		}
	}
	check(now)
	check(now.Add(time.Minute))
	if calls["telegram"] != 1 || calls["email"] != 1 {
		t.Fatal(calls)
	}
	failEmail = false
	check(now.Add(6 * time.Minute))
	if calls["telegram"] != 1 || calls["email"] != 2 {
		t.Fatal(calls)
	}
	check(now.Add(3 * time.Hour))
	if calls["telegram"] != 2 || calls["email"] != 3 {
		t.Fatal(calls)
	}
	if err := m.wdb.Db.Create(&schema.XboxChild{ID: "available", Email: "test@example.com", Password: "secret"}).Error; err != nil {
		t.Fatal(err)
	}
	check(now.Add(3*time.Hour + time.Minute))
	if err := m.wdb.Db.Model(&schema.XboxChild{}).Where("id = ?", "available").Update("hub_access_key_id", "assigned").Error; err != nil {
		t.Fatal(err)
	}
	check(now.Add(3*time.Hour + 2*time.Minute))
	if calls["telegram"] != 3 || calls["email"] != 4 {
		t.Fatal(calls)
	}
}
func TestResourceAlertLeaseAndDisabledRules(t *testing.T) {
	m := alertTestManager(t)
	cfg := defaultResourceAlerts()
	now := time.Now().UTC()
	calls := 0
	send := func(context.Context, resourceAlertSettings, string, string) error { calls++; return nil }
	saveAlertTestConfig(t, m, cfg)
	if err := m.checkResourceAlerts(context.Background(), now, send); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("disabled sent")
	}
	cfg.Rules[1].Enabled = true
	cfg.Rules[1].Telegram = true
	saveAlertTestConfig(t, m, cfg)
	state := schema.ResourceAlertDelivery{ID: "netease:telegram", LeaseUntil: now.Add(time.Minute)}
	if err := m.wdb.Db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.checkResourceAlerts(context.Background(), now, send); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("active lease sent")
	}
	if err := m.checkResourceAlerts(context.Background(), now.Add(2*time.Minute), send); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("expired lease did not recover")
	}
}
func TestResourceAlertAdminSettings(t *testing.T) {
	m := alertTestManager(t)
	path := "/v1/admin/resource-alerts"
	if r := llmTestRequest(m, "GET", path, "", "", false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	cfg := defaultResourceAlerts()
	cfg.TelegramToken = "123:secret-token"
	cfg.SMTPPassword = "secret-smtp"
	cfg.TelegramChatID = "1234"
	cfg.Rules[0].Enabled = true
	cfg.Rules[0].Telegram = true
	cfg.Rules[0].Threshold = 7
	b, _ := json.Marshal(cfg)
	r := llmTestRequest(m, "PUT", path, string(b), "", true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = llmTestRequest(m, "GET", path, "", "", true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "secret-token") || strings.Contains(r.Body.String(), "secret-smtp") {
		t.Fatal(r.Code, r.Body.String())
	}
	cfg.TelegramToken = ""
	cfg.SMTPPassword = ""
	cfg.Rules[0].Threshold = 0
	b, _ = json.Marshal(cfg)
	r = llmTestRequest(m, "PUT", path, string(b), "", true)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	stored, err := m.readResourceAlerts(context.Background())
	if err != nil || stored.TelegramToken != "123:secret-token" || stored.SMTPPassword != "secret-smtp" || stored.Rules[0].Threshold != 0 {
		t.Fatal("secret preservation / configurable zero threshold failed", err)
	}
	cfg.Rules[0].Threshold = -1
	b, _ = json.Marshal(cfg)
	if r = llmTestRequest(m, "PUT", path, string(b), "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	cfg.Rules[0].Threshold = 2
	cfg.Rules[0].Email = true
	cfg.SMTPHost = "smtp.example.com"
	cfg.EmailFrom = "sender@example.com\r\nBcc: other@example.com"
	cfg.EmailTo = "to@example.com"
	b, _ = json.Marshal(cfg)
	if r = llmTestRequest(m, "PUT", path, string(b), "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}
}

func TestResourceAlertConcurrentWorkers(t *testing.T) {
	m := alertTestManager(t)
	cfg := defaultResourceAlerts()
	cfg.Rules[0].Enabled = true
	cfg.Rules[0].Telegram = true
	saveAlertTestConfig(t, m, cfg)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	now := time.Now().UTC()
	go func() {
		done <- m.checkResourceAlerts(context.Background(), now, func(context.Context, resourceAlertSettings, string, string) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	err := m.checkResourceAlerts(context.Background(), now, func(context.Context, resourceAlertSettings, string, string) error {
		t.Error("second worker sent duplicate alert")
		return nil
	})
	close(release)
	if firstErr := <-done; firstErr != nil {
		t.Fatal(firstErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}
