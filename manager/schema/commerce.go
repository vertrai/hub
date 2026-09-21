package schema

import "time"

type InviteCode struct {
	ClaimedAt *time.Time `json:"claimedAt,omitempty"`
	Code      string     `gorm:"primaryKey;size:32" json:"code"`
	Product   string     `gorm:"size:64" json:"product,omitempty"`
	Note      string     `gorm:"size:500" json:"note,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	UsedAt    *time.Time `json:"usedAt,omitempty"`
	UsedBy    string     `gorm:"size:255;index" json:"usedBy,omitempty"`
	AgentID   string     `gorm:"size:80" json:"agentId,omitempty"`
}

func (InviteCode) TableName() string { return "manager_invite_codes" }

// WebAgent is the durable commercial instance, separate from its current Pod.
// Desire changes with subscription entitlement; State tracks actual deployment.
type WebAgent struct {
	WeixinAuthorizedBotID string     `json:"-"`
	ChannelConfigured     bool       `json:"-"`
	EnableTelegram        bool       `json:"-"`
	ID                    string     `gorm:"primaryKey;size:80" json:"agentId"`
	UserID                string     `gorm:"size:255;index" json:"-"`
	Product               string     `gorm:"size:64" json:"product"`
	CatalogID             string     `gorm:"size:64" json:"-"`
	Module                string     `json:"-"`
	Source                string     `gorm:"size:160;uniqueIndex" json:"-"`
	InviteCode            string     `json:"inviteCode,omitempty"`
	PodID                 string     `gorm:"size:80" json:"-"`
	AccessKeyID           string     `gorm:"size:80" json:"-"`
	BotUsername           string     `json:"botUsername,omitempty"`
	State                 string     `gorm:"size:32;index" json:"status"`
	Desired               string     `gorm:"size:32;index" json:"-"`
	Phase                 string     `gorm:"size:32" json:"-"`
	Error                 string     `json:"-"`
	LeaseUntil            *time.Time `json:"-"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

func (WebAgent) TableName() string { return "manager_web_agents" }

type Billing struct {
	ID                 string    `gorm:"primaryKey;size:80" json:"id"`
	UserID             string    `gorm:"size:255;index" json:"-"`
	Product            string    `gorm:"size:64" json:"product"`
	CatalogID          string    `json:"-"`
	Module             string    `json:"-"`
	Status             string    `json:"status"`
	PriceID            string    `json:"-"`
	CheckoutSessionID  *string   `gorm:"size:255;uniqueIndex" json:"checkoutSessionId"`
	SubscriptionID     *string   `gorm:"size:255;uniqueIndex" json:"subscriptionId"`
	CustomerID         string    `gorm:"size:255;index" json:"-"`
	CheckoutURL        string    `json:"-"`
	AgentID            string    `gorm:"size:80" json:"agentId"`
	CurrentPeriodStart time.Time `json:"currentPeriodStart"`
	CurrentPeriodEnd   time.Time `json:"currentPeriodEnd"`
	CancelAtPeriodEnd  bool      `json:"cancelAtPeriodEnd"`
	InvoiceURL         string    `json:"invoiceUrl"`
	InvoicePDF         string    `json:"invoicePdf"`
	LastEventCreated   int64     `json:"-"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

func (Billing) TableName() string { return "manager_billing" }

type StripeEvent struct {
	ID        string `gorm:"primaryKey;size:255"`
	Type      string
	CreatedAt time.Time
}

func (StripeEvent) TableName() string { return "manager_stripe_events" }

// StripeSettings stores only encrypted configuration; secrets never appear in API responses.
type StripeSettings struct {
	ID              string    `gorm:"primaryKey;size:32" json:"-"`
	EncryptedConfig []byte    `gorm:"not null" json:"-"`
	UpdatedAt       time.Time `json:"-"`
}

func (StripeSettings) TableName() string { return "manager_stripe_settings" }
