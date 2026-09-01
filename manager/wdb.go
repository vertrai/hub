package manager

import (
	"fmt"

	"github.com/vertrai/hub/manager/schema"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Wdb struct{ Db *gorm.DB }

func NewWdb(dsn string) (*Wdb, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres dsn is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Error), CreateBatchSize: 3000})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	w := &Wdb{Db: db}
	if err := w.renameLegacyTables(); err != nil {
		return nil, err
	}
	if err := w.Db.AutoMigrate(&schema.User{}, &schema.AccessKey{}, &schema.HymatrixPod{}, &schema.WeixinBot{}, &schema.MiniProgramAgentTask{}); err != nil {
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	// AccessKeyID identifies both current and historical Pod attempts. The
	// AccessKey.AssignedPodID unique index enforces the single active assignment;
	// keeping AccessKeyID unique would prevent retrying after a failed Spawn.
	const legacyPodAccessKeyIndex = "idx_manager_hymatrix_pods_access_key_id"
	if w.Db.Migrator().HasIndex(&schema.HymatrixPod{}, legacyPodAccessKeyIndex) {
		if err := w.Db.Migrator().DropIndex(&schema.HymatrixPod{}, legacyPodAccessKeyIndex); err != nil {
			return nil, fmt.Errorf("drop legacy unique pod access-key index: %w", err)
		}
	}
	return w, nil
}

func (w *Wdb) renameLegacyTables() error {
	migrations := []struct {
		legacy string
		final  string
	}{
		{legacy: "users", final: "manager_users"},
		{legacy: "hymatix_pods", final: "manager_hymatrix_pods"},
		{legacy: "hymatrix_pods", final: "manager_hymatrix_pods"},
	}
	for _, migration := range migrations {
		if !w.Db.Migrator().HasTable(migration.legacy) || w.Db.Migrator().HasTable(migration.final) {
			continue
		}
		if err := w.Db.Migrator().RenameTable(migration.legacy, migration.final); err != nil {
			return fmt.Errorf("rename legacy manager table %s to %s: %w", migration.legacy, migration.final, err)
		}
	}
	return nil
}

func (w *Wdb) Close() error {
	db, err := w.Db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
