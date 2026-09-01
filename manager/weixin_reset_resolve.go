package manager

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
)

// Admin-only acknowledgement after inspecting the runtime and testing the active bot.
// botId must identify a credential already reserved by this Pod, never an arbitrary bot.
func (m *Manager) resolvePodWeixinReset(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "database unavailable"})
		return
	}
	var input struct {
		BotID    string `json:"botId"`
		Verified bool   `json:"verified"`
	}
	if c.ShouldBindJSON(&input) != nil || input.BotID == "" || !input.Verified {
		c.JSON(400, gin.H{"error": "verified=true and actual active botId are required"})
		return
	}
	err := m.finishPodWeixinReset(c.Param("id"), input.BotID)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "无法核验：请检查待处理状态和实际 Bot 归属"})
		return
	}
	c.JSON(200, gin.H{"resolved": true})
}

// finishPodWeixinReset atomically commits a delivered binding and clears its guard.
// It never delivers another transaction and only promotes a reserved identity.
func (m *Manager) finishPodWeixinReset(podID, botID string) error {
	return m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var pod schema.HymatrixPod
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&pod, "id = ? AND weixin_reset_pending = ?", podID, true).Error; err != nil {
			return err
		}
		var bot schema.WeixinBot
		if err := tx.Where("id = ? AND user_id = ? AND status IN ?", botID, pod.UserID, []string{"reset_reserved", schema.WeixinBotStatusAssigned}).Where("assigned_pod_id = ? OR reset_pod_id = ?", pod.ID, pod.ID).First(&bot).Error; err != nil {
			return errors.New("bot is not reserved by this pod")
		}
		if err := tx.Model(&schema.WeixinBot{}).Where("id <> ? AND (assigned_pod_id = ? OR reset_pod_id = ?)", bot.ID, pod.ID, pod.ID).Updates(map[string]any{"status": "retired", "assigned_pod_id": nil, "reset_pod_id": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&bot).Updates(map[string]any{"status": schema.WeixinBotStatusAssigned, "assigned_pod_id": pod.ID, "reset_pod_id": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&pod).Updates(map[string]any{"weixin_bot_id": bot.ID, "weixin_reset_pending": false}).Error
	})
}
