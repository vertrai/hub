package manager

import (
	"os"
	"testing"
	"time"

	"github.com/vertrai/hub/manager/schema"
	"gorm.io/driver/postgres"
)

func TestWdbStartupDoesNotMigrate(t *testing.T) {
	dsn := os.Getenv("HUB_TEST_COMMERCE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set HUB_TEST_COMMERCE_POSTGRES_DSN to an isolated test database")
	}
	m := newCommercePostgresManager(t, dsn)
	isolatedDSN := m.wdb.Db.Dialector.(*postgres.Dialector).Config.DSN
	started := time.Now()
	db, err := NewWdb(isolatedDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Logf("connection-only startup: %s", time.Since(started))
	if db.Db.Migrator().HasTable(&schema.User{}) {
		t.Fatal("normal startup migrated an empty database")
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&schema.StripeSettings{}, &schema.User{}, &schema.AgentCatalogEntry{}, &schema.Billing{}, &schema.HymatrixPod{}} {
		if !db.Db.Migrator().HasTable(model) {
			t.Fatalf("missing migrated table for %T", model)
		}
	}
	if err := db.Db.Create(&schema.User{ID: "migration-survivor"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	var user schema.User
	if err := db.Db.First(&user, "id = ?", "migration-survivor").Error; err != nil {
		t.Fatal("repeat migration lost user", err)
	}
}
