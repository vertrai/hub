package schema

import "time"

// XboxChild is permanently allocated to at most one Hub Access Key.
type XboxChild struct {
	ID             string     `gorm:"primaryKey;size:80" json:"id"`
	Email          string     `gorm:"size:254;not null;uniqueIndex" json:"email"`
	Password       string     `gorm:"type:text;not null" json:"-"`
	ParentEmail    string     `gorm:"size:254;not null" json:"parentEmail"`
	HubAccessKeyID *string    `gorm:"size:80;uniqueIndex" json:"hubAccessKeyId,omitempty"`
	UsedAt         *time.Time `json:"usedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func (XboxChild) TableName() string { return "manager_xbox_children" }
