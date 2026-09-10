package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/manager/llm"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

type llmOAuthSession struct {
	Owner, ProviderID, Name string
	Models                  []string
	Reauthorize             bool
	ExpectedAccountID       string
	Device                  llm.DeviceAuthorization
	Grant                   llm.DeviceGrant
	Credential              []byte
	ExpiresAt, NextPoll     time.Time
	InFlight                bool
}

func (m *Manager) startLLMOAuth(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	var input struct {
		ProviderID  string   `json:"providerId"`
		Name        string   `json:"name"`
		Models      []string `json:"models"`
		Reauthorize bool     `json:"reauthorize"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "有效的 Provider ID 是必填项"})
		return
	}
	if input.ProviderID == "" && !input.Reauthorize {
		input.ProviderID = newLLMProviderID("", "openai-codex")
	}
	if !llmProviderID.MatchString(input.ProviderID) {
		c.JSON(400, gin.H{"error": "重新授权需要有效的账号标识"})
		return
	}
	models := input.Models
	if models == nil {
		models = append([]string(nil), codexModelPreset.Models...)
	}
	var err error
	models, err = normalizeLLMModels(models)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	s := &llmOAuthSession{Owner: c.GetString("adminEmail"), ProviderID: input.ProviderID, Name: strings.TrimSpace(input.Name), Models: models, Reauthorize: input.Reauthorize}
	if len(s.Name) > 200 {
		c.JSON(400, gin.H{"error": "名称不能超过 200 字符"})
		return
	}
	if s.Name == "" {
		s.Name = s.ProviderID
	}
	var previous schema.LLMProvider
	err = m.wdb.Db.First(&previous, "id = ?", s.ProviderID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(500, gin.H{"error": "账号查询失败"})
		return
	}
	if input.Reauthorize {
		if err != nil || previous.Kind != "openai-codex" {
			c.JSON(409, gin.H{"error": "只能重新授权已存在的 Codex 账号"})
			return
		}
		s.ExpectedAccountID, _, _ = llm.CredentialMetadata(previous.Credential)
	} else if err == nil {
		c.JSON(409, gin.H{"error": "Provider ID 已存在，请使用新 ID 或选择重新授权"})
		return
	}
	m.llmOAuthMu.Lock()
	m.pruneLLMOAuthLocked(time.Now())
	if len(m.llmOAuthSessions) >= 128 {
		m.llmOAuthMu.Unlock()
		c.JSON(429, gin.H{"error": "授权会话过多，请稍后重试"})
		return
	}
	state := uuid.NewString()
	s.InFlight = true
	s.ExpiresAt = time.Now().Add(15 * time.Minute)
	m.llmOAuthSessions[state] = s
	m.llmOAuthMu.Unlock()
	device, err := m.codexOAuth.Start(c.Request.Context())
	m.llmOAuthMu.Lock()
	if err != nil {
		delete(m.llmOAuthSessions, state)
		m.llmOAuthMu.Unlock()
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	s.Device = device
	s.InFlight = false
	s.ExpiresAt = time.Now().Add(device.ExpiresIn)
	s.NextPoll = time.Now().Add(device.Interval)
	m.llmOAuthMu.Unlock()
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"providerId": s.ProviderID, "state": state, "userCode": device.UserCode, "verificationUrl": llm.CodexDeviceVerificationURL, "expiresAt": s.ExpiresAt, "interval": int(device.Interval.Seconds())})
}
func (m *Manager) pruneLLMOAuthLocked(now time.Time) {
	for id, s := range m.llmOAuthSessions {
		if !s.InFlight && now.After(s.ExpiresAt) {
			delete(m.llmOAuthSessions, id)
		}
	}
}
func (m *Manager) completeLLMOAuth(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	var input struct {
		State string `json:"state"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if c.ShouldBindJSON(&input) != nil || input.State == "" {
		c.JSON(400, gin.H{"error": "state 必填"})
		return
	}
	m.llmOAuthMu.Lock()
	m.pruneLLMOAuthLocked(time.Now())
	s, ok := m.llmOAuthSessions[input.State]
	if !ok || s.Owner != c.GetString("adminEmail") {
		m.llmOAuthMu.Unlock()
		c.JSON(404, gin.H{"error": "授权会话不存在或已过期，请重新生成授权码"})
		return
	}
	if s.InFlight {
		m.llmOAuthMu.Unlock()
		c.JSON(409, gin.H{"error": "正在完成授权，请稍候"})
		return
	}
	if time.Now().Before(s.NextPoll) && len(s.Credential) == 0 && s.Grant.Code == "" {
		wait := max(1, int(time.Until(s.NextPoll).Seconds())+1)
		m.llmOAuthMu.Unlock()
		c.JSON(202, gin.H{"status": "authorization_pending", "retryAfter": wait})
		return
	}
	s.InFlight = true
	m.llmOAuthMu.Unlock()
	defer func() { m.llmOAuthMu.Lock(); s.InFlight = false; m.llmOAuthMu.Unlock() }()
	c.Header("Cache-Control", "no-store")
	if len(s.Credential) == 0 {
		if s.Grant.Code == "" {
			grant, err := m.codexOAuth.Poll(c.Request.Context(), s.Device.DeviceAuthID, s.Device.UserCode)
			if errors.Is(err, llm.ErrAuthorizationPending) || errors.Is(err, llm.ErrOAuthSlowDown) {
				if errors.Is(err, llm.ErrOAuthSlowDown) {
					s.Device.Interval = min(time.Minute, s.Device.Interval+5*time.Second)
				}
				s.NextPoll = time.Now().Add(s.Device.Interval)
				c.JSON(202, gin.H{"status": "authorization_pending", "retryAfter": int(s.Device.Interval.Seconds())})
				return
			}
			if err != nil {
				c.JSON(502, gin.H{"error": err.Error()})
				return
			}
			s.Grant = grant
		}
		credential, err := m.codexOAuth.Exchange(c.Request.Context(), s.Grant)
		if err != nil {
			c.JSON(502, gin.H{"error": err.Error()})
			return
		}
		// Retain exchanged credentials until persistence succeeds; a transient DB
		// failure must not consume the one-use grant a second time on retry.
		s.Credential = credential
	}
	accountID, _, _ := llm.CredentialMetadata(s.Credential)
	if s.Reauthorize && accountID != s.ExpectedAccountID {
		c.JSON(409, gin.H{"error": "授权账号与原账号不同，请取消后登录原账号，或以新 Provider ID 添加"})
		return
	}
	m.llmMu.Lock()
	err := m.persistLLMOAuth(s)
	m.llmMu.Unlock()
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	m.llmOAuthMu.Lock()
	delete(m.llmOAuthSessions, input.State)
	m.llmOAuthMu.Unlock()
	c.JSON(200, gin.H{"status": "connected", "providerId": s.ProviderID, "name": s.Name})
}
func (m *Manager) persistLLMOAuth(s *llmOAuthSession) error {
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if s.Reauthorize {
			var existing schema.LLMProvider
			if err := tx.First(&existing, "id = ? AND kind = ?", s.ProviderID, "openai-codex").Error; err != nil {
				return errors.New("原 Codex 账号已删除或不可用，请重新添加")
			}
			accountID, _, _ := llm.CredentialMetadata(existing.Credential)
			if accountID != s.ExpectedAccountID {
				return errors.New("账号已变更，请重新授权")
			}
			// Preserve edits to models, enabled state, name and base URL made while the
			// administrator was authorizing. Only replace credentials.
			var credential llm.CodexCredential
			_ = json.Unmarshal(s.Credential, &credential)
			credential.BaseURL = existing.BaseURL
			raw, _ := json.Marshal(credential)
			if err := tx.Model(&existing).Update("credential", raw).Error; err != nil {
				return errors.New("凭据保存失败，可再次点击完成连接重试")
			}
		} else {
			var count int64
			if err := tx.Model(&schema.LLMProvider{}).Where("id = ?", s.ProviderID).Count(&count).Error; err != nil {
				return errors.New("账号查询失败，可重试")
			}
			if count > 0 {
				return errors.New("Provider ID 已存在，请取消后使用新 ID 授权")
			}
			models, _ := json.Marshal(s.Models)
			p := schema.LLMProvider{ID: s.ProviderID, Name: s.Name, Kind: "openai-codex", BaseURL: "https://chatgpt.com/backend-api/codex", Models: string(models), Credential: s.Credential, Enabled: true}
			if err := tx.Create(&p).Error; err != nil {
				return errors.New("账号保存失败，可再次点击完成连接重试")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.codex.DiscardCredential(s.ProviderID)
	return nil
}
func (m *Manager) cancelLLMOAuth(c *gin.Context) {
	m.llmOAuthMu.Lock()
	defer m.llmOAuthMu.Unlock()
	s, ok := m.llmOAuthSessions[c.Param("state")]
	if !ok || s.Owner != c.GetString("adminEmail") {
		c.JSON(404, gin.H{"error": "授权会话不存在"})
		return
	}
	if s.InFlight {
		c.JSON(409, gin.H{"error": "授权请求正在处理，请稍后取消"})
		return
	}
	delete(m.llmOAuthSessions, c.Param("state"))
	c.JSON(200, gin.H{"cancelled": true})
}
