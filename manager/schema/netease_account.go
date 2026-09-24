package schema

import "time"

// NetEaseAccount is permanently allocated to at most one Hub Access Key.
type NetEaseAccount struct {
	ID             string     `gorm:"primaryKey;size:80" json:"id"`
	Username       string     `gorm:"size:254;not null;uniqueIndex" json:"username"`
	Password       string     `gorm:"type:text;not null" json:"-"`
	HubAccessKeyID *string    `gorm:"size:80;uniqueIndex" json:"hubAccessKeyId,omitempty"`
	UsedAt         *time.Time `json:"usedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func (NetEaseAccount) TableName() string { return "manager_netease_accounts" }
