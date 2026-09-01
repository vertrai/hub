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
	if err := db.AutoMigrate(&schema.AccessKey{}, &schema.GoogleAccount{}, &schema.GoogleAccountAssignment{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func addGoogleUser(t *testing.T, db *gorm.DB, suffix string) schema.GoogleAccount {
	t.Helper()
	row := schema.GoogleAccount{ID: "google_" + suffix, Email: suffix + "@example.com", Password: "password-" + suffix, GoogleUserID: "workspace_" + suffix, Purpose: schema.GooglePurposeGeneral, Status: schema.StatusAvailable}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestDesignateReusesExistingGoogleUser(t *testing.T) {
	db := testDB(t)
	service := New(db)
	google := addGoogleUser(t, db, "one")
	xbox, err := service.Designate(google.ID)
	if err != nil {
		t.Fatal(err)
	}
	if xbox.ID != google.ID || xbox.Email != google.Email || xbox.Password != google.Password || xbox.Purpose != schema.GooglePurposeXbox {
		t.Fatalf("designated=%#v original=%#v", xbox, google)
	}
	if _, err := service.Designate(google.ID); err == nil {
		t.Fatal("already designated user should not be designated twice")
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
		google := addGoogleUser(t, db, suffix)
		if _, err := service.Designate(google.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.Acquire("key_one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.Acquire("key_one")
	if err != nil || again.ID != first.ID || again.Password != first.Password {
		t.Fatalf("repeat=%#v err=%v want=%#v", again, err, first)
	}
	second, err := service.Acquire("key_two")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("different keys acquired %q", first.ID)
	}
	if _, err := service.Acquire("missing_key"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing key error=%v", err)
	}
}
