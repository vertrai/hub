package schema

import "time"

// LLMProvider credentials are never serialized to administrative responses.
type LLMProvider struct {
	ID         string    `gorm:"primaryKey;size:80" json:"id"`
	Name       string    `gorm:"size:200" json:"name"`
	Preset     string    `gorm:"size:40" json:"preset"`
	Kind       string    `gorm:"size:40;not null" json:"kind"`
	BaseURL    string    `json:"baseUrl"`
	Models     string    `gorm:"type:text;not null" json:"-"`
	Credential []byte    `gorm:"not null" json:"-"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (LLMProvider) TableName() string { return "manager_llm_providers" }

type LLMKey struct {
	HubAccessKeyID *string   `gorm:"size:80;uniqueIndex" json:"hubAccessKeyId,omitempty"`
	OwnerUserID    string    `gorm:"size:80;index" json:"ownerUserId,omitempty"`
	Secret         string    `gorm:"type:text" json:"-"`
	AllowedModels  string    `gorm:"type:text" json:"-"`
	DefaultModel   string    `gorm:"size:200" json:"defaultModel"`
	Revoked        bool      `gorm:"not null;default:false" json:"revoked"`
	UpdatedAt      time.Time `json:"updatedAt"`
	ID             string    `gorm:"primaryKey;size:80" json:"id"`
	Name           string    `json:"name"`
	Hash           string    `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Prefix         string    `json:"prefix"`
	CreatedAt      time.Time `json:"createdAt"`
}

func (LLMKey) TableName() string { return "manager_llm_keys" }
