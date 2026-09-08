package schema

import "time"

// LLMRoute decouples public model IDs from provider/account identities.
type LLMRoute struct {
	ID            string    `gorm:"primaryKey;size:200" json:"id"`
	Name          string    `gorm:"size:200" json:"name"`
	ProviderID    string    `gorm:"size:80;not null;index" json:"providerId"`
	UpstreamModel string    `gorm:"size:200;not null" json:"upstreamModel"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (LLMRoute) TableName() string { return "manager_llm_routes" }

// Singleton settings only apply to future resource allocations.
type LLMResourceSettings struct {
	ID            string    `gorm:"primaryKey" json:"-"`
	BaseURL       string    `json:"baseUrl"`
	AllowedModels string    `gorm:"type:text" json:"-"`
	DefaultModel  string    `json:"defaultModel"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (LLMResourceSettings) TableName() string { return "manager_llm_resource_settings" }
