package manager

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vertrai/hub/manager/schema"
)

func TestInviteClaimDistributionAndRedemption(t *testing.T) {
	m := newCommerceTestManager(t)
	now := time.Now()
	expired := now.Add(-time.Hour)
	for _, code := range []schema.InviteCode{{Code: "ABCDEF"}, {Code: "BBBBBB"}, {Code: "ZZZZZZ", RevokedAt: &now}, {Code: "EXPIRE", ExpiresAt: &expired}, {Code: "USEDXX", UsedAt: &now}} {
		if err := m.wdb.Db.Create(&code).Error; err != nil {
			t.Fatal(err)
		}
	}
	claim := func(body string) int { return catalogRequest(m, "POST", "/", body, "", m.claimInviteCodes, nil).Code }
	if status := claim(`{"codes":["ABCDEF"]}`); status != 200 {
		t.Fatal(status)
	}
	if status := claim(`{"codes":["ABCDEF"]}`); status != 409 {
		t.Fatal("duplicate claim", status)
	}
	if status := claim(`{"codes":["BBBBBB","ZZZZZZ"]}`); status != 409 {
		t.Fatal("invalid batch", status)
	}
	var row schema.InviteCode
	if err := m.wdb.Db.First(&row, "code = ?", "BBBBBB").Error; err != nil || row.ClaimedAt != nil {
		t.Fatal("partial batch committed", err)
	}
	for _, body := range []string{`{"codes":["EXPIRE"]}`, `{"codes":["USEDXX"]}`, `{"codes":["MISSING"]}`} {
		if claim(body) != 409 {
			t.Fatal("unavailable code claimed")
		}
	}
	token := userToken(t, m, "recipient")
	if r := webRequest(m, "POST", "/v1/invite-codes/redeem", `{"product":"x_agent","inviteCode":"ABCDEF"}`, token); r.Code != 200 {
		t.Fatal("claimed code cannot redeem", r.Code, r.Body.String())
	}
	other := userToken(t, m, "other")
	if r := webRequest(m, "POST", "/v1/invite-codes/redeem", `{"product":"x_agent","inviteCode":"ABCDEF"}`, other); r.Code != 403 {
		t.Fatal("second user redeemed", r.Code)
	}
}
func TestInviteClaimConcurrentPostgres(t *testing.T) {
	if os.Getenv("HUB_TEST_COMMERCE_POSTGRES_DSN") == "" {
		t.Skip("requires test PostgreSQL")
	}
	m := newCommerceTestManager(t)
	if err := m.wdb.Db.Create(&schema.InviteCode{Code: "ABCDEF"}).Error; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- catalogRequest(m, "POST", "/", `{"codes":["ABCDEF"]}`, "", m.claimInviteCodes, nil).Code
		}()
	}
	wg.Wait()
	close(statuses)
	success := 0
	for status := range statuses {
		if status == 200 {
			success++
		} else if status != 409 {
			t.Fatal(status)
		}
	}
	if success != 1 {
		t.Fatalf("claim succeeded %d times", success)
	}
}
