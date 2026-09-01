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

// Designate changes an existing unassigned Google user from the general pool
// into a purpose-specific Xbox user. Credentials remain stored only once.
func (s *Service) Designate(googleAccountID string) (schema.GoogleAccount, error) {
	var row schema.GoogleAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", strings.TrimSpace(googleAccountID)).Error; err != nil {
			return err
		}
		if row.Status != schema.StatusAvailable || row.Purpose != schema.GooglePurposeGeneral {
			return fmt.Errorf("google user is not available in the general pool")
		}
		row.Purpose = schema.GooglePurposeXbox
		return tx.Save(&row).Error
	})
	return row, err
}

func (s *Service) List() ([]schema.GoogleAccount, error) {
	var rows []schema.GoogleAccount
	return rows, s.db.Where("purpose = ?", schema.GooglePurposeXbox).Order("created_at desc").Find(&rows).Error
}

func (s *Service) Acquire(accessKeyID string) (schema.GoogleAccount, error) {
	var row schema.GoogleAccount
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&schema.AccessKey{}, "id = ?", accessKeyID).Error; err != nil {
			return err
		}
		var assignment schema.GoogleAccountAssignment
		err := tx.Where("access_key_id = ? AND purpose = ?", accessKeyID, schema.GooglePurposeXbox).First(&assignment).Error
		if err == nil {
			return tx.First(&row, "id = ?", assignment.GoogleAccountID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("purpose = ? AND status = ?", schema.GooglePurposeXbox, schema.StatusAvailable).Order("created_at").First(&row).Error; err != nil {
			return err
		}
		assignment = schema.GoogleAccountAssignment{ID: "guas_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], GoogleAccountID: row.ID, AccessKeyID: accessKeyID, Purpose: schema.GooglePurposeXbox, CreatedAt: time.Now()}
		if err := tx.Create(&assignment).Error; err != nil {
			return err
		}
		row.Status = schema.StatusAssigned
		row.AssignedAt = &assignment.CreatedAt
		return tx.Save(&row).Error
	})
	return row, err
}
