package manager

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/common"
	"github.com/vertrai/hub/manager/schema"
	gatewayweb "github.com/vertrai/hub/web"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) router() *gin.Engine {
	r := gin.New()
	r.Use(common.RequestLogger(log), gin.Recovery(), common.CORSMiddleware())
	r.GET("/info", m.info)
	r.POST("/v1/wechat/login", m.loginMiniProgramUser)
	r.POST("/v1/wechat/agents/spawn", m.spawnMiniProgramAgent)
	r.GET("/v1/wechat/agents/current", m.getCurrentMiniProgramAgent)
	r.GET("/v1/wechat/agents/:taskId", m.getMiniProgramAgent)
	r.POST("/v1/wechat/agents/:taskId/wechat-qr/refresh", m.refreshMiniProgramAgentQR)
	gatewayweb.RegisterRoutes(r, m.requireAdminPage)
	r.GET("/v1/admin/auth/info", m.adminAuthInfo)
	r.POST("/v1/admin/auth/google", m.adminGoogleLogin)
	r.GET("/v1/admin/session", m.adminMe)
	r.POST("/v1/admin/logout", m.adminLogout)
	admin := r.Group("/v1/admin", m.requireAdmin)
	admin.POST("/users", m.createUserAccessKey)
	admin.GET("/users", m.listUsers)
	admin.PATCH("/access-keys/:id/scopes", m.updateAccessKeyScopes)
	admin.GET("/users/options", m.listUserOptions)
	admin.GET("/users/:userId/access-keys/available", m.listAvailableAccessKeys)
	admin.POST("/access-keys/:id/telegram-bot", m.acquireTelegramBot)
	admin.POST("/telegram/bot-link", m.resolveTelegramBotLink)
	admin.POST("/browser/sessions/:id/close", func(c *gin.Context) {
		m.proxyResource("/v1/internal/browser/sessions/" + url.PathEscape(c.Param("id")) + "/close")(c)
	})
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/google/accounts"}, {http.MethodPost, "/google/accounts/batch"}, {http.MethodGet, "/google/accounts"},
		{http.MethodGet, "/browser/sessions"},
		{http.MethodPost, "/telegram/bots"}, {http.MethodGet, "/telegram/bots"}, {http.MethodPost, "/telegram/bots/create"},
		{http.MethodPost, "/telegram/auth/init"}, {http.MethodPost, "/telegram/auth/verify"}, {http.MethodPost, "/telegram/auth/2fa"}, {http.MethodGet, "/telegram/auth/status"}, {http.MethodGet, "/telegram/auth/accounts"},
	} {
		admin.Handle(route.method, route.path, m.proxyResource("/v1/internal"+route.path))
	}
	admin.POST("/hymatrix/pods", m.spawnPod)
	admin.POST("/hymatrix/pods/:id/start", m.startPod)
	admin.POST("/hymatrix/pods/:id/eval", m.evalPod)
	admin.DELETE("/hymatrix/pods/:id", m.deletePod)
	admin.GET("/hymatrix/pods", m.listPods)
	admin.GET("/hymatrix/node-info", m.hymatrixNodeInfo)
	admin.POST("/weixin/onboarding", m.startWeixinOnboarding)
	admin.GET("/weixin/onboarding/:attempt", m.pollWeixinOnboarding)
	admin.GET("/weixin/onboarding/:attempt/credentials", m.getWeixinOnboardingCredentials)
	admin.DELETE("/weixin/onboarding/:attempt", m.cancelWeixinOnboarding)
	admin.GET("/weixin/bots", m.listWeixinBots)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/google-user"}, {http.MethodGet, "/google-user/access-token"},
		{http.MethodPost, "/google-user/test/gmail/send"}, {http.MethodPost, "/google-user/test/drive/folders"},
		{http.MethodGet, "/browser"}, {http.MethodPost, "/browser/reset"}, {http.MethodPost, "/browser/close"},
		{http.MethodGet, "/telegram-bot"},
	} {
		r.Handle(route.method, "/v1"+route.path, m.proxyGatewayResource("/v1"+route.path))
	}
	return r
}

func (m *Manager) evalPod(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	var pod schema.HymatrixPod
	if err := m.wdb.Db.First(&pod, "id = ?", c.Param("id")).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "pod not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pod.Status != schema.PodStatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "Eval is only available for a running pod"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(pod.RuntimeType), "hermes") {
		c.JSON(http.StatusConflict, gin.H{"error": "Eval is only available for a Hermes runtime"})
		return
	}
	if c.GetHeader("X-Hymatrix-Eval-Confirmation") != pod.ID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "explicit Eval confirmation is required"})
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "command is required"})
		return
	}
	client, err := NewHymatrixClient(HymatrixConfig{NodeURL: pod.NodeURL, PrivateKey: pod.PrivateKey, Module: pod.Module, Scheduler: pod.Scheduler})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	actor := c.GetString("adminEmail")
	commandHash := sha256.Sum256([]byte(req.Command))
	messageID, runtimeResult, err := client.Eval(c.Request.Context(), pod.PID, req.Command)
	if err != nil {
		log.Warn("admin Eval failed", "actor", actor, "pod", pod.ID, "pid", pod.PID, "command_sha256", fmt.Sprintf("%x", commandHash), "command_bytes", len(req.Command), "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	log.Info("admin Eval submitted", "actor", actor, "pod", pod.ID, "pid", pod.PID, "message_id", messageID, "command_sha256", fmt.Sprintf("%x", commandHash), "command_bytes", len(req.Command))
	c.JSON(http.StatusOK, gin.H{"accepted": true, "messageId": messageID, "runtimeResult": runtimeResult, "podId": pod.ID, "pid": pod.PID})
}

func (m *Manager) runAPI(endpoint string) {
	m.apiServer = &http.Server{Addr: endpoint, Handler: m.router()}
	if err := m.apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("api server stopped", "err", err)
	}
}
func (m *Manager) info(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"service": "manager", "status": "ok", "resourcesConfigured": m.resources.configured()})
}
func (m *Manager) proxyResource(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body any
		if c.Request.Body != nil && c.Request.ContentLength != 0 {
			var raw json.RawMessage
			if err := c.ShouldBindJSON(&raw); err != nil {
				c.JSON(400, gin.H{"error": "invalid request body"})
				return
			}
			body = raw
		}
		raw, status, err := m.resources.do(c.Request.Context(), c.Request.Method, path, body, "")
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		ctype := "application/json"
		c.Data(status, ctype, raw)
	}
}

func (m *Manager) proxyGatewayResource(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if key == "" {
			key = c.GetHeader("X-Gateway-API-Key")
		}
		if key == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "gateway api key is required"})
			return
		}
		var body any
		if c.Request.Body != nil && c.Request.ContentLength != 0 {
			var raw json.RawMessage
			if err := c.ShouldBindJSON(&raw); err != nil {
				c.JSON(400, gin.H{"error": "invalid request body"})
				return
			}
			body = raw
		}
		raw, status, err := m.resources.do(c.Request.Context(), c.Request.Method, path, body, key)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Data(status, "application/json", raw)
	}
}

func (m *Manager) createUserAccessKey(c *gin.Context) {
	var req struct {
		UserID        string `json:"userId"`
		Name          string `json:"name"`
		AllowGoogle   *bool  `json:"allowGoogle"`
		AllowBrowser  *bool  `json:"allowBrowser"`
		AllowTelegram *bool  `json:"allowTelegram"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.UserID) == "" {
		c.JSON(400, gin.H{"error": "userId is required"})
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.Name == "" {
		req.Name = req.UserID
	}
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	user := schema.User{ID: req.UserID, Name: req.Name, Status: "active"}
	if err := m.wdb.Db.Where("id = ?", user.ID).Assign(schema.User{Name: user.Name, Status: user.Status}).FirstOrCreate(&user).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	created, _, err := m.resources.createAccessKey(c.Request.Context(), req.UserID, ResourceScopes{
		AllowGoogle: boolDefaultTrue(req.AllowGoogle), AllowBrowser: boolDefaultTrue(req.AllowBrowser), AllowTelegram: boolDefaultTrue(req.AllowTelegram),
	})
	if err != nil {
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	local := schema.AccessKey{ID: "mak_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: req.UserID, ResourceKeyID: created.AccessKey.ID, KeyPrefix: created.AccessKey.KeyPrefix, Secret: created.GatewayAPIKey, Status: "available"}
	if err := m.wdb.Db.Create(&local).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"accessKey": created.AccessKey, "gatewayApiKey": created.GatewayAPIKey})
}

func boolDefaultTrue(value *bool) bool {
	return value == nil || *value
}

func (m *Manager) updateAccessKeyScopes(c *gin.Context) {
	var scopes ResourceScopes
	if err := c.ShouldBindJSON(&scopes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid resource scopes"})
		return
	}
	key, status, err := m.resources.updateAccessKeyScopes(c.Request.Context(), c.Param("id"), scopes)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(status, gin.H{"accessKey": key})
}

func (m *Manager) listUserOptions(c *gin.Context) {
	var users []schema.User
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	if err := m.wdb.Db.Where("status = ?", "active").Order("name asc").Find(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"items": users})
}
func (m *Manager) listAvailableAccessKeys(c *gin.Context) {
	var keys []schema.AccessKey
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	if err := m.wdb.Db.Where("user_id = ? AND status = ?", c.Param("userId"), "available").Order("created_at desc").Find(&keys).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"items": keys})
}
func (m *Manager) acquireTelegramBot(c *gin.Context) {
	var key schema.AccessKey
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	if err := m.wdb.Db.Where("id = ? AND status = ?", c.Param("id"), "available").First(&key).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(409, gin.H{"error": "access key is unavailable or already assigned"})
		return
	} else if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	bot, err := m.resources.telegramBotDetails(c.Request.Context(), key.Secret)
	if err != nil {
		var resourcesError *ResourcesHTTPError
		if errors.As(err, &resourcesError) && resourcesError.StatusCode == http.StatusForbidden {
			c.JSON(http.StatusForbidden, gin.H{
				"error":    "当前 API Key 未开通 Telegram 资源，请手动填写 Bot Token 或调整 API Key 权限",
				"resource": "telegram",
			})
			return
		}
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"botToken": bot.BotToken, "username": bot.Username, "botLink": telegramBotLink(bot.Username)})
}

func (m *Manager) listUsers(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	var users []schema.User
	if err := m.wdb.Db.Order("created_at desc").Find(&users).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	keys, err := m.resources.listAccessKeys(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	type userSummary struct {
		schema.User
		AccessKeys []ResourceAccessKey `json:"accessKeys"`
	}
	items := make([]userSummary, 0, len(users))
	byOwner := make(map[string][]ResourceAccessKey)
	for _, key := range keys {
		byOwner[key.OwnerUserID] = append(byOwner[key.OwnerUserID], key)
	}
	for _, user := range users {
		assigned := byOwner[user.ID]
		if assigned == nil {
			assigned = []ResourceAccessKey{}
		}
		items = append(items, userSummary{User: user, AccessKeys: assigned})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (m *Manager) spawnPod(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	var req struct {
		UserID, Name, RuntimeType, NodeURL, AdminURL, PrivateKey, Module string
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.RuntimeType) == "" || strings.TrimSpace(req.NodeURL) == "" || strings.TrimSpace(req.PrivateKey) == "" || strings.TrimSpace(req.Module) == "" {
		c.JSON(400, gin.H{"error": "userId, runtimeType, nodeUrl, privateKey and module are required"})
		return
	}
	var user schema.User
	if err := m.wdb.Db.Where("id = ? AND status = ?", strings.TrimSpace(req.UserID), "active").First(&user).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user is unavailable"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	nodeInfo, err := fetchHymatrixNodeInfo(c.Request.Context(), req.NodeURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	scheduler := strings.TrimSpace(nodeInfo.Node.AccountID)
	config := HymatrixConfig{NodeURL: req.NodeURL, PrivateKey: req.PrivateKey, Module: req.Module, Scheduler: scheduler}
	client, err := NewHymatrixClient(config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	podID := "pod_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	pod := schema.HymatrixPod{ID: podID, UserID: req.UserID, Name: req.Name, RuntimeType: req.RuntimeType, PID: "pending_" + podID, Status: schema.PodStatusSpawning, NodeURL: req.NodeURL, AdminURL: strings.TrimSpace(req.AdminURL), PrivateKey: req.PrivateKey, Module: req.Module, Scheduler: scheduler}
	if pod.Name == "" {
		pod.Name = req.RuntimeType
	}
	if err := m.wdb.Db.Create(&pod).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pid, err := client.Spawn(c.Request.Context(), PodSpawnInput{RuntimeType: req.RuntimeType})
	if err != nil {
		pod.Status = schema.PodStatusFailed
		pod.Error = err.Error()
	} else {
		pod.PID = pid
		pod.Status = schema.PodStatusSpawned
	}
	if saveErr := m.wdb.Db.Save(&pod).Error; saveErr != nil {
		c.JSON(500, gin.H{"error": saveErr.Error()})
		return
	}
	if err != nil {
		c.JSON(502, gin.H{"error": err.Error(), "pod": pod})
		return
	}
	c.JSON(201, gin.H{"pod": pod})
}

func (m *Manager) startPod(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	var pod schema.HymatrixPod
	if err := m.wdb.Db.First(&pod, "id = ?", c.Param("id")).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "pod not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pod.Status != schema.PodStatusSpawned {
		c.JSON(http.StatusConflict, gin.H{"error": "only a spawned pod can be started"})
		return
	}
	var req struct {
		AccessKeyID, BotToken, WeixinBotID, GatewayURL, HermesGatewayToken string
		EnableTelegram                                                     bool `json:"enableTelegram"`
		LLM                                                                struct {
			APIKey, BaseURL, Model, Provider string
		} `json:"llm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.AccessKeyID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "accessKeyId is required"})
		return
	}
	req.GatewayURL = strings.TrimRight(strings.TrimSpace(req.GatewayURL), "/")
	req.HermesGatewayToken = strings.TrimSpace(req.HermesGatewayToken)
	req.BotToken = strings.TrimSpace(req.BotToken)
	req.WeixinBotID = strings.TrimSpace(req.WeixinBotID)
	telegramEnabled := req.EnableTelegram || req.BotToken != ""
	if !telegramEnabled && req.WeixinBotID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one messaging channel (Telegram or Weixin) is required"})
		return
	}
	if req.HermesGatewayToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hermesGatewayToken is required"})
		return
	}
	gatewayURL, gatewayErr := url.Parse(req.GatewayURL)
	if gatewayErr != nil || gatewayURL.Host == "" || (gatewayURL.Scheme != "http" && gatewayURL.Scheme != "https") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gatewayUrl must be an absolute HTTP or HTTPS URL"})
		return
	}
	req.LLM.Provider = strings.TrimSpace(req.LLM.Provider)
	if req.LLM.Provider == "" {
		req.LLM.Provider = "custom"
	}
	if strings.TrimSpace(req.LLM.Model) == "" || strings.TrimSpace(req.LLM.APIKey) == "" || (req.LLM.Provider == "custom" && strings.TrimSpace(req.LLM.BaseURL) == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "llm.model and llm.apiKey are required; llm.baseUrl is also required for custom provider"})
		return
	}
	var accessKey schema.AccessKey
	if err := m.wdb.Db.Where("id = ? AND user_id = ? AND status = ?", req.AccessKeyID, pod.UserID, "available").First(&accessKey).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusConflict, gin.H{"error": "access key is unavailable, belongs to another user, or is already assigned"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var weixinBot schema.WeixinBot
	if req.WeixinBotID != "" {
		if err := m.wdb.Db.Where("id = ? AND user_id = ? AND status = ?", req.WeixinBotID, pod.UserID, schema.WeixinBotStatusAvailable).First(&weixinBot).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusConflict, gin.H{"error": "Weixin bot is unavailable, belongs to another user, or is already assigned"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&schema.HymatrixPod{}).Where("id = ? AND status = ?", pod.ID, schema.PodStatusSpawned).Update("status", schema.PodStatusStarting)
		if result.Error != nil || result.RowsAffected != 1 {
			return fmt.Errorf("pod start was requested concurrently")
		}
		result = tx.Model(&schema.AccessKey{}).Where("id = ? AND status = ?", accessKey.ID, "available").Updates(map[string]any{"status": "assigned", "assigned_pod_id": pod.ID})
		if result.Error != nil || result.RowsAffected != 1 {
			return fmt.Errorf("access key was assigned concurrently")
		}
		if req.WeixinBotID != "" {
			result = tx.Model(&schema.WeixinBot{}).Where("id = ? AND status = ?", req.WeixinBotID, schema.WeixinBotStatusAvailable).Updates(map[string]any{"status": schema.WeixinBotStatusAssigned, "assigned_pod_id": pod.ID})
			if result.Error != nil || result.RowsAffected != 1 {
				return fmt.Errorf("Weixin bot was assigned concurrently")
			}
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	botToken := req.BotToken
	var startErr error
	if botToken == "" && telegramEnabled {
		botToken, startErr = m.resources.telegramBot(c.Request.Context(), accessKey.Secret)
	}
	if startErr == nil {
		client, err := NewHymatrixClient(HymatrixConfig{NodeURL: pod.NodeURL, PrivateKey: pod.PrivateKey, Module: pod.Module, Scheduler: pod.Scheduler, LLMAPIKey: req.LLM.APIKey, LLMBaseURL: req.LLM.BaseURL, LLMModel: req.LLM.Model, LLMProvider: req.LLM.Provider})
		if err != nil {
			startErr = err
		} else {
			startErr = client.StartAgent(c.Request.Context(), pod.PID, PodStartInput{GatewayURL: req.GatewayURL, GatewayAPIKey: accessKey.Secret, BotToken: botToken, HermesGatewayToken: req.HermesGatewayToken, WeixinAccountID: weixinBot.AccountID, WeixinToken: weixinBot.Token, WeixinBaseURL: weixinBot.BaseURL, WeixinAllowedUsers: weixinBot.AllowedUserID})
		}
	}
	if startErr != nil {
		cleanupErr := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&schema.AccessKey{}).Where("id = ? AND assigned_pod_id = ?", accessKey.ID, pod.ID).Updates(map[string]any{"status": "available", "assigned_pod_id": nil}).Error; err != nil {
				return err
			}
			if req.WeixinBotID != "" {
				if err := tx.Model(&schema.WeixinBot{}).Where("id = ? AND assigned_pod_id = ?", req.WeixinBotID, pod.ID).Updates(map[string]any{"status": schema.WeixinBotStatusAvailable, "assigned_pod_id": nil}).Error; err != nil {
					return err
				}
			}
			return tx.Model(&schema.HymatrixPod{}).Where("id = ?", pod.ID).Updates(map[string]any{"status": schema.PodStatusSpawned, "error": startErr.Error()}).Error
		})
		if cleanupErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%v; restore start state: %v", startErr, cleanupErr)})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": startErr.Error()})
		return
	}
	pod.Status = schema.PodStatusRunning
	pod.Error = ""
	pod.LLMAPIKey, pod.LLMBaseURL, pod.LLMModel, pod.LLMProvider = req.LLM.APIKey, req.LLM.BaseURL, req.LLM.Model, req.LLM.Provider
	pod.GatewayAPIKey, pod.AccessKeyID, pod.BotToken, pod.WeixinBotID = accessKey.Secret, accessKey.ID, botToken, req.WeixinBotID
	if err := m.wdb.Db.Save(&pod).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"pod": pod})
}

func (m *Manager) hymatrixNodeInfo(c *gin.Context) {
	nodeURL := strings.TrimSpace(c.Query("nodeUrl"))
	if nodeURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nodeUrl is required"})
		return
	}
	info, err := fetchHymatrixNodeInfo(c.Request.Context(), nodeURL)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"scheduler":   strings.TrimSpace(info.Node.AccountID),
		"nodeName":    info.Node.Name,
		"nodeVersion": info.NodeVersion,
		"protocol":    info.Protocol,
	})
}
func (m *Manager) deletePod(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	podID := c.Param("id")
	var target schema.HymatrixPod
	if err := m.wdb.Db.First(&target, "id = ?", podID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "pod not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if target.Status == schema.PodStatusSpawning || target.Status == schema.PodStatusStarting {
		c.JSON(http.StatusConflict, gin.H{"error": errPodDeletionInProgress.Error()})
		return
	}
	attemptIDs := []string{}
	deletedTasks := int64(0)
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var pod schema.HymatrixPod
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&pod, "id = ?", podID).Error; err != nil {
			return err
		}
		if pod.Status == schema.PodStatusSpawning || pod.Status == schema.PodStatusStarting {
			return errPodDeletionInProgress
		}
		var tasks []schema.MiniProgramAgentTask
		taskScope := "pod_id = ? OR (user_id = ? AND pod_id = '' AND status = ?)"
		taskScopeArgs := []any{pod.ID, pod.UserID, "DELETED"}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status", "weixin_attempt_id").Where(taskScope, taskScopeArgs...).Find(&tasks).Error; err != nil {
			return err
		}
		for _, task := range tasks {
			if miniProgramTaskDeletionInProgress(task.Status) {
				return errPodDeletionInProgress
			}
			if task.WeixinAttemptID != "" {
				attemptIDs = append(attemptIDs, task.WeixinAttemptID)
			}
		}
		if err := m.stopHymatrixVM(c.Request.Context(), pod.AdminURL, pod.NodeURL, pod.PID); err != nil {
			return fmt.Errorf("%w: %v", errStopHymatrixVM, err)
		}
		if err := tx.Model(&schema.AccessKey{}).Where("assigned_pod_id = ?", pod.ID).Updates(map[string]any{"status": "available", "assigned_pod_id": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&schema.WeixinBot{}).Where("assigned_pod_id = ?", pod.ID).Updates(map[string]any{"status": schema.WeixinBotStatusAvailable, "assigned_pod_id": nil}).Error; err != nil {
			return err
		}
		result := tx.Where(taskScope, taskScopeArgs...).Delete(&schema.MiniProgramAgentTask{})
		if result.Error != nil {
			return result.Error
		}
		deletedTasks = result.RowsAffected
		return tx.Delete(&pod).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "pod not found"})
		return
	}
	if errors.Is(err, errPodDeletionInProgress) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, errStopHymatrixVM) {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, attemptID := range attemptIDs {
		m.consumeWeixinAttempt(attemptID)
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "podId": podID, "deletedMiniProgramTasks": deletedTasks})
}

var (
	errPodDeletionInProgress = errors.New("cannot delete a pod while its provisioning or startup task is in progress")
	errStopHymatrixVM        = errors.New("stop Hymx VM")
)

func miniProgramTaskDeletionInProgress(status string) bool {
	return status == schema.MiniProgramTaskSpawning || status == schema.MiniProgramTaskRefreshingQR || status == schema.MiniProgramTaskStartingAgent
}

func (m *Manager) listPods(c *gin.Context) {
	var pods []schema.HymatrixPod
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	if err := m.wdb.Db.Order("created_at desc").Find(&pods).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"items": pods})
}
