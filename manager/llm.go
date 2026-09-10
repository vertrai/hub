package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/manager/llm"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func llmError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "gateway_error", "code": http.StatusText(status)}})
}
func (m *Manager) requireLLMKey(c *gin.Context) {
	if m.wdb == nil {
		llmError(c, 503, "manager database unavailable")
		c.Abort()
		return
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		llmError(c, 401, "LLM API key required")
		c.Abort()
		return
	}
	hash := sha256.Sum256([]byte(parts[1]))
	var key schema.LLMKey
	err := m.wdb.Db.Where("hash = ? AND revoked = ?", hex.EncodeToString(hash[:]), false).First(&key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			llmError(c, 401, "invalid LLM API key")
		} else {
			llmError(c, 503, "key lookup unavailable")
		}
		c.Abort()
		return
	}
	c.Set("llmKey", key)
}
func (m *Manager) listLLMProviders(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	var providers []schema.LLMProvider
	if err := m.wdb.Db.Order("id").Find(&providers).Error; err != nil {
		c.JSON(500, gin.H{"error": "provider lookup failed"})
		return
	}
	items := make([]any, 0, len(providers))
	for _, p := range providers {
		var models []string
		_ = json.Unmarshal([]byte(p.Models), &models)
		item := gin.H{"id": p.ID, "name": p.Name, "preset": p.Preset, "kind": p.Kind, "baseUrl": p.BaseURL, "models": models, "enabled": p.Enabled, "updatedAt": p.UpdatedAt}
		if p.Kind == "openai-codex" {
			accountID, email, expiresAt := llm.CredentialMetadata(p.Credential)
			item["accountId"], item["email"], item["expiresAt"] = accountID, email, expiresAt
		}
		items = append(items, item)
	}
	c.JSON(200, gin.H{"items": items})
}

var llmProviderID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

func (m *Manager) saveLLMProvider(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	var input struct {
		Name       string          `json:"name"`
		Preset     string          `json:"preset"`
		APIKey     string          `json:"apiKey"`
		Kind       string          `json:"kind"`
		BaseURL    string          `json:"baseUrl"`
		Models     []string        `json:"models"`
		Credential json.RawMessage `json:"credential"`
		Enabled    bool            `json:"enabled"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if c.ShouldBindJSON(&input) != nil || (input.Kind != "openai-codex" && input.Kind != "openai") || len(input.Models) == 0 {
		c.JSON(400, gin.H{"error": "valid id, kind and models are required"})
		return
	}
	providerID := c.Param("id")
	if c.Request.Method == http.MethodPost {
		providerID = newLLMProviderID(input.Preset, input.Kind)
	}
	if !llmProviderID.MatchString(providerID) {
		c.JSON(400, gin.H{"error": "invalid provider id"})
		return
	}
	modelsList, err := normalizeLLMModels(input.Models)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	input.Models = modelsList
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) > 200 {
		c.JSON(400, gin.H{"error": "名称不能超过 200 字符"})
		return
	}
	if input.Name == "" {
		input.Name = providerID
	}
	if input.Preset != "" {
		preset, ok := findLLMPreset(input.Preset)
		if !ok || input.Kind != "openai" {
			c.JSON(400, gin.H{"error": "invalid preset"})
			return
		}
		if strings.TrimSpace(input.BaseURL) == "" {
			input.BaseURL = preset.BaseURL
		}
		if input.Preset == "custom" && strings.TrimSpace(input.BaseURL) == "" {
			c.JSON(400, gin.H{"error": "自定义服务需要填写 Base URL"})
			return
		}
	}
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if input.BaseURL == "" {
		if input.Kind == "openai-codex" {
			input.BaseURL = "https://chatgpt.com/backend-api/codex"
		} else {
			input.BaseURL = "https://api.openai.com/v1"
		}
	}
	u, err := url.Parse(input.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		c.JSON(400, gin.H{"error": "invalid base URL"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	var previous schema.LLMProvider
	err = m.wdb.Db.First(&previous, "id = ?", providerID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(500, gin.H{"error": "provider lookup failed"})
		return
	}
	credential := []byte(input.Credential)
	if input.Kind == "openai" && strings.TrimSpace(input.APIKey) != "" {
		credential, _ = json.Marshal(map[string]string{"api_key": strings.TrimSpace(input.APIKey)})
	}
	if len(credential) == 0 || string(credential) == "null" {
		if previous.Kind != input.Kind {
			c.JSON(400, gin.H{"error": "credentials required for this provider"})
			return
		}
		credential = previous.Credential
	}
	if input.Kind == "openai-codex" {
		credential, err = llm.ImportCredential(credential, input.BaseURL)
	} else {
		var v struct {
			APIKey string `json:"api_key"`
		}
		err = json.Unmarshal(credential, &v)
		if strings.TrimSpace(v.APIKey) == "" {
			err = errors.New("api_key is required")
		}
	}
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid credentials: " + err.Error()})
		return
	}
	models, _ := json.Marshal(input.Models)
	p := schema.LLMProvider{ID: providerID, Name: input.Name, Preset: input.Preset, Kind: input.Kind, BaseURL: input.BaseURL, Models: string(models), Credential: credential, Enabled: input.Enabled}
	if err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "preset", "kind", "base_url", "models", "credential", "enabled", "updated_at"})}).Create(&p).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		c.JSON(500, gin.H{"error": "Provider 保存失败"})
		return
	}
	m.codex.DiscardCredential(p.ID)
	c.JSON(200, gin.H{"saved": true, "providerId": providerID})
}
func (m *Manager) deleteLLMProvider(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	m.llmMu.Lock()
	defer m.llmMu.Unlock()
	if err := m.wdb.Db.Delete(&schema.LLMProvider{}, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(500, gin.H{"error": "delete failed"})
		return
	}
	m.codex.DiscardCredential(c.Param("id"))
	c.JSON(200, gin.H{"deleted": true})
}
func (m *Manager) llmKeys(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	switch c.Request.Method {
	case http.MethodPost:
		var input struct {
			Name          string    `json:"name"`
			AllowedModels *[]string `json:"allowedModels"`
			DefaultModel  string    `json:"defaultModel"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Name) == "" {
			c.JSON(400, gin.H{"error": "name required"})
			return
		}
		m.llmMu.Lock()
		defer m.llmMu.Unlock()
		allowed := []string{}
		if input.AllowedModels == nil {
			c.JSON(400, gin.H{"error": "请选择此 Key 允许使用的模型"})
			return
		}
		allowed = *input.AllowedModels

		if err := m.validateLLMPolicy(allowed, input.DefaultModel); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		key, secret, err := newLLMKey(input.Name, allowed, input.DefaultModel)
		if err != nil {
			llmResourceFailure(c, errors.New("key generation failed"))
			return
		}
		if err := m.wdb.Db.Create(&key).Error; err != nil {
			llmResourceFailure(c, errors.New("key creation failed"))
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"key": key, "apiKey": secret})
	case http.MethodDelete:
		if err := m.wdb.Db.Model(&schema.LLMKey{}).Where("id = ?", c.Param("id")).Update("revoked", true).Error; err != nil {
			llmResourceFailure(c, errors.New("key revocation failed"))
			return
		}
		c.JSON(200, gin.H{"deleted": true})
	default:
		var keys []schema.LLMKey
		if err := m.wdb.Db.Order("created_at desc").Find(&keys).Error; err != nil {
			llmResourceFailure(c, errors.New("key lookup failed"))
			return
		}
		items := []any{}
		for _, key := range keys {
			items = append(items, gin.H{"id": key.ID, "name": key.Name, "prefix": key.Prefix, "hubAccessKeyId": key.HubAccessKeyID, "ownerUserId": key.OwnerUserID, "allowedModels": llmStringList(key.AllowedModels), "defaultModel": key.DefaultModel, "revoked": key.Revoked})
		}
		c.JSON(200, gin.H{"items": items})
	}
}
func (m *Manager) llmModels(c *gin.Context) {
	key := c.MustGet("llmKey").(schema.LLMKey)
	models, err := m.availableLLMModels()
	if err != nil {
		llmError(c, 503, "model lookup unavailable")
		return
	}
	data := []any{}
	if key.DefaultModel != "" && keyCanUseLLM(key, key.DefaultModel) {
		if _, _, err := m.resolveLLMModel(key.DefaultModel); err == nil {
			data = append(data, gin.H{"id": "hub-chat", "object": "model", "created": 0, "owned_by": "hub"})
		}
	}
	for _, model := range models {
		if keyCanUseLLM(key, model.ID) {
			data = append(data, gin.H{"id": model.ID, "object": "model", "created": 0, "owned_by": "hub"})
		}
	}
	c.JSON(200, gin.H{"object": "list", "data": data})
}
func (m *Manager) relayLLM(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var body map[string]any
	if json.NewDecoder(c.Request.Body).Decode(&body) != nil || body == nil {
		llmError(c, 400, "invalid JSON request (limit 8 MiB)")
		return
	}
	model, _ := body["model"].(string)
	key := c.MustGet("llmKey").(schema.LLMKey)
	resolvedModel := model
	if model == "hub-chat" {
		resolvedModel = key.DefaultModel
	}
	if resolvedModel == "" {
		llmError(c, 400, "model is required; hub-chat needs a configured default model")
		return
	}
	if !keyCanUseLLM(key, resolvedModel) {
		llmError(c, 403, "model is not allowed for this LLM API key")
		return
	}
	m.llmMu.Lock()
	p, upstreamModel, err := m.resolveLLMModel(resolvedModel)
	if err == nil && p.Kind == "openai-codex" {
		var updated []byte
		updated, err = m.codex.PrepareCredential(c.Request.Context(), p.ID, p.Credential)
		if err == nil && len(updated) > 0 {
			err = m.wdb.Db.Model(&p).Update("credential", updated).Error
			if err == nil {
				p.Credential = updated
			}
		}
	}
	m.llmMu.Unlock()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			llmError(c, 404, "model unavailable")
		} else {
			llmError(c, 503, "provider unavailable; check credentials")
		}
		return
	}
	stream, _ := body["stream"].(bool)
	endpoint := llm.GatewayEndpointResponses
	path := "/responses"
	if strings.HasSuffix(c.Request.URL.Path, "/chat/completions") {
		endpoint = llm.GatewayEndpointChat
		path = "/chat/completions"
	}
	if p.Kind == "openai-codex" {
		raw, _ := json.Marshal(body)
		input := llm.CodexAdapterRequest{Account: llm.Account{ID: p.ID, Credential: p.Credential}, UpstreamModel: upstreamModel, Body: raw}
		if !stream {
			var result llm.CodexAdapterResult
			if endpoint == llm.GatewayEndpointChat {
				result = m.codex.ExecuteChat(c.Request.Context(), input)
			} else {
				result = m.codex.Execute(c.Request.Context(), input)
			}
			if result.Err != nil {
				status := 502
				if result.ErrorCode == "invalid_request" {
					status = 400
				}
				llmError(c, status, "Codex request failed: "+result.ErrorCode)
				return
			}
			c.Data(result.StatusCode, "application/json", result.Body)
			return
		}
		result := m.codex.OpenStream(c.Request.Context(), llm.CodexStreamRequest{CodexAdapterRequest: input, Endpoint: endpoint})
		if result.Err != nil {
			status := 502
			if result.ErrorCode == "invalid_request" {
				status = 400
			}
			llmError(c, status, "Codex stream failed: "+result.ErrorCode)
			return
		}
		relayLLMResponse(c, result.Response, true)
		return
	}
	var credential struct {
		APIKey string `json:"api_key"`
	}
	_ = json.Unmarshal(p.Credential, &credential)
	body["model"] = upstreamModel
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, p.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		llmError(c, 502, "invalid upstream URL")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential.APIKey)
	resp, err := m.llmClient.Do(req)
	if err != nil {
		llmError(c, 502, llmTransportErrorMessage(err))
		return
	}
	relayLLMResponse(c, resp, stream)
}
func relayLLMResponse(c *gin.Context, resp *http.Response, stream bool) {
	defer resp.Body.Close()
	for _, key := range []string{"Content-Type", "Retry-After", "X-Request-Id"} {
		if v := resp.Header.Get(key); v != "" {
			c.Header(key, v)
		}
	}
	if stream {
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
	}
	c.Status(resp.StatusCode)
	buffer := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := c.Writer.Write(buffer[:n]); writeErr != nil {
				return
			}
			if stream {
				c.Writer.Flush()
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Warn("LLM upstream stream interrupted")
			}
			return
		}
	}
}

func newLLMClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

// IDs are stable routing identifiers, independent of the editable display name.
func newLLMProviderID(preset, kind string) string {
	prefix := "provider"
	if kind == "openai-codex" {
		prefix = "codex"
	} else if p, ok := findLLMPreset(preset); ok && p.ID != "custom" {
		prefix = p.ID
	}
	return prefix + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// Report actionable transport categories without exposing URLs or credentials.
func llmTransportErrorMessage(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "proxyconnect" {
		return "无法连接上游代理，请检查 Manager 进程的 HTTP_PROXY / HTTPS_PROXY（含小写变量）及代理服务；无需代理时清除这些变量后重启 Manager"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "上游域名解析失败，请检查 Provider Base URL 和 Manager 的 DNS 网络配置"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "上游连接被拒绝，请检查 Provider Base URL、服务端口和代理配置"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接上游超时，请检查 Manager 到上游的网络或代理"
	}
	return "上游网络请求失败，请检查 Manager 的网络、代理及 TLS 证书配置"
}
