// Package schema defines resources server persistence models.
package schema

import "time"

const (
	StatusActive         = "active"
	StatusAvailable      = "available"
	StatusAssigned       = "assigned"
	StatusRevoked        = "revoked"
	GooglePurposeGeneral = "general"
	GooglePurposeXbox    = "xbox"
)

type AccessKey struct {
	ID            string     `gorm:"primaryKey;size:80" json:"id"`
	OwnerUserID   string     `gorm:"size:80;not null;index" json:"ownerUserId"`
	KeyHash       string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	KeyPrefix     string     `gorm:"size:16;not null" json:"keyPrefix"`
	Status        string     `gorm:"size:24;not null;index" json:"status"`
	AllowGoogle   bool       `gorm:"not null;default:true" json:"allowGoogle"`
	AllowBrowser  bool       `gorm:"not null;default:true" json:"allowBrowser"`
	AllowTelegram bool       `gorm:"not null;default:true" json:"allowTelegram"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type Browser struct {
	ID                string     `gorm:"primaryKey;size:80" json:"id"`
	AccessKeyID       string     `gorm:"size:80;not null;uniqueIndex" json:"accessKeyId"`
	ProviderBrowserID string     `gorm:"size:160" json:"providerBrowserId,omitempty"`
	ProviderProfileID string     `gorm:"size:160" json:"providerProfileId,omitempty"`
	ProfileName       string     `gorm:"size:200" json:"profileName,omitempty"`
	CDPURL            string     `gorm:"type:text" json:"cdpUrl,omitempty"`
	LiveURL           string     `gorm:"type:text" json:"liveUrl,omitempty"`
	ProxyCountryCode  string     `gorm:"size:12" json:"proxyCountryCode,omitempty"`
	TimeoutMinutes    int        `json:"timeoutMinutes,omitempty"`
	Status            string     `gorm:"size:24;not null;index" json:"status"`
	ProviderStartedAt *time.Time `json:"startedAt,omitempty"`
	ProviderTimeoutAt *time.Time `json:"timeoutAt,omitempty"`
	ProviderCheckedAt *time.Time `json:"providerCheckedAt,omitempty"`
	LastUsedAt        *time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type GoogleAccount struct {
	ID                  string     `gorm:"primaryKey;size:80" json:"id"`
	Email               string     `gorm:"size:320;not null;uniqueIndex" json:"email"`
	Password            string     `gorm:"type:text;not null" json:"password"`
	GoogleUserID        string     `gorm:"size:160;uniqueIndex" json:"googleUserId"`
	Status              string     `gorm:"size:24;not null;index" json:"status"`
	Purpose             string     `gorm:"size:32;not null;default:'';index" json:"purpose,omitempty"`
	AssignedAccessKeyID *string    `gorm:"size:80;uniqueIndex" json:"assignedAccessKeyId,omitempty"`
	AssignedAt          *time.Time `json:"assignedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// TelegramBot is a bot token from the administrator-managed resource pool.
// BotToken remains readable because an assigned agent must be able to retrieve it repeatedly.
type TelegramBot struct {
	ID                  string     `gorm:"primaryKey;size:80" json:"id"`
	BotToken            string     `gorm:"type:text;not null;uniqueIndex" json:"botToken"`
	Username            string     `gorm:"size:160;not null;uniqueIndex" json:"username"`
	CreatedByAccount    string     `gorm:"size:160" json:"createdByAccount,omitempty"`
	Status              string     `gorm:"size:24;not null;index" json:"status"`
	AssignedAccessKeyID *string    `gorm:"size:80;uniqueIndex" json:"assignedAccessKeyId,omitempty"`
	AssignedAt          *time.Time `json:"assignedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// TelegramAccount stores metadata for a Telegram user account authorized to
// create bots. The MTProto session itself remains in the configured data dir.
type TelegramAccount struct {
	ID            string    `gorm:"primaryKey;size:80" json:"accountId"`
	Phone         string    `gorm:"size:40;not null;uniqueIndex" json:"phone"`
	APIID         int       `gorm:"not null" json:"apiId"`
	APIHash       string    `gorm:"type:text;not null" json:"-"`
	PhoneCodeHash string    `gorm:"type:text" json:"-"`
	Status        string    `gorm:"size:24;not null;index" json:"status"`
	Username      string    `gorm:"size:160" json:"username,omitempty"`
	CooldownUntil time.Time `json:"cooldownUntil,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
