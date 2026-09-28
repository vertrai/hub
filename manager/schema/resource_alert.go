package schema

import "time"

// ResourceAlertConfig is an admin-only singleton. Credentials never appear in API responses.
type ResourceAlertConfig struct {
	ID        uint   `gorm:"primaryKey"`
	Config    string `gorm:"type:text;not null"`
	UpdatedAt time.Time
}

func (ResourceAlertConfig) TableName() string { return "manager_resource_alert_config" }

// One durable delivery state per pool and channel, including a cross-process lease.
type ResourceAlertDelivery struct {
	ID          string     `gorm:"primaryKey;size:80" json:"id"`
	NextAttempt time.Time  `json:"nextAttempt"`
	LeaseUntil  time.Time  `json:"-"`
	LastSent    *time.Time `json:"lastSent,omitempty"`
	LastError   string     `json:"lastError"`
}

func (ResourceAlertDelivery) TableName() string { return "manager_resource_alert_deliveries" }
