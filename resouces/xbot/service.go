package xbot

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vertrai/hub/resouces/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) CreateFromGoogle(account schema.GoogleAccount) (schema.XBotAccount, error) {
	row := schema.XBotAccount{
		ID: "xbot_" + strings.ReplaceAll(uuid.NewString(), "-", ""), GoogleAccountID: account.ID,
		Email: account.Email, Password: account.Password, Status: schema.XBotStatusPendingRegistration,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&schema.GoogleAccount{}).Where("id = ? AND status = ?", account.ID, schema.StatusAvailable).Update("status", schema.StatusReservedXBot)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("google account is no longer available")
		}
		return tx.Create(&row).Error
	})
	return row, err
}

func (s *Service) List() ([]schema.XBotAccount, error) {
	var rows []schema.XBotAccount
	return rows, s.db.Order("created_at desc").Find(&rows).Error
}

func (s *Service) MarkRegistered(id, gamertag string) (schema.XBotAccount, error) {
	gamertag = strings.TrimSpace(gamertag)
	var row schema.XBotAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id).Error; err != nil {
			return err
		}
		if row.Status != schema.XBotStatusPendingRegistration && row.Status != schema.XBotStatusRegistered {
			return fmt.Errorf("only a pending XBot can be registered")
		}
		now := time.Now()
		if gamertag != "" {
			row.Gamertag = &gamertag
		}
		row.Status, row.RegisteredAt = schema.XBotStatusRegistered, &now
		return tx.Save(&row).Error
	})
	return row, err
}

func (s *Service) Acquire(accessKeyID string) (schema.XBotAccount, error) {
	var row schema.XBotAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&schema.AccessKey{}, "id = ?", accessKeyID).Error; err != nil {
			return err
		}
		err := tx.Where("assigned_access_key_id = ?", accessKeyID).First(&row).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status = ?", schema.XBotStatusRegistered).Order("registered_at, created_at").First(&row).Error; err != nil {
			return err
		}
		now := time.Now()
		row.Status, row.AssignedAccessKeyID, row.AssignedAt = schema.XBotStatusInUse, &accessKeyID, &now
		return tx.Save(&row).Error
	})
	return row, err
}
