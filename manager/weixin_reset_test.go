package manager

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type resetSenderFunc func() (string, string, error)

func (f resetSenderFunc) ResetWeixin(context.Context, string, WeixinResetInput) (string, string, error) {
	return f()
}

// Run against an isolated PostgreSQL database with HUB_TEST_POSTGRES_DSN set.
func resetFixture(t *testing.T) (*Manager, schema.HymatrixPod, schema.WeixinBot) {
	t.Helper()
	dsn := os.Getenv("HUB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set HUB_TEST_POSTGRES_DSN to an isolated PostgreSQL test database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&schema.HymatrixPod{}, &schema.WeixinBot{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	pod := schema.HymatrixPod{ID: "pod-test", UserID: "user-test", Status: "running", RuntimeType: "hermes", PID: "pid-test", WeixinBotID: "old"}
	old := schema.WeixinBot{ID: "old", UserID: pod.UserID, AccountID: "account-old", Status: "assigned", AssignedPodID: &pod.ID}
	next := schema.WeixinBot{ID: "next", UserID: pod.UserID, AccountID: "account-next", Status: "available"}
	for _, record := range []any{&pod, &old, &next} {
		if err := tx.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{wdb: &Wdb{Db: tx}}, pod, next
}

func submitReset(t *testing.T, m *Manager, pod schema.HymatrixPod, bot schema.WeixinBot, send resetSenderFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("POST", "/reset", nil)
	m.submitPodWeixinReset(c, pod, bot, send)
	return r
}

func TestWeixinResetDeliveryCompletesAndAllowsSecondReset(t *testing.T) {
	m, pod, next := resetFixture(t)
	send := resetSenderFunc(func() (string, string, error) { return "tx-delivered", "", nil })
	for i := 0; i < 2; i++ {
		oldID := pod.WeixinBotID
		r := submitReset(t, m, pod, next, send)
		if r.Code != 202 {
			t.Fatalf("response: %d %s", r.Code, r.Body.String())
		}
		if err := m.wdb.Db.First(&pod, "id = ?", pod.ID).Error; err != nil {
			t.Fatal(err)
		}
		if pod.WeixinResetPending || pod.WeixinBotID != next.ID {
			t.Fatalf("binding not completed: pending=%v bot=%s", pod.WeixinResetPending, pod.WeixinBotID)
		}
		var old, active schema.WeixinBot
		if err := m.wdb.Db.First(&old, "id = ?", oldID).Error; err != nil {
			t.Fatal(err)
		}
		if err := m.wdb.Db.First(&active, "id = ?", next.ID).Error; err != nil {
			t.Fatal(err)
		}
		if old.Status != "retired" || old.AssignedPodID != nil || old.ResetPodID != nil {
			t.Fatal("old identity still reserved")
		}
		if active.Status != "assigned" || active.AssignedPodID == nil || *active.AssignedPodID != pod.ID || active.ResetPodID != nil {
			t.Fatal("new identity not assigned")
		}
		if i == 0 {
			next = schema.WeixinBot{ID: "third", UserID: pod.UserID, AccountID: "account-third", Status: "available"}
			if err := m.wdb.Db.Create(&next).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWeixinResetUncertainDeliveryKeepsGuard(t *testing.T) {
	for _, kind := range []string{"timeout", "missing-id"} {
		t.Run(kind, func(t *testing.T) {
			m, pod, next := resetFixture(t)
			calls := 0
			send := resetSenderFunc(func() (string, string, error) {
				calls++
				if kind == "timeout" {
					return "", "", errors.New("timeout")
				}
				return "", "", nil
			})
			r := submitReset(t, m, pod, next, send)
			if r.Code != 502 {
				t.Fatalf("status=%d", r.Code)
			}
			if err := m.wdb.Db.First(&pod, "id = ?", pod.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !pod.WeixinResetPending || pod.WeixinBotID != "old" {
				t.Fatal("uncertain delivery lost old binding or guard")
			}
			r = submitReset(t, m, pod, next, send)
			if r.Code != 409 || calls != 1 {
				t.Fatal("uncertain transaction sent twice")
			}
		})
	}
}

func TestWeixinResetCompletionRollsBackAssignmentFailure(t *testing.T) {
	m, pod, next := resetFixture(t)
	// Inject a database failure after delivery; all assignment updates must roll back.
	if err := m.wdb.Db.Exec("ALTER TABLE manager_weixin_bots ADD CONSTRAINT reject_promotion CHECK (id <> 'next' OR status <> 'assigned')").Error; err != nil {
		t.Fatal(err)
	}
	r := submitReset(t, m, pod, next, resetSenderFunc(func() (string, string, error) { return "tx-ok", "", nil }))
	if r.Code != 409 {
		t.Fatalf("status=%d", r.Code)
	}
	if err := m.wdb.Db.First(&pod, "id = ?", pod.ID).Error; err != nil {
		t.Fatal(err)
	}
	var old schema.WeixinBot
	if err := m.wdb.Db.First(&old, "id = ?", "old").Error; err != nil {
		t.Fatal(err)
	}
	if !pod.WeixinResetPending || pod.WeixinBotID != "old" || old.Status != "assigned" || old.AssignedPodID == nil {
		t.Fatal("partial assignment escaped failed transaction")
	}
}
