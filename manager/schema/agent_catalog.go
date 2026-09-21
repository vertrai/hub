package schema

import "time"

// ID is the stable business identity, retained in MiniProgramAgentTask.Template.
// Module is only returned to administrators, never to mini-program clients.
type AgentCatalogEntry struct {
	Wechat            *WechatCatalogCopy `gorm:"serializer:json;type:jsonb" json:"wechat,omitempty"`
	Web               *WebCatalogConfig  `gorm:"serializer:json;type:jsonb" json:"web,omitempty"`
	ProductID         string             `gorm:"size:64;not null;default:'';uniqueIndex:idx_manager_catalog_product,where:product_id <> ''" json:"productId"`
	StripePriceID     string             `gorm:"size:255" json:"stripePriceId"`
	ID                string             `gorm:"primaryKey;size:64" json:"id"`
	Name              string             `gorm:"size:80;not null" json:"name"`
	LogoURL           string             `json:"logoUrl"`
	Kicker            string             `json:"kicker"`
	Intro             string             `json:"intro"`
	Summary           string             `json:"summary"`
	Capabilities      []string           `gorm:"serializer:json;type:jsonb" json:"capabilities"`
	LoginCopy         string             `json:"loginCopy"`
	CapabilityCopy    string             `json:"capabilityCopy"`
	CreationDetail    string             `json:"creationDetail"`
	CTALabel          string             `json:"ctaLabel"`
	CompatibilityNote string             `json:"compatibilityNote"`
	Module            string             `json:"module"`
	Published         bool               `json:"published"`
	SortOrder         int                `json:"sortOrder"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

func (AgentCatalogEntry) TableName() string { return "manager_agent_catalog" }

// Catalog images are durable Manager-owned assets, independent of the catalog lifecycle.
type AgentCatalogImage struct {
	ID          string `gorm:"primaryKey;size:64"`
	ContentType string
	Data        []byte
	CreatedAt   time.Time
}

func (AgentCatalogImage) TableName() string { return "manager_agent_catalog_images" }

// Web settings are independent of the established WeChat publication contract.
// Nil preserves legacy commerce behavior until an administrator configures the web entry.
type WebCatalogConfig struct {
	SubscriptionEnabled *bool                     `json:"subscriptionEnabled,omitempty"`
	Published           bool                      `json:"published"`
	SortOrder           int                       `json:"sortOrder"`
	InviteEnabled       bool                      `json:"inviteEnabled"`
	Content             map[string]WebCatalogCopy `json:"content"`
}
type WebCatalogCopy struct {
	Name              string   `json:"name"`
	Intro             string   `json:"intro"`
	Summary           string   `json:"summary"`
	Capabilities      []string `json:"capabilities"`
	CompatibilityNote string   `json:"compatibilityNote"`
	ConsentText       string   `json:"consentText"`
}

// WechatCatalogCopy overrides shared defaults without changing the public mini-program contract.
// Nil retains the existing catalog content until its first scoped edit.
type WechatCatalogCopy struct {
	Name              string   `json:"name"`
	LogoURL           string   `json:"logoUrl"`
	Intro             string   `json:"intro"`
	Summary           string   `json:"summary"`
	Capabilities      []string `json:"capabilities"`
	CompatibilityNote string   `json:"compatibilityNote"`
}
