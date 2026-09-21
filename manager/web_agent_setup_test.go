package manager

import (
	"fmt"
	"github.com/vertrai/hub/manager/schema"
	"strings"
	"testing"
	"time"
)

func TestWebChannelSetupOwnershipAndReservation(t *testing.T) {
	m := newCommerceTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.WeixinBot{}); err != nil {
		t.Fatal(err)
	}
	owner := userToken(t, m, "setup-owner")
	other := userToken(t, m, "setup-other")
	pod := schema.HymatrixPod{ID: "setup-pod", UserID: "google_setup-owner", PID: "confirmed-pid", Status: schema.PodStatusSpawned, BotToken: "allocated-secret"}
	a := schema.WebAgent{ID: "setup-agent", UserID: pod.UserID, PodID: pod.ID, State: "awaiting_setup", Desired: "running", Source: "setup-test", WeixinAuthorizedBotID: "setup-bot"}
	bot := schema.WeixinBot{ID: "setup-bot", UserID: pod.UserID, AccountID: "account", Token: "secret-token", Status: schema.WeixinBotStatusAvailable}
	for _, v := range []any{&pod, &a, &bot} {
		if err := m.wdb.Db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/agents/" + a.ID + "/start"
	if r := webRequest(m, "POST", path, `{"enableTelegram":true}`, other); r.Code != 404 {
		t.Fatal("cross-user start allowed", r.Code)
	}
	if r := webRequest(m, "POST", path, `{}`, owner); r.Code != 400 {
		t.Fatal("channel-free start allowed")
	}
	if r := webRequest(m, "POST", path, `{"weixinBotId":"not-owned"}`, owner); r.Code != 409 {
		t.Fatal("unowned bot allowed")
	}
	if r := webRequest(m, "POST", path, `{"weixinBotId":"setup-bot","enableTelegram":true}`, owner); r.Code != 202 || strings.Contains(r.Body.String(), bot.Token) {
		t.Fatal(r.Code, r.Body.String())
	}
	m.wdb.Db.First(&a, "id = ?", a.ID)
	m.wdb.Db.First(&pod, "id = ?", pod.ID)
	m.wdb.Db.First(&bot, "id = ?", bot.ID)
	if !a.ChannelConfigured || !a.EnableTelegram || a.State != "queued" || pod.WeixinBotID != bot.ID || bot.AssignedPodID == nil || *bot.AssignedPodID != pod.ID {
		t.Fatal("configuration not durably reserved")
	}
	if r := webRequest(m, "POST", path, `{"enableTelegram":true}`, owner); r.Code != 409 {
		t.Fatal("duplicate start allowed")
	}
}

func TestWebWeixinConfirmedAuthorization(t *testing.T) {
	for _, missingColumn := range []bool{false, true} {
		t.Run(fmt.Sprint("missingColumn=", missingColumn), func(t *testing.T) {
			m := newCommerceTestManager(t)
			token := userToken(t, m, "scan-owner")
			a := schema.WebAgent{ID: "scan-agent", UserID: "google_scan-owner", State: "awaiting_setup", Desired: "running", Source: "scan-test"}
			if err := m.wdb.Db.Create(&a).Error; err != nil {
				t.Fatal(err)
			}
			m.weixinAttempts = map[string]weixinAttempt{"scan": {ID: "scan", UserID: a.UserID, WebAgentID: a.ID, CredentialExpiresAt: time.Now().Add(time.Minute), Credentials: &WeixinCredentials{BotID: "scan-bot"}}}
			if missingColumn {
				if err := m.wdb.Db.Migrator().DropColumn(&schema.WebAgent{}, "WeixinAuthorizedBotID"); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				r := webRequest(m, "GET", "/v1/agents/scan-agent/weixin/scan", "", token)
				want := 200
				if missingColumn {
					want = 500
				}
				if r.Code != want {
					t.Fatalf("want %d, got %d: %s", want, r.Code, r.Body.String())
				}
				if !missingColumn {
					var saved schema.WebAgent
					if err := m.wdb.Db.First(&saved, "id = ?", a.ID).Error; err != nil {
						t.Fatal(err)
					}
					if saved.WeixinAuthorizedBotID != "scan-bot" {
						t.Fatal("authorization not saved")
					}
				}
			}
		})
	}
}

func TestWebAgentListsActualWeixinBinding(t *testing.T) {
	m := newCommerceTestManager(t)
	token := userToken(t, m, "bound-owner")
	pod := schema.HymatrixPod{ID: "bound-pod", UserID: "google_bound-owner", PID: "bound-pid", WeixinBotID: "bound-bot"}
	a := schema.WebAgent{ID: "bound-agent", UserID: pod.UserID, PodID: pod.ID, Source: "bound-test", State: "running", BotUsername: "test_bot", EnableTelegram: true, ChannelConfigured: true}
	for _, row := range []any{&pod, &a} {
		if err := m.wdb.Db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := webRequest(m, "GET", "/v1/agents", "", token)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"type":"weixin"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	// An authorization alone is not an active channel binding.
	m.wdb.Db.Model(&pod).Update("weixin_bot_id", "")
	m.wdb.Db.Model(&a).Update("weixin_authorized_bot_id", "authorized-only")
	r = webRequest(m, "GET", "/v1/agents", "", token)
	if r.Code != 200 || strings.Contains(r.Body.String(), `"type":"weixin"`) {
		t.Fatal(r.Code, r.Body.String())
	}
}
