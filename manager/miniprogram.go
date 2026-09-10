package manager

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const miniProgramSessionLifetime = 30 * 24 * time.Hour

func (m *Manager) loginMiniProgramUser(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	if err := validateMiniProgramIdentityConfig(m.config.MiniProgram); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WeChat login code is required"})
		return
	}
	openid, err := m.exchangeMiniProgramCode(c.Request.Context(), strings.TrimSpace(input.Code))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	userID := miniProgramUserID(m.config.MiniProgram.AppID, openid)
	user := schema.User{ID: userID, Name: "微信用户 " + userID[len(userID)-8:], Status: "active"}
	if err := m.wdb.Db.Where("id = ?", userID).Assign(schema.User{Status: "active"}).FirstOrCreate(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	expiresAt := time.Now().UTC().Add(miniProgramSessionLifetime)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"sessionToken": m.signMiniProgramSession(userID, expiresAt), "expiresAt": expiresAt, "user": gin.H{"id": user.ID, "name": user.Name}})
}

func (m *Manager) spawnMiniProgramAgent(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	if err := validateMiniProgramConfig(m.config.MiniProgram); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	userID, ok := m.requireMiniProgramSession(c)
	if !ok {
		return
	}
	var input struct {
		Template string `json:"template"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if input.Template != "" && input.Template != "hermes" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported agent template"})
		return
	}
	_, tokenHash, err := newMiniProgramTaskToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create task token"})
		return
	}
	task := schema.MiniProgramAgentTask{ID: "mpt_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: userID, TokenHash: tokenHash, Status: schema.MiniProgramTaskSpawning}
	err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var lockedUser schema.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedUser, "id = ?", userID).Error; err != nil {
			return err
		}
		var activeTasks int64
		if err := tx.Model(&schema.MiniProgramAgentTask{}).Where("user_id = ? AND (pod_id <> '' OR status = ?)", userID, schema.MiniProgramTaskSpawning).Count(&activeTasks).Error; err != nil {
			return err
		}
		if activeTasks > 0 {
			return errMiniProgramAgentAlreadyActive
		}
		var recentAttempts int64
		if err := tx.Model(&schema.MiniProgramAgentTask{}).Where("user_id = ? AND created_at >= ?", userID, time.Now().UTC().Add(-time.Hour)).Count(&recentAttempts).Error; err != nil {
			return err
		}
		if recentAttempts >= 3 {
			return errMiniProgramProvisionRateLimited
		}
		return tx.Create(&task).Error
	})
	if errors.Is(err, errMiniProgramAgentAlreadyActive) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, errMiniProgramProvisionRateLimited) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	go m.provisionMiniProgramAgentTask(task.ID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, m.miniProgramTaskResponse(task, ""))
}

var (
	errMiniProgramAgentAlreadyActive   = errors.New("该微信用户已经拥有正在创建或运行的 Agent")
	errMiniProgramProvisionRateLimited = errors.New("创建次数过多，请一小时后重试")
)

func (m *Manager) provisionMiniProgramAgentTask(taskID string) {
	var task schema.MiniProgramAgentTask
	if err := m.wdb.Db.First(&task, "id = ?", taskID).Error; err != nil {
		return
	}
	if err := m.provisionMiniProgramPod(context.Background(), &task); err != nil {
		m.failMiniProgramTask(&task, err)
		return
	}
	prepareMiniProgramTaskForWeixin(&task)
	if err := m.wdb.Db.Save(&task).Error; err != nil {
		m.failMiniProgramTask(&task, err)
	}
}

func prepareMiniProgramTaskForWeixin(task *schema.MiniProgramAgentTask) {
	task.Status = schema.MiniProgramTaskWaitingForWeixin
	task.WeixinAttemptID = ""
	task.QRCodeData = ""
	task.QRExpiresAt = time.Time{}
	task.Error = ""
}

func (m *Manager) getMiniProgramAgent(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	userID, ok := m.requireMiniProgramSession(c)
	if !ok {
		return
	}
	var task schema.MiniProgramAgentTask
	if err := m.wdb.Db.First(&task, "id = ? AND user_id = ?", c.Param("taskId"), userID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent task not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	m.reconcileMiniProgramWeixinAttempt(&task)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, m.miniProgramTaskResponse(task, ""))
}

func (m *Manager) getCurrentMiniProgramAgent(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	userID, ok := m.requireMiniProgramSession(c)
	if !ok {
		return
	}
	var task schema.MiniProgramAgentTask
	err := m.wdb.Db.Where("user_id = ?", userID).Order("created_at desc").First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"task": nil})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	m.reconcileMiniProgramWeixinAttempt(&task)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"task": m.miniProgramTaskResponse(task, "")})
}

func (m *Manager) refreshMiniProgramAgentQR(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	userID, ok := m.requireMiniProgramSession(c)
	if !ok {
		return
	}
	var task schema.MiniProgramAgentTask
	if err := m.wdb.Db.First(&task, "id = ? AND user_id = ?", c.Param("taskId"), userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent task not found"})
		return
	}
	if !canRenewMiniProgramQR(task.Status) || task.PodID == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "WeChat QR code cannot be renewed in the current state"})
		return
	}
	previousStatus := task.Status
	claim := m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ?", task.ID, previousStatus).Update("status", schema.MiniProgramTaskRefreshingQR)
	if claim.Error != nil || claim.RowsAffected != 1 {
		c.JSON(http.StatusConflict, gin.H{"error": "QR code refresh is already in progress"})
		return
	}
	onboarding, err := m.createWeixinOnboarding(c.Request.Context(), userID)
	if err != nil {
		m.consumeWeixinAttempt(task.WeixinAttemptID)
		m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ?", task.ID, schema.MiniProgramTaskRefreshingQR).Updates(map[string]any{"status": schema.MiniProgramTaskQRExpired, "error": "微信连接码更新失败，请重试"})
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	result := m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ?", task.ID, schema.MiniProgramTaskRefreshingQR).Updates(map[string]any{
		"status": schema.MiniProgramTaskWaitingForWeixin, "weixin_attempt_id": onboarding.AttemptID, "qr_code_data": onboarding.QRImage, "qr_expires_at": onboarding.ExpiresAt, "error": "",
	})
	if result.Error != nil || result.RowsAffected != 1 {
		m.consumeWeixinAttempt(onboarding.AttemptID)
		m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ?", task.ID, schema.MiniProgramTaskRefreshingQR).Updates(map[string]any{"status": schema.MiniProgramTaskQRExpired, "error": "微信连接码刷新失败，请重试"})
		c.JSON(http.StatusConflict, gin.H{"error": "QR code was refreshed concurrently"})
		return
	}
	task.Status, task.WeixinAttemptID, task.QRCodeData, task.QRExpiresAt, task.Error = schema.MiniProgramTaskWaitingForWeixin, onboarding.AttemptID, onboarding.QRImage, onboarding.ExpiresAt, ""
	go m.watchMiniProgramWeixin(task.ID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, m.miniProgramTaskResponse(task, ""))
}

func canRenewMiniProgramQR(status string) bool {
	return status == schema.MiniProgramTaskWaitingForWeixin || status == schema.MiniProgramTaskQRExpired
}

func (m *Manager) reconcileMiniProgramWeixinAttempt(task *schema.MiniProgramAgentTask) {
	if task.Status == schema.MiniProgramTaskRefreshingQR {
		if time.Since(task.UpdatedAt) > time.Minute {
			result := m.wdb.Db.Model(task).Where("id = ? AND status = ?", task.ID, schema.MiniProgramTaskRefreshingQR).Updates(map[string]any{"status": schema.MiniProgramTaskQRExpired, "error": "微信连接码刷新中断，请重试"})
			if result.Error == nil && result.RowsAffected == 1 {
				task.Status, task.Error = schema.MiniProgramTaskQRExpired, "微信连接码刷新中断，请重试"
			}
		}
		return
	}
	if task.Status != schema.MiniProgramTaskWaitingForWeixin || task.WeixinAttemptID == "" {
		return
	}
	m.weixinMu.Lock()
	_, exists := m.weixinAttempts[task.WeixinAttemptID]
	m.weixinMu.Unlock()
	// Another Manager replica may own the in-memory attempt. Wait until the
	// advertised QR lifetime has elapsed before offering a safe refresh.
	if exists || time.Now().UTC().Before(task.QRExpiresAt) {
		return
	}
	result := m.wdb.Db.Model(task).Where("id = ? AND status = ?", task.ID, schema.MiniProgramTaskWaitingForWeixin).Updates(map[string]any{"status": schema.MiniProgramTaskQRExpired, "error": "微信连接码需要刷新"})
	if result.Error == nil && result.RowsAffected == 1 {
		task.Status, task.Error = schema.MiniProgramTaskQRExpired, "微信连接码需要刷新"
	}
}

func (m *Manager) watchMiniProgramWeixin(taskID string) {
	for {
		var task schema.MiniProgramAgentTask
		if err := m.wdb.Db.First(&task, "id = ?", taskID).Error; err != nil || task.Status != schema.MiniProgramTaskWaitingForWeixin {
			return
		}
		if !task.QRExpiresAt.IsZero() && time.Now().UTC().After(task.QRExpiresAt) {
			result := m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ? AND weixin_attempt_id = ?", task.ID, schema.MiniProgramTaskWaitingForWeixin, task.WeixinAttemptID).Updates(map[string]any{"status": schema.MiniProgramTaskQRExpired, "error": "微信二维码已过期"})
			if result.Error == nil && result.RowsAffected == 1 {
				m.consumeWeixinAttempt(task.WeixinAttemptID)
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		state, status, err := m.pollWeixinOnboardingAttempt(ctx, task.WeixinAttemptID)
		cancel()
		if err != nil {
			if status != http.StatusConflict {
				log.Warn("mini program Weixin poll failed", "task", task.ID, "err", err)
			}
			time.Sleep(2 * time.Second)
			continue
		}
		if state.State == "expired" {
			m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ? AND weixin_attempt_id = ?", task.ID, schema.MiniProgramTaskWaitingForWeixin, task.WeixinAttemptID).Updates(map[string]any{"status": schema.MiniProgramTaskQRExpired, "error": "微信二维码已过期"})
			return
		}
		if state.State != "connected" {
			time.Sleep(2 * time.Second)
			continue
		}
		result := m.wdb.Db.Model(&schema.MiniProgramAgentTask{}).Where("id = ? AND status = ? AND weixin_attempt_id = ?", task.ID, schema.MiniProgramTaskWaitingForWeixin, task.WeixinAttemptID).Updates(map[string]any{"status": schema.MiniProgramTaskStartingAgent, "qr_code_data": ""})
		if result.Error != nil || result.RowsAffected != 1 {
			return
		}
		task.Status, task.QRCodeData = schema.MiniProgramTaskStartingAgent, ""
		startCtx, startCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		err = m.startMiniProgramPod(startCtx, &task, state.BotID)
		startCancel()
		if err != nil {
			m.failMiniProgramTask(&task, err)
		} else {
			task.Status, task.Error = schema.MiniProgramTaskRunning, ""
			if saveErr := m.wdb.Db.Save(&task).Error; saveErr != nil {
				log.Error("save running mini program task", "task", task.ID, "err", saveErr)
			}
			m.consumeWeixinAttempt(task.WeixinAttemptID)
		}
		return
	}
}

func (m *Manager) provisionMiniProgramPod(ctx context.Context, task *schema.MiniProgramAgentTask) error {
	created, _, err := m.resources.createAccessKey(ctx, task.UserID, ResourceScopes{AllowGoogle: true, AllowBrowser: true})
	if err != nil {
		return fmt.Errorf("create gateway access key: %w", err)
	}
	accessKey := schema.AccessKey{ID: "mak_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: task.UserID, ResourceKeyID: created.AccessKey.ID, KeyPrefix: created.AccessKey.KeyPrefix, Secret: created.GatewayAPIKey, Status: "available"}
	if err := m.wdb.Db.Create(&accessKey).Error; err != nil {
		return fmt.Errorf("store gateway access key: %w", err)
	}
	cfg := m.config.MiniProgram
	nodeInfo, err := fetchHymatrixNodeInfo(ctx, cfg.NodeURL)
	if err != nil {
		return err
	}
	hymatrixConfig := HymatrixConfig{NodeURL: cfg.NodeURL, PrivateKey: cfg.PrivateKey, Module: cfg.Module, Scheduler: strings.TrimSpace(nodeInfo.Node.AccountID)}
	client, err := NewHymatrixClient(hymatrixConfig)
	if err != nil {
		return err
	}
	pod := schema.HymatrixPod{ID: "pod_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: task.UserID, Name: "财税助手", RuntimeType: cfg.RuntimeType, Status: schema.PodStatusSpawning, NodeURL: cfg.NodeURL, AdminURL: cfg.AdminURL, PrivateKey: cfg.PrivateKey, Module: cfg.Module, Scheduler: hymatrixConfig.Scheduler, AccessKeyID: accessKey.ID}
	pod.PID = "pending_" + pod.ID
	if err := m.wdb.Db.Create(&pod).Error; err != nil {
		return err
	}
	task.PodID = pod.ID
	_ = m.wdb.Db.Save(task).Error
	pid, err := client.Spawn(ctx, PodSpawnInput{RuntimeType: cfg.RuntimeType})
	if err != nil {
		pod.Status, pod.Error = schema.PodStatusFailed, err.Error()
		_ = m.wdb.Db.Save(&pod).Error
		return err
	}
	pod.PID, pod.Status = pid, schema.PodStatusSpawned
	return m.wdb.Db.Save(&pod).Error
}

func (m *Manager) startMiniProgramPod(ctx context.Context, task *schema.MiniProgramAgentTask, weixinBotID string) error {
	var pod schema.HymatrixPod
	if err := m.wdb.Db.First(&pod, "id = ? AND user_id = ?", task.PodID, task.UserID).Error; err != nil {
		return err
	}
	var accessKey schema.AccessKey
	if err := m.wdb.Db.First(&accessKey, "id = ? AND user_id = ? AND status = ?", pod.AccessKeyID, task.UserID, "available").Error; err != nil {
		return fmt.Errorf("load available access key: %w", err)
	}
	var bot schema.WeixinBot
	if err := m.wdb.Db.First(&bot, "id = ? AND user_id = ? AND status = ?", weixinBotID, task.UserID, schema.WeixinBotStatusAvailable).Error; err != nil {
		return fmt.Errorf("load available Weixin bot: %w", err)
	}
	if err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if result := tx.Model(&schema.HymatrixPod{}).Where("id = ? AND status = ?", pod.ID, schema.PodStatusSpawned).Update("status", schema.PodStatusStarting); result.Error != nil || result.RowsAffected != 1 {
			return fmt.Errorf("pod is not available to start")
		}
		if result := tx.Model(&schema.AccessKey{}).Where("id = ? AND status = ?", accessKey.ID, "available").Updates(map[string]any{"status": "assigned", "assigned_pod_id": pod.ID}); result.Error != nil || result.RowsAffected != 1 {
			return fmt.Errorf("access key is not available")
		}
		if result := tx.Model(&schema.WeixinBot{}).Where("id = ? AND status = ?", bot.ID, schema.WeixinBotStatusAvailable).Updates(map[string]any{"status": schema.WeixinBotStatusAssigned, "assigned_pod_id": pod.ID}); result.Error != nil || result.RowsAffected != 1 {
			return fmt.Errorf("Weixin bot is not available")
		}
		return nil
	}); err != nil {
		return err
	}
	cfg := m.config.MiniProgram
	client, err := NewHymatrixClient(HymatrixConfig{NodeURL: pod.NodeURL, PrivateKey: pod.PrivateKey, Module: pod.Module, Scheduler: pod.Scheduler, LLMAPIKey: cfg.LLMAPIKey, LLMBaseURL: cfg.LLMBaseURL, LLMModel: cfg.LLMModel, LLMProvider: cfg.LLMProvider})
	if err == nil {
		err = client.StartAgent(ctx, pod.PID, PodStartInput{GatewayURL: cfg.GatewayURL, GatewayAPIKey: accessKey.Secret, HermesGatewayToken: cfg.HermesGatewayToken, WeixinAccountID: bot.AccountID, WeixinToken: bot.Token, WeixinBaseURL: bot.BaseURL, WeixinAllowedUsers: bot.AllowedUserID})
	}
	if err != nil {
		_ = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
			tx.Model(&schema.AccessKey{}).Where("id = ? AND assigned_pod_id = ?", accessKey.ID, pod.ID).Updates(map[string]any{"status": "available", "assigned_pod_id": nil})
			tx.Model(&schema.WeixinBot{}).Where("id = ? AND assigned_pod_id = ?", bot.ID, pod.ID).Updates(map[string]any{"status": schema.WeixinBotStatusAvailable, "assigned_pod_id": nil})
			return tx.Model(&schema.HymatrixPod{}).Where("id = ?", pod.ID).Updates(map[string]any{"status": schema.PodStatusSpawned, "error": err.Error()}).Error
		})
		return err
	}
	pod.Status, pod.Error, pod.WeixinBotID = schema.PodStatusRunning, "", bot.ID
	pod.GatewayAPIKey, pod.LLMAPIKey, pod.LLMBaseURL, pod.LLMModel, pod.LLMProvider = accessKey.Secret, cfg.LLMAPIKey, cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMProvider
	return m.wdb.Db.Save(&pod).Error
}

func (m *Manager) exchangeMiniProgramCode(ctx context.Context, code string) (string, error) {
	cfg := m.config.MiniProgram
	base := strings.TrimRight(cfg.WeixinAPIBase, "/")
	endpoint := base + "/sns/jscode2session?" + url.Values{"appid": {cfg.AppID}, "secret": {cfg.AppSecret}, "js_code": {code}, "grant_type": {"authorization_code"}}.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	res, err := m.miniProgramHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("WeChat login: %w", err)
	}
	defer res.Body.Close()
	var payload struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.NewDecoder(res.Body).Decode(&payload) != nil || res.StatusCode/100 != 2 || payload.ErrCode != 0 || payload.OpenID == "" {
		return "", fmt.Errorf("WeChat login rejected: %s", payload.ErrMsg)
	}
	return payload.OpenID, nil
}

func validateMiniProgramConfig(cfg MiniProgramConfig) error {
	values := map[string]string{"appId": cfg.AppID, "appSecret": cfg.AppSecret, "pod.nodeURL": cfg.NodeURL, "pod.privateKey": cfg.PrivateKey, "pod.module": cfg.Module, "pod.runtimeType": cfg.RuntimeType, "agent.gatewayURL": cfg.GatewayURL, "agent.hermesGatewayToken": cfg.HermesGatewayToken, "agent.llm.apiKey": cfg.LLMAPIKey, "agent.llm.model": cfg.LLMModel}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("miniProgram.%s is not configured", name)
		}
	}
	if cfg.WeixinAPIBase == "" {
		return fmt.Errorf("miniProgram.weixinAPIBase is not configured")
	}
	return nil
}

func validateMiniProgramIdentityConfig(cfg MiniProgramConfig) error {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.AppSecret) == "" || strings.TrimSpace(cfg.WeixinAPIBase) == "" {
		return fmt.Errorf("miniProgram appId, appSecret and weixinAPIBase are required")
	}
	return nil
}

func (m *Manager) signMiniProgramSession(userID string, expiresAt time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(userID + "|" + strconv.FormatInt(expiresAt.Unix(), 10)))
	mac := hmac.New(sha256.New, []byte(m.config.MiniProgram.AppSecret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) parseMiniProgramSession(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(m.config.MiniProgram.AppSecret))
	_, _ = mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) != 1 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	claims := strings.Split(string(raw), "|")
	if len(claims) != 2 || !strings.HasPrefix(claims[0], "wx_") {
		return "", false
	}
	expiresUnix, err := strconv.ParseInt(claims[1], 10, 64)
	if err != nil || time.Now().UTC().After(time.Unix(expiresUnix, 0)) {
		return "", false
	}
	return claims[0], true
}

func (m *Manager) requireMiniProgramSession(c *gin.Context) (string, bool) {
	userID, ok := m.parseMiniProgramSession(bearerToken(c))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "微信登录已失效，请重新登录"})
		return "", false
	}
	return userID, true
}

func miniProgramUserID(appID, openID string) string {
	sum := sha256.Sum256([]byte(appID + ":" + openID))
	return "wx_" + hex.EncodeToString(sum[:16])
}

func newMiniProgramTaskToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw)
	return token, hashMiniProgramToken(token), nil
}

func hashMiniProgramToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validMiniProgramTaskToken(expectedHash, token string) bool {
	actual := hashMiniProgramToken(token)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actual)) == 1
}

func bearerToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
}

func (m *Manager) failMiniProgramTask(task *schema.MiniProgramAgentTask, err error) {
	task.Status, task.Error = schema.MiniProgramTaskFailed, err.Error()
	_ = m.wdb.Db.Save(task).Error
}

func (m *Manager) miniProgramTaskResponse(task schema.MiniProgramAgentTask, token string) gin.H {
	runtimeType := ""
	if m.wdb != nil && task.PodID != "" {
		var pod schema.HymatrixPod
		if err := m.wdb.Db.Select("runtime_type").First(&pod, "id = ?", task.PodID).Error; err == nil {
			runtimeType = pod.RuntimeType
		}
	}
	result := gin.H{"taskId": task.ID, "status": task.Status, "podId": task.PodID, "runtimeType": runtimeType, "createdAt": task.CreatedAt, "error": task.Error}
	if task.QRCodeData != "" {
		result["qrCodeUrl"] = task.QRCodeData
		if !task.QRExpiresAt.IsZero() {
			result["qrExpiresAt"] = task.QRExpiresAt
		}
	}
	if token != "" {
		result["taskToken"] = token
	}
	return result
}
