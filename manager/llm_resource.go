package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type llmResourceError struct {
	Status  int
	Message string
}

func (e *llmResourceError) Error() string { return e.Message }
func llmResourceFailure(c *gin.Context, err error) {
	status := 503
	var e *llmResourceError
	if errors.As(err, &e) {
		status = e.Status
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

type llmResourceConfig struct {
	APIKey       string            `json:"apiKey"`
	KeyID        string            `json:"keyId"`
	BaseURL      string            `json:"baseUrl"`
	Provider     string            `json:"provider"`
	Model        string            `json:"model"`
	DefaultModel string            `json:"defaultModel"`
	Models       []schema.LLMRoute `json:"models"`
}

func newLLMKey(name string, allowed []string, defaultModel string) (schema.LLMKey, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return schema.LLMKey{}, "", err
	}
	secret := "hub-llm-" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(secret))
	return schema.LLMKey{ID: uuid.NewString(), Name: name, Hash: hex.EncodeToString(hash[:]), Prefix: secret[:16], AllowedModels: llmEncodeList(allowed), DefaultModel: defaultModel}, secret, nil
}
func (m *Manager) llmSettings() (schema.LLMResourceSettings, error) {
	var settings schema.LLMResourceSettings
	err := m.wdb.Db.First(&settings, "id = ?", "default").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return schema.LLMResourceSettings{ID: "default", AllowedModels: "[]"}, nil
	}
	return settings, err
}
func (m *Manager) adminLLMResourceSettings(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	if c.Request.Method == http.MethodGet {
		s, err := m.llmSettings()
		if err != nil {
			llmResourceFailure(c, err)
			return
		}
		c.JSON(200, gin.H{"baseUrl": s.BaseURL, "allowedModels": llmStringList(s.AllowedModels), "defaultModel": s.DefaultModel})
		return
	}
	var input struct {
		BaseURL       *string   `json:"baseUrl"`
		AllowedModels *[]string `json:"allowedModels"`
		DefaultModel  *string   `json:"defaultModel"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "invalid settings"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	s, err := m.llmSettings()
	if err != nil {
		llmResourceFailure(c, errors.New("设置读取失败"))
		return
	}
	if input.BaseURL != nil {
		s.BaseURL = *input.BaseURL
	}
	if input.AllowedModels != nil {
		s.AllowedModels = llmEncodeList(*input.AllowedModels)
	}
	if input.DefaultModel != nil {
		s.DefaultModel = *input.DefaultModel
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(s.BaseURL), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		c.JSON(400, gin.H{"error": "请填写客户端可访问的完整 LLM Base URL（以 /llm/v1 结尾）"})
		return
	}
	if !strings.HasSuffix(u.Path, "/llm/v1") {
		c.JSON(400, gin.H{"error": "LLM Base URL 必须以 /llm/v1 结尾"})
		return
	}
	if input.AllowedModels != nil || input.DefaultModel != nil {
		if err := m.validateLLMPolicy(llmStringList(s.AllowedModels), s.DefaultModel); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
	}
	s.BaseURL = u.String()
	if err := m.wdb.Db.Save(&s).Error; err != nil {
		llmResourceFailure(c, errors.New("设置保存失败"))
		return
	}
	c.JSON(200, gin.H{"saved": true})
}
func (m *Manager) gatewayLLMIdentity(ctx context.Context, secret string) (ResourceAccessKey, error) {
	if strings.TrimSpace(secret) == "" {
		return ResourceAccessKey{}, &llmResourceError{401, "Hub API Key 必填"}
	}
	raw, status, err := m.resources.do(ctx, http.MethodGet, "/v1/access-key", nil, secret)
	if err != nil {
		return ResourceAccessKey{}, &llmResourceError{503, "Hub API Key 验证服务不可用"}
	}
	if status != 200 {
		if status == 401 || status == 403 {
			return ResourceAccessKey{}, &llmResourceError{status, "Hub API Key 无效或已停用"}
		}
		return ResourceAccessKey{}, &llmResourceError{503, "Hub API Key 验证失败，请检查 Resources 版本"}
	}
	var result struct {
		AccessKey ResourceAccessKey `json:"accessKey"`
	}
	if json.Unmarshal(raw, &result) != nil || result.AccessKey.ID == "" || result.AccessKey.Status != "active" {
		return ResourceAccessKey{}, &llmResourceError{401, "Hub API Key 无效"}
	}
	return result.AccessKey, nil
}
func (m *Manager) acquireLLMResource(ctx context.Context, hubSecret string) (llmResourceConfig, error) {
	if m.wdb == nil {
		return llmResourceConfig{}, errors.New("database unavailable")
	}
	identity, err := m.gatewayLLMIdentity(ctx, hubSecret)
	if err != nil {
		return llmResourceConfig{}, err
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	settings, err := m.llmSettings()
	if err != nil {
		return llmResourceConfig{}, errors.New("LLM 设置不可用")
	}
	if settings.BaseURL == "" {
		return llmResourceConfig{}, errors.New("请管理员先配置 LLM 资源的访问地址")
	}
	var key schema.LLMKey
	err = m.wdb.Db.First(&key, "hub_access_key_id = ?", identity.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		allowed := llmStringList(settings.AllowedModels)
		if len(allowed) == 0 {
			return llmResourceConfig{}, errors.New("自动申请模型未配置")
		}
		if err := m.validateLLMPolicy(allowed, settings.DefaultModel); err != nil {
			return llmResourceConfig{}, err
		}
		candidate, secret, keyErr := newLLMKey("Hub "+identity.ID, allowed, settings.DefaultModel)
		if keyErr != nil {
			return llmResourceConfig{}, errors.New("LLM Key 生成失败")
		}
		candidate.HubAccessKeyID = &identity.ID
		candidate.OwnerUserID = identity.OwnerUserID
		candidate.Secret = secret
		// Unique ownership protects against concurrent allocation across requests.
		if err := m.wdb.Db.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return llmResourceConfig{}, errors.New("LLM 资源分配失败")
		}
		err = m.wdb.Db.First(&key, "hub_access_key_id = ?", identity.ID).Error
	}
	if err != nil {
		return llmResourceConfig{}, errors.New("LLM 资源查询失败")
	}
	if key.Revoked {
		return llmResourceConfig{}, &llmResourceError{403, "LLM Key 已撤销，请联系管理员"}
	}
	if key.Secret == "" {
		return llmResourceConfig{}, errors.New("LLM 资源凭据不可用")
	}
	available, err := m.availableLLMModels()
	if err != nil {
		return llmResourceConfig{}, errors.New("模型列表不可用")
	}
	models := []schema.LLMRoute{}
	for _, model := range available {
		if keyCanUseLLM(key, model.ID) {
			models = append(models, model)
		}
	}
	return llmResourceConfig{APIKey: key.Secret, KeyID: key.ID, BaseURL: settings.BaseURL, Provider: "custom", Model: "hub-chat", DefaultModel: key.DefaultModel, Models: models}, nil
}
func keyCanUseLLM(key schema.LLMKey, id string) bool {
	return hasLLMModel(llmStringList(key.AllowedModels), id)
}
func (m *Manager) getLLMResource(c *gin.Context) {
	secret := c.GetHeader("X-Gateway-API-Key")
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		secret = parts[1]
	}
	resource, err := m.acquireLLMResource(c.Request.Context(), secret)
	if err != nil {
		llmResourceFailure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, resource)
}
func (m *Manager) adminAcquireLLMResource(c *gin.Context) {
	if m.wdb == nil {
		llmResourceFailure(c, errors.New("database unavailable"))
		return
	}
	var key schema.AccessKey
	if err := m.wdb.Db.First(&key, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(404, gin.H{"error": "Hub API Key 不存在"})
		return
	}
	resource, err := m.acquireLLMResource(c.Request.Context(), key.Secret)
	if err != nil {
		llmResourceFailure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, resource)
}

// Administrators (including an authorized upgrade backend) can update policy
// atomically. Holders cannot grant themselves more expensive model access.
func (m *Manager) updateLLMKeyPolicy(c *gin.Context) {
	if m.wdb == nil {
		llmResourceFailure(c, errors.New("database unavailable"))
		return
	}
	var input struct {
		AllowedModels []string `json:"allowedModels"`
		DefaultModel  string   `json:"defaultModel"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "invalid policy"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	if err := m.validateLLMPolicy(input.AllowedModels, input.DefaultModel); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var key schema.LLMKey
	if err := m.wdb.Db.First(&key, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(404, gin.H{"error": "LLM Key 不存在"})
		return
	}
	if key.Revoked {
		c.JSON(409, gin.H{"error": "LLM Key 已撤销"})
		return
	}
	if err := m.wdb.Db.Model(&key).Updates(map[string]any{"allowed_models": llmEncodeList(input.AllowedModels), "default_model": input.DefaultModel}).Error; err != nil {
		llmResourceFailure(c, errors.New("策略保存失败"))
		return
	}
	c.JSON(200, gin.H{"saved": true})
}

func (m *Manager) hermesLLMResource(ctx context.Context, secret, model string) (llmResourceConfig, error) {
	resource, err := m.acquireLLMResource(ctx, secret)
	if err != nil {
		return resource, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "hub-chat"
	}
	target := model
	if model == "hub-chat" {
		target = resource.DefaultModel
	}
	found := false
	for _, available := range resource.Models {
		if available.ID == target {
			found = true
		}
	}
	if !found {
		return resource, &llmResourceError{403, "所选模型不可用或不在此 LLM Key 的允许列表中"}
	}
	resource.Model = model
	return resource, nil
}

// adminManageBoundLLMResource never allocates on read. Manual creation uses the
// administrator's explicit policy, independent of automatic allocation settings.
func (m *Manager) adminManageBoundLLMResource(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	var input struct {
		UserID        string   `json:"userId"`
		AllowedModels []string `json:"allowedModels"`
		DefaultModel  string   `json:"defaultModel"`
	}
	if c.Request.Method == http.MethodPost {
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "invalid input"})
			return
		}
	} else {
		input.UserID = c.Query("userId")
	}
	var hubKey schema.AccessKey
	if input.UserID == "" || m.wdb.Db.First(&hubKey, "resource_key_id = ? AND user_id = ?", c.Param("id"), input.UserID).Error != nil {
		c.JSON(404, gin.H{"error": "该用户的 Hub Key 不存在"})
		return
	}
	identity, err := m.gatewayLLMIdentity(c.Request.Context(), hubKey.Secret)
	if err != nil {
		llmResourceFailure(c, err)
		return
	}
	if identity.ID != hubKey.ResourceKeyID || identity.OwnerUserID != input.UserID {
		c.JSON(403, gin.H{"error": "Hub Key 不属于所选用户"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	settings, err := m.llmSettings()
	if err != nil {
		c.JSON(503, gin.H{"error": "LLM 设置不可用"})
		return
	}
	var key schema.LLMKey
	err = m.wdb.Db.First(&key, "hub_access_key_id = ?", hubKey.ResourceKeyID).Error
	if c.Request.Method == http.MethodPost {
		if err == nil {
			c.JSON(409, gin.H{"error": "该 Hub Key 已创建 LLM Key，请查看或修改现有资源"})
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(503, gin.H{"error": "资源查询失败"})
			return
		}
		if settings.BaseURL == "" {
			c.JSON(400, gin.H{"error": "请先配置 LLM 对外 Base URL"})
			return
		}
		if len(input.AllowedModels) == 0 {
			c.JSON(400, gin.H{"error": "至少选择一个对外模型"})
			return
		}
		if err = m.validateLLMPolicy(input.AllowedModels, input.DefaultModel); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		var secret string
		key, secret, err = newLLMKey("Hub "+hubKey.ResourceKeyID, input.AllowedModels, input.DefaultModel)
		if err != nil {
			c.JSON(503, gin.H{"error": "密钥生成失败"})
			return
		}
		key.HubAccessKeyID = &hubKey.ResourceKeyID
		key.OwnerUserID = input.UserID
		key.Secret = secret
		result := m.wdb.Db.Clauses(clause.OnConflict{DoNothing: true}).Create(&key)
		if result.Error != nil {
			c.JSON(503, gin.H{"error": "资源创建失败"})
			return
		}
		if result.RowsAffected == 0 {
			c.JSON(409, gin.H{"error": "该 Hub Key 已创建 LLM Key"})
			return
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(200, gin.H{"exists": false})
		return
	} else if err != nil {
		c.JSON(503, gin.H{"error": "资源查询失败"})
		return
	}
	c.Header("Cache-Control", "no-store")
	secret := key.Secret
	if key.Revoked {
		secret = ""
	}
	c.JSON(200, gin.H{"exists": true, "keyId": key.ID, "apiKey": secret, "baseUrl": settings.BaseURL, "provider": "custom", "model": "hub-chat", "allowedModels": llmStringList(key.AllowedModels), "defaultModel": key.DefaultModel, "revoked": key.Revoked})
}
