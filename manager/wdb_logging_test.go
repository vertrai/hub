package manager

import (
	"bytes"
	"context"
	"errors"
	stdlog "log"
	"strings"
	"testing"

	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

func TestManagerDatabaseLoggingExpectedMisses(t *testing.T) {
	m := newLLMTestManager(t)
	if err := m.wdb.Db.AutoMigrate(&schema.ResourceAlertConfig{}, &schema.WebAgent{}, &schema.HymatrixPod{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	m.wdb.Db = m.wdb.Db.Session(&gorm.Session{Logger: managerDatabaseLogger(stdlog.New(&output, "", 0))})
	// An unsaved alert configuration is a normal first-run state.
	cfg, err := m.readResourceAlerts(context.Background())
	if err != nil || len(cfg.Rules) != 2 {
		t.Fatal("default configuration failed", err)
	}
	// An empty durable queue is the normal idle worker state.
	if err = m.runCommerceJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Suppression must not change the error used by callers to branch on a miss.
	var key schema.LLMKey
	if err = m.wdb.Db.First(&key, "hub_access_key_id = ?", "not-created").Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("lookup semantics changed", err)
	}
	if output.Len() != 0 {
		t.Fatalf("expected misses logged as errors: %s", output.String())
	}
	// Actual database errors must still be logged and returned.
	if err = m.wdb.Db.Exec("SELECT * FROM intentionally_missing_logging_test_table").Error; err == nil {
		t.Fatal("missing-table query should fail")
	}
	if !strings.Contains(output.String(), "intentionally_missing_logging_test_table") {
		t.Fatal("real database error was hidden")
	}
}
