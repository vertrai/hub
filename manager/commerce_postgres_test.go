package manager

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/vertrai/hub/manager/schema"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Every test gets a separate schema. The opt-in DSN must point at a test DB.
func newCommercePostgresManager(t *testing.T, dsn string) *Manager {
	t.Helper()
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	name := commerceID("commerce_test_")
	if err = admin.Exec("CREATE SCHEMA " + name).Error; err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
		admin.Exec("DROP SCHEMA " + name + " CASCADE")
		sqlAdmin, _ := admin.DB()
		sqlAdmin.Close()
	})
	m, err := New("test", Config{}, &Wdb{Db: db})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestCommercePostgresConcurrentRedemptionAndEvents(t *testing.T) {
	if os.Getenv("HUB_TEST_COMMERCE_POSTGRES_DSN") == "" {
		t.Skip("set HUB_TEST_COMMERCE_POSTGRES_DSN for PostgreSQL concurrency verification")
	}
	m := newCommerceTestManager(t)
	if err := m.wdb.Db.Create(&schema.InviteCode{Code: "CONCURRENT"}).Error; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.reserveInviteAgent("CONCURRENT", fmt.Sprintf("user%d", i), "x_agent", schema.AgentCatalogEntry{ID: "x", Module: "module_x"})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("one code granted to %d users", success)
	}
	f, _ := billingFixture(t, m)
	event := subscriptionEvent("evt_parallel", f.subscription)
	results = make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- m.processStripeEvent(context.Background(), event) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	m.wdb.Db.Model(&schema.WebAgent{}).Where("source = ?", "stripe:sub_test").Count(&count)
	if count != 1 {
		t.Fatalf("duplicate paid instances: %d", count)
	}
}
