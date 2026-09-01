package resouces

import (
	"fmt"

	"github.com/vertrai/hub/resouces/schema"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Wdb struct{ Db *gorm.DB }

func NewWdb(dsn string) (*Wdb, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres dsn is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Error), CreateBatchSize: 3000,
	})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	w := &Wdb{Db: db}
	if err := w.migrate(); err != nil {
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return w, nil
}

func (w *Wdb) migrate() error {
	return w.Db.AutoMigrate(&schema.AccessKey{}, &schema.Browser{}, &schema.GoogleAccount{}, &schema.TelegramBot{}, &schema.TelegramAccount{})
}

func (w *Wdb) Close() error {
	sqlDB, err := w.Db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
