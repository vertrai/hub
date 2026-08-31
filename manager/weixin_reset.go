package manager

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

func (m *Manager) resetPodWeixin(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	var req struct {
		BotID string `json:"botId"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.BotID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "botId is required"})
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
	if pod.Status != schema.PodStatusRunning || !strings.EqualFold(pod.RuntimeType, "hermes") {
		c.JSON(http.StatusConflict, gin.H{"error": "Weixin reset requires a running Hermes pod"})
		return
	}
	var bot schema.WeixinBot
	if err := m.wdb.Db.First(&bot, "id = ? AND user_id = ? AND status = ?", strings.TrimSpace(req.BotID), pod.UserID, schema.WeixinBotStatusAvailable).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusConflict, gin.H{"error": "new Weixin bot is not available for this pod user"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	client, err := NewHymatrixClient(HymatrixConfig{NodeURL: pod.NodeURL, PrivateKey: pod.PrivateKey, Module: pod.Module, Scheduler: pod.Scheduler})
	if err == nil {
		_, _, err = client.ResetWeixin(c.Request.Context(), pod.PID, WeixinResetInput{AccountID: bot.AccountID, Token: bot.Token, BaseURL: bot.BaseURL, AllowedUserID: bot.AllowedUserID})
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if pod.WeixinBotID != "" {
			if err := tx.Model(&schema.WeixinBot{}).Where("id = ? AND assigned_pod_id = ?", pod.WeixinBotID, pod.ID).Updates(map[string]any{"status": schema.WeixinBotStatusAvailable, "assigned_pod_id": nil}).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&schema.WeixinBot{}).Where("id = ? AND status = ?", bot.ID, schema.WeixinBotStatusAvailable).Updates(map[string]any{"status": schema.WeixinBotStatusAssigned, "assigned_pod_id": pod.ID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("new Weixin bot was assigned concurrently")
		}
		return tx.Model(&schema.HymatrixPod{}).Where("id = ?", pod.ID).Update("weixin_bot_id", bot.ID).Error
	})
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true, "logPath": "/tmp/reset-weixin.log"})
}
