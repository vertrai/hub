package schema

import "time"

const (
	PodStatusSpawning = "spawning"
	PodStatusSpawned  = "spawned"
	PodStatusStarting = "starting"
	PodStatusRunning  = "running"
	PodStatusFailed   = "failed"
)

const (
	MiniProgramTaskSpawning         = "SPAWNING"
	MiniProgramTaskWaitingForWeixin = "WAITING_FOR_WECHAT"
	MiniProgramTaskRefreshingQR     = "REFRESHING_WECHAT_QR"
	MiniProgramTaskStartingAgent    = "STARTING_AGENT"
	MiniProgramTaskRunning          = "RUNNING"
	MiniProgramTaskQRExpired        = "QR_EXPIRED"
	MiniProgramTaskFailed           = "FAILED"
)

type User struct {
	ID        string    `gorm:"primaryKey;size:80" json:"id"`
	Name      string    `gorm:"size:200;not null" json:"name"`
	Status    string    `gorm:"size:24;not null;index" json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (User) TableName() string { return "manager_users" }

type AccessKey struct {
	ID            string    `gorm:"primaryKey;size:80" json:"id"`
	UserID        string    `gorm:"size:80;not null;index" json:"userId"`
	ResourceKeyID string    `gorm:"size:80;not null;uniqueIndex" json:"resourceKeyId"`
	KeyPrefix     string    `gorm:"size:16;not null" json:"keyPrefix"`
	Secret        string    `gorm:"type:text;not null" json:"-"`
	Status        string    `gorm:"size:24;not null;index" json:"status"`
	AssignedPodID *string   `gorm:"size:80;uniqueIndex" json:"assignedPodId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (AccessKey) TableName() string { return "manager_access_keys" }

type HymatrixPod struct {
	ID            string    `gorm:"primaryKey;size:80" json:"id"`
	UserID        string    `gorm:"size:80;not null;index" json:"userId"`
	Name          string    `gorm:"size:200;not null" json:"name"`
	RuntimeType   string    `gorm:"size:80;not null" json:"runtimeType"`
	PID           string    `gorm:"size:160;uniqueIndex" json:"pid"`
	Status        string    `gorm:"size:24;not null;index" json:"status"`
	NodeURL       string    `gorm:"type:text;not null" json:"nodeUrl"`
	AdminURL      string    `gorm:"type:text;not null;default:''" json:"adminUrl"`
	PrivateKey    string    `gorm:"type:text;not null" json:"-"`
	Module        string    `gorm:"type:text;not null" json:"module"`
	Scheduler     string    `gorm:"type:text;not null" json:"scheduler"`
	LLMAPIKey     string    `gorm:"type:text" json:"-"`
	LLMBaseURL    string    `gorm:"type:text" json:"llmBaseUrl,omitempty"`
	LLMModel      string    `gorm:"size:200" json:"llmModel,omitempty"`
	LLMProvider   string    `gorm:"size:80" json:"llmProvider,omitempty"`
	GatewayAPIKey string    `gorm:"type:text" json:"-"`
	AccessKeyID   string    `gorm:"size:80;not null;index:idx_manager_hymatrix_pods_access_key_history" json:"accessKeyId"`
	BotToken      string    `gorm:"type:text" json:"-"`
	WeixinBotID   string    `gorm:"size:80;index" json:"weixinBotId,omitempty"`
	Error         string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (HymatrixPod) TableName() string { return "manager_hymatrix_pods" }

const (
	WeixinBotStatusAvailable = "available"
	WeixinBotStatusAssigned  = "assigned"
)

// WeixinBot is a user-authorized iLink identity. Unlike pooled Telegram bots,
// it is created by a QR authorization and can be assigned to one Pod only.
type WeixinBot struct {
	ID            string    `gorm:"primaryKey;size:80" json:"id"`
	UserID        string    `gorm:"size:80;not null;index" json:"userId"`
	AccountID     string    `gorm:"size:200;not null;uniqueIndex" json:"accountId"`
	Token         string    `gorm:"type:text;not null" json:"-"`
	BaseURL       string    `gorm:"type:text;not null" json:"baseUrl"`
	AllowedUserID string    `gorm:"size:240;not null" json:"allowedUserId"`
	Status        string    `gorm:"size:24;not null;index" json:"status"`
	AssignedPodID *string   `gorm:"size:80;uniqueIndex" json:"assignedPodId,omitempty"`
	AuthorizedAt  time.Time `json:"authorizedAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (WeixinBot) TableName() string { return "manager_weixin_bots" }

// MiniProgramAgentTask is the public, token-protected view of Pod provisioning.
// Sensitive Pod, wallet and iLink credentials remain in their owning tables.
type MiniProgramAgentTask struct {
	ID              string    `gorm:"primaryKey;size:80" json:"taskId"`
	UserID          string    `gorm:"size:80;not null;index" json:"-"`
	PodID           string    `gorm:"size:80;index" json:"podId,omitempty"`
	WeixinAttemptID string    `gorm:"size:80" json:"-"`
	TokenHash       string    `gorm:"size:64;not null" json:"-"`
	Status          string    `gorm:"size:32;not null;index" json:"status"`
	QRCodeData      string    `gorm:"type:text" json:"qrCodeUrl,omitempty"`
	QRExpiresAt     time.Time `json:"qrExpiresAt,omitempty"`
	Error           string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (MiniProgramAgentTask) TableName() string { return "manager_mini_program_agent_tasks" }
