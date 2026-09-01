package xbot

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/vertrai/hub/resouces/schema"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&schema.AccessKey{}, &schema.GoogleAccount{}, &schema.XBotAccount{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPoolAllowsMultiplePendingBotsAndRegistrationWithoutGamertag(t *testing.T) {
	db := testDB(t)
	service := New(db)
	for _, suffix := range []string{"one", "two"} {
		google := schema.GoogleAccount{ID: "google_" + suffix, Email: suffix + "@example.com", Password: "password-" + suffix, GoogleUserID: "workspace_" + suffix, Status: schema.StatusAvailable}
		if err := db.Create(&google).Error; err != nil {
			t.Fatal(err)
		}
		bot, err := service.CreateFromGoogle(google)
		if err != nil {
			t.Fatal(err)
		}
		if bot.Status != schema.XBotStatusPendingRegistration || bot.Gamertag != nil {
			t.Fatalf("unexpected pending bot: %#v", bot)
		}
	}
	rows, err := service.List()
	if err != nil || len(rows) != 2 {
		t.Fatalf("bots=%d err=%v", len(rows), err)
	}
	registered, err := service.MarkRegistered(rows[0].ID, "")
	if err != nil || registered.Status != schema.XBotStatusRegistered {
		t.Fatalf("registered=%#v err=%v", registered, err)
	}
}

func TestAcquireIsIdempotentAndExclusivePerAccessKey(t *testing.T) {
	db := testDB(t)
	service := New(db)
	for _, id := range []string{"key_one", "key_two"} {
		if err := db.Create(&schema.AccessKey{ID: id, OwnerUserID: id, KeyHash: "hash_" + id, KeyPrefix: "gw", Status: schema.StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{"one", "two"} {
		google := schema.GoogleAccount{ID: "google_" + suffix, Email: suffix + "@example.com", Password: "password-" + suffix, GoogleUserID: "workspace_" + suffix, Status: schema.StatusAvailable}
		if err := db.Create(&google).Error; err != nil {
			t.Fatal(err)
		}
		bot, err := service.CreateFromGoogle(google)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.MarkRegistered(bot.ID, "tag_"+suffix); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.Acquire("key_one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.Acquire("key_one")
	if err != nil || again.ID != first.ID || again.Password != first.Password {
		t.Fatalf("repeat acquire returned %#v, err=%v; want %#v", again, err, first)
	}
	second, err := service.Acquire("key_two")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("different keys acquired the same bot %q", first.ID)
	}
	if _, err := service.Acquire("missing_key"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing key error=%v", err)
	}
}
