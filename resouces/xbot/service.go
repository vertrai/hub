package xbot

import (
	"fmt"
	"strings"

	"github.com/vertrai/hub/resouces/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) Designate(googleAccountID string) (schema.GoogleAccount, error) {
	var row schema.GoogleAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", strings.TrimSpace(googleAccountID)).Error; err != nil {
			return err
		}
		if row.Status != schema.StatusAvailable || row.Purpose != "" {
			return fmt.Errorf("Google user is not an untyped available account")
		}
		row.Purpose = schema.GooglePurposeXbox
		row.XboxStatus = schema.XboxStatusWaiting
		return tx.Save(&row).Error
	})
	return row, err
}

func (s *Service) MarkReady(googleAccountID string) (schema.GoogleAccount, error) {
	var row schema.GoogleAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", strings.TrimSpace(googleAccountID)).Error; err != nil {
			return err
		}
		if row.Purpose != schema.GooglePurposeXbox || row.Status != schema.StatusAvailable {
			return fmt.Errorf("only an unassigned Xbox Google user can be marked ready")
		}
		row.XboxStatus = schema.XboxStatusReady
		return tx.Save(&row).Error
	})
	return row, err
}

func (s *Service) List() ([]schema.GoogleAccount, error) {
	var rows []schema.GoogleAccount
	return rows, s.db.Where("purpose = ?", schema.GooglePurposeXbox).Order("created_at desc").Find(&rows).Error
}
