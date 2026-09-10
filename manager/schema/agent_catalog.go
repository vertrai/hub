package schema

import "time"

// ID is the stable business identity, retained in MiniProgramAgentTask.Template.
// Module is only returned to administrators, never to mini-program clients.
type AgentCatalogEntry struct {
	ID                string    `gorm:"primaryKey;size:64" json:"id"`
	Name              string    `gorm:"size:80;not null" json:"name"`
	LogoURL           string    `json:"logoUrl"`
	Kicker            string    `json:"kicker"`
	Intro             string    `json:"intro"`
	Summary           string    `json:"summary"`
	Capabilities      []string  `gorm:"serializer:json;type:jsonb" json:"capabilities"`
	LoginCopy         string    `json:"loginCopy"`
	CapabilityCopy    string    `json:"capabilityCopy"`
	CreationDetail    string    `json:"creationDetail"`
	CTALabel          string    `json:"ctaLabel"`
	CompatibilityNote string    `json:"compatibilityNote"`
	Module            string    `json:"module"`
	Published         bool      `json:"published"`
	SortOrder         int       `json:"sortOrder"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

func (AgentCatalogEntry) TableName() string { return "manager_agent_catalog" }
