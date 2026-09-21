package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) ownedSetupAgent(c *gin.Context) (schema.WebAgent, bool) {
	var a schema.WebAgent
	if err := m.wdb.Db.First(&a, "id = ? AND user_id = ?", c.Param("id"), mustWebUser(c)).Error; err != nil {
		c.JSON(404, gin.H{"error": "assistant not found"})
		return a, false
	}
	if a.State != "awaiting_setup" || a.Desired != "running" {
		c.JSON(409, gin.H{"error": "assistant is not waiting for channel configuration"})
		return a, false
	}
	return a, true
}
func (m *Manager) configureWebAgent(c *gin.Context) {
	var req struct {
		EnableTelegram bool   `json:"enableTelegram"`
		BotToken       string `json:"botToken"`
		WeixinBotID    string `json:"weixinBotId"`
	}
	if c.ShouldBindJSON(&req) != nil || (!req.EnableTelegram && strings.TrimSpace(req.WeixinBotID) == "") {
		c.JSON(400, gin.H{"error": "choose Telegram or Weixin"})
		return
	}
	a, ok := m.ownedSetupAgent(c)
	if !ok {
		return
	}
	username := ""
	req.BotToken = strings.TrimSpace(req.BotToken)
	if req.EnableTelegram && req.BotToken != "" {
		if len(req.BotToken) > 256 || strings.ContainsAny(req.BotToken, "\r\n/ ?#") {
			c.JSON(400, gin.H{"error": "invalid Telegram bot token"})
			return
		}
		request, requestErr := http.NewRequestWithContext(c.Request.Context(), "GET", "https://api.telegram.org/bot"+req.BotToken+"/getMe", nil)
		if requestErr != nil {
			c.JSON(400, gin.H{"error": "invalid Telegram bot token"})
			return
		}
		response, err := telegramAPIHTTPClient.Do(request)
		if err != nil {
			c.JSON(502, gin.H{"error": "cannot verify Telegram bot"})
			return
		}
		defer response.Body.Close()
		var result struct {
			OK     bool `json:"ok"`
			Result struct {
				Username string `json:"username"`
			} `json:"result"`
		}
		if json.NewDecoder(response.Body).Decode(&result) != nil || response.StatusCode != 200 || !result.OK || result.Result.Username == "" {
			c.JSON(400, gin.H{"error": "Telegram bot token was not recognized"})
			return
		}
		username = result.Result.Username
	}
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ? AND user_id = ?", a.ID, mustWebUser(c)).Error; err != nil {
			return err
		}
		if a.State != "awaiting_setup" || a.Desired != "running" {
			return errors.New("configuration already submitted")
		}
		var pod schema.HymatrixPod
		if err := tx.First(&pod, "id = ? AND user_id = ?", a.PodID, a.UserID).Error; err != nil {
			return err
		}
		if pod.Status != schema.PodStatusSpawned || strings.HasPrefix(pod.PID, "pending_") || pod.PID == "" {
			return errors.New("spawn has not completed")
		}
		if req.EnableTelegram && req.BotToken == "" && pod.BotToken == "" {
			return errors.New("Telegram bot token required")
		}
		if req.WeixinBotID != "" {
			if req.WeixinBotID != a.WeixinAuthorizedBotID {
				return errors.New("scan Weixin for this assistant first")
			}
			r := tx.Model(&schema.WeixinBot{}).Where("id = ? AND user_id = ? AND status = ?", req.WeixinBotID, a.UserID, schema.WeixinBotStatusAvailable).Updates(map[string]any{"status": schema.WeixinBotStatusAssigned, "assigned_pod_id": pod.ID})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return errors.New("Weixin authorization is unavailable")
			}
		}
		if err := tx.Model(&pod).Updates(map[string]any{"weixin_bot_id": req.WeixinBotID, "bot_token": func() string {
			if req.EnableTelegram {
				if req.BotToken == "" {
					return pod.BotToken
				}
				return req.BotToken
			}
			return ""
		}()}).Error; err != nil {
			return err
		}
		if req.EnableTelegram && req.BotToken == "" {
			username = a.BotUsername
		}
		return tx.Model(&a).Updates(map[string]any{"bot_username": username, "channel_configured": true, "enable_telegram": req.EnableTelegram, "state": "queued", "phase": "configured"}).Error
	})
	if err != nil {
		c.JSON(409, gin.H{"error": "cannot submit channel configuration; refresh and check authorization"})
		return
	}
	c.JSON(202, gin.H{"status": "starting"})
}
func (m *Manager) startWebWeixin(c *gin.Context) {
	a, ok := m.ownedSetupAgent(c)
	if !ok {
		return
	}
	result, err := m.createWeixinOnboarding(c.Request.Context(), a.UserID)
	if err != nil {
		c.JSON(502, gin.H{"error": "cannot request Weixin QR code"})
		return
	}
	m.weixinMu.Lock()
	attempt := m.weixinAttempts[result.AttemptID]
	attempt.WebAgentID = a.ID
	m.weixinAttempts[result.AttemptID] = attempt
	m.weixinMu.Unlock()
	c.JSON(201, gin.H{"attemptId": result.AttemptID, "qrImage": result.QRImage, "expiresAt": result.ExpiresAt})
}
func (m *Manager) pollWebWeixin(c *gin.Context) {
	a, ok := m.ownedSetupAgent(c)
	if !ok {
		return
	}
	m.weixinMu.Lock()
	attempt, exists := m.weixinAttempts[c.Param("attempt")]
	m.weixinMu.Unlock()
	if !exists || attempt.UserID != a.UserID || attempt.WebAgentID != a.ID {
		c.JSON(404, gin.H{"error": "authorization expired; request a new QR code"})
		return
	}
	state, status, err := m.pollWeixinOnboardingAttempt(c.Request.Context(), attempt.ID)
	if err != nil {
		c.JSON(status, gin.H{"error": "cannot update Weixin authorization; retry or request a new code"})
		return
	}
	if state.BotID != "" {
		result := m.wdb.Db.Model(&schema.WebAgent{}).Where("id = ? AND user_id = ? AND state = ?", a.ID, a.UserID, "awaiting_setup").Update("weixin_authorized_bot_id", state.BotID)
		if result.Error != nil {
			log.Error("persist web Weixin authorization failed; verify database migrations", "agent_id", a.ID, "error", result.Error)
			c.JSON(500, gin.H{"code": "weixin_authorization_save_failed", "error": "cannot save Weixin authorization; please retry"})
			return
		}
		if result.RowsAffected != 1 {
			c.JSON(409, gin.H{"code": "assistant_configuration_changed", "error": "assistant configuration changed; refresh before continuing"})
			return
		}
	}
	c.JSON(status, gin.H{"state": state.State, "botId": state.BotID})
}

func (m *Manager) acquireWebTelegram(c *gin.Context) {
	a, ok := m.ownedSetupAgent(c)
	if !ok {
		return
	}
	username := ""
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, "id = ?", a.ID).Error; err != nil {
			return err
		}
		if a.State != "awaiting_setup" || a.Desired != "running" {
			return errors.New("not awaiting setup")
		}
		var pod schema.HymatrixPod
		if err := tx.First(&pod, "id = ? AND user_id = ?", a.PodID, a.UserID).Error; err != nil {
			return err
		}
		if pod.BotToken != "" && a.BotUsername != "" {
			username = a.BotUsername
			return nil
		}
		var key schema.AccessKey
		if err := tx.First(&key, "id = ? AND user_id = ?", a.AccessKeyID, a.UserID).Error; err != nil {
			return err
		}
		bot, err := m.resources.telegramBotDetails(c.Request.Context(), key.Secret)
		if err != nil {
			return err
		}
		if strings.TrimSpace(bot.BotToken) == "" || strings.TrimSpace(bot.Username) == "" {
			return errors.New("no usable Telegram bot")
		}
		username = bot.Username
		if err := tx.Model(&pod).Update("bot_token", bot.BotToken).Error; err != nil {
			return err
		}
		return tx.Model(&a).Update("bot_username", username).Error
	})
	if err != nil {
		c.JSON(409, gin.H{"error": "No Telegram bot could be allocated. Enter your own Bot Token or retry later."})
		return
	}
	c.JSON(200, gin.H{"url": telegramBotLink(username)})
}
