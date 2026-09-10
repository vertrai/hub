package google

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/vertrai/hub/resouces/schema"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

type fixedTokenIssuer struct{}

func (fixedTokenIssuer) Issue(_ context.Context, email string) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "token-for-" + email, Expiry: time.Now().Add(time.Hour)}, nil
}

func TestIssueTokenUsesAlreadyAssignedXboxAccountWithoutPurpose(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&schema.AccessKey{}, &schema.GoogleAccount{}); err != nil {
		t.Fatal(err)
	}
	keyID := "key_xbox"
	if err := db.Create(&schema.AccessKey{ID: keyID, OwnerUserID: "owner", KeyHash: "hash", KeyPrefix: "gw", Status: schema.StatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	account := schema.GoogleAccount{ID: "google_xbox", Email: "xbox@example.com", Password: "password", GoogleUserID: "workspace", Purpose: schema.GooglePurposeXbox, XboxStatus: schema.XboxStatusReady, Status: schema.StatusAvailable}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	service := NewService(db, nil, fixedTokenIssuer{}, "")
	if _, err := service.AcquireAccount(context.Background(), keyID, schema.GooglePurposeXbox); err != nil {
		t.Fatal(err)
	}
	token, assigned, err := service.IssueToken(context.Background(), keyID)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.ID != account.ID || token.AccessToken != "token-for-"+account.Email {
		t.Fatalf("assigned=%#v token=%#v", assigned, token)
	}
}
