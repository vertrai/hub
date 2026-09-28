package manager

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

func (m *Manager) resetPodWeixin(c *gin.Context) {
	var req struct {
		BotID string `json:"botId"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.BotID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "botId is required"})
		return
	}
	status, result := m.resetPodWeixinByID(c.Request.Context(), c.Param("id"), req.BotID)
	c.JSON(status, result)
}

func (m *Manager) resetPodWeixinByID(ctx context.Context, podID, botID string) (int, gin.H) {
	if m.wdb == nil {
		return http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"}
	}
	var pod schema.HymatrixPod
	if err := m.wdb.Db.First(&pod, "id = ?", podID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound, gin.H{"error": "pod not found"}
	} else if err != nil {
		return http.StatusInternalServerError, gin.H{"error": err.Error()}
	}
	if pod.Status != schema.PodStatusRunning || !strings.EqualFold(pod.RuntimeType, "hermes") {
		return http.StatusConflict, gin.H{"error": "Weixin reset requires a running Hermes pod"}
	}
	var bot schema.WeixinBot
	if err := m.wdb.Db.First(&bot, "id = ? AND user_id = ? AND status = ?", strings.TrimSpace(botID), pod.UserID, schema.WeixinBotStatusAvailable).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusConflict, gin.H{"error": "new Weixin bot is not available for this pod user"}
	} else if err != nil {
		return http.StatusInternalServerError, gin.H{"error": err.Error()}
	}
	client, err := NewHymatrixClient(HymatrixConfig{NodeURL: pod.NodeURL, PrivateKey: pod.PrivateKey, Module: pod.Module, Scheduler: pod.Scheduler})
	if err != nil {
		return http.StatusBadGateway, gin.H{"error": "unable to initialize runtime client"}
	}
	return m.submitPodWeixinResetResult(ctx, pod, bot, client)
}

type weixinResetSender interface {
	ResetWeixin(context.Context, string, WeixinResetInput) (string, string, error)
}

func (m *Manager) submitPodWeixinReset(c *gin.Context, pod schema.HymatrixPod, bot schema.WeixinBot, client weixinResetSender) {
	status, result := m.submitPodWeixinResetResult(c.Request.Context(), pod, bot, client)
	c.JSON(status, result)
}

func (m *Manager) submitPodWeixinResetResult(ctx context.Context, pod schema.HymatrixPod, bot schema.WeixinBot, client weixinResetSender) (int, gin.H) {
	// Persistent CAS protects all replicas and both admin and mini-program entrypoints.
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		claim := tx.Model(&schema.HymatrixPod{}).Where("id = ? AND status = ? AND weixin_reset_pending = ?", pod.ID, schema.PodStatusRunning, false).Update("weixin_reset_pending", true)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return errors.New("previous Weixin reset is still pending")
		}
		reserve := tx.Model(&schema.WeixinBot{}).Where("id = ? AND status = ? AND assigned_pod_id IS NULL AND reset_pod_id IS NULL", bot.ID, schema.WeixinBotStatusAvailable).Updates(map[string]any{"status": "reset_reserved", "reset_pod_id": pod.ID})
		if reserve.Error != nil {
			return reserve.Error
		}
		if reserve.RowsAffected != 1 {
			return errors.New("new Weixin bot was assigned concurrently")
		}
		return nil
	})
	if err != nil {
		return http.StatusConflict, gin.H{"error": err.Error()}
	}
	messageID, _, err := client.ResetWeixin(ctx, pod.PID, WeixinResetInput{AccountID: bot.AccountID, Token: bot.Token, BaseURL: bot.BaseURL, AllowedUserID: bot.AllowedUserID})
	if err != nil || strings.TrimSpace(messageID) == "" {
		// The command may have reached the runtime. Keep both credentials reserved.
		return http.StatusBadGateway, gin.H{"error": "Weixin reset outcome uncertain; administrator verification required"}
	}
	// Product completion means successful transaction delivery, not runtime health.
	// Commit assignment and release the guard together so another rebind is possible.
	err = m.finishPodWeixinReset(pod.ID, bot.ID)
	if err != nil {
		return http.StatusConflict, gin.H{"error": err.Error()}
	}
	return http.StatusAccepted, gin.H{"accepted": true, "completed": true, "messageId": messageID, "logPath": "/tmp/reset-weixin.log"}
}
