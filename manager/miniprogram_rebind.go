package manager

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
)

// Every operation resolves the task from the signed session, never a client user/pod ID.
func (m *Manager) miniProgramRebindTask(c *gin.Context) (schema.MiniProgramAgentTask, bool) {
	var task schema.MiniProgramAgentTask
	userID, ok := m.requireMiniProgramSession(c)
	if !ok {
		return task, false
	}
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return task, false
	}
	err := m.wdb.Db.First(&task, "id = ? AND user_id = ?", c.Param("taskId"), userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "助手不存在"})
		return task, false
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "读取助手失败"})
		return task, false
	}
	var pod schema.HymatrixPod
	if task.Status != schema.MiniProgramTaskRunning || m.wdb.Db.First(&pod, "id = ? AND user_id = ?", task.PodID, userID).Error != nil || pod.Status != schema.PodStatusRunning || !strings.EqualFold(pod.RuntimeType, "hermes") {
		c.JSON(409, gin.H{"error": "仅运行中的 Hermes 助手可以更换微信"})
		return task, false
	}
	c.Header("Cache-Control", "no-store")
	if pod.WeixinResetPending && c.Request.Method == http.MethodPost && c.Param("attempt") == "" {
		c.JSON(409, gin.H{"error": "上次换绑尚待管理员核验，请勿重复提交"})
		return task, false
	}
	return task, true
}

func (m *Manager) startMiniProgramRebind(c *gin.Context) {
	task, ok := m.miniProgramRebindTask(c)
	if !ok {
		return
	}
	result, err := m.createWeixinOnboarding(c.Request.Context(), task.UserID)
	if err != nil {
		c.JSON(502, gin.H{"error": "获取连接码失败，请重试"})
		return
	}
	m.weixinMu.Lock()
	attempt, exists := m.weixinAttempts[result.AttemptID]
	if exists {
		attempt.MiniProgramTaskID = task.ID
		attempt.AutoRebind = true
		attempt.RebindState = "waiting"
		m.weixinAttempts[attempt.ID] = attempt
	}
	m.weixinMu.Unlock()
	if !exists {
		c.JSON(409, gin.H{"error": "连接码已被其他请求替换，请重试"})
		return
	}
	go m.runMiniProgramRebind(task, result.AttemptID)
	c.JSON(201, gin.H{"attemptId": result.AttemptID, "qrImage": result.QRImage, "expiresAt": result.ExpiresAt, "intervalSeconds": result.IntervalSeconds})
}

func ownsMiniProgramRebind(a weixinAttempt, task schema.MiniProgramAgentTask) bool {
	return a.UserID == task.UserID && a.MiniProgramTaskID != "" && a.MiniProgramTaskID == task.ID
}

func (m *Manager) miniProgramRebindAttempt(c *gin.Context, task schema.MiniProgramAgentTask) (weixinAttempt, bool) {
	m.weixinMu.Lock()
	a, ok := m.weixinAttempts[c.Param("attempt")]
	m.weixinMu.Unlock()
	if !ok || !ownsMiniProgramRebind(a, task) {
		c.JSON(404, gin.H{"error": "连接码已失效，请重新获取"})
		return a, false
	}
	return a, true
}

func (m *Manager) pollMiniProgramRebind(c *gin.Context) {
	task, ok := m.miniProgramRebindTask(c)
	if !ok {
		return
	}
	a, ok := m.miniProgramRebindAttempt(c, task)
	if !ok {
		return
	}
	if a.AutoRebind {
		c.JSON(200, gin.H{"state": a.RebindState})
		return
	}
	state, status, err := m.pollWeixinOnboardingAttempt(c.Request.Context(), c.Param("attempt"))
	if err != nil {
		c.JSON(status, gin.H{"error": "查询扫码状态失败，请重试"})
		return
	}
	// No credentials or internal bot/user IDs are exposed to the mini program.
	c.JSON(status, gin.H{"state": state.State})
}

func (m *Manager) cancelMiniProgramRebind(c *gin.Context) {
	task, ok := m.miniProgramRebindTask(c)
	if !ok {
		return
	}
	m.weixinMu.Lock()
	a, exists := m.weixinAttempts[c.Param("attempt")]
	if exists && ownsMiniProgramRebind(a, task) && !a.Submitting {
		delete(m.weixinAttempts, a.ID)
	}
	m.weixinMu.Unlock()
	if exists && (!ownsMiniProgramRebind(a, task) || a.Submitting) {
		c.JSON(409, gin.H{"error": "无法取消此换绑请求"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (m *Manager) confirmMiniProgramRebind(c *gin.Context) {
	task, ok := m.miniProgramRebindTask(c)
	if !ok {
		return
	}
	m.weixinMu.Lock()
	a, exists := m.weixinAttempts[c.Param("attempt")]
	valid := exists && ownsMiniProgramRebind(a, task) && !a.AutoRebind && !a.Submitting && a.Credentials != nil && time.Now().Before(a.CredentialExpiresAt)
	if valid {
		a.Submitting = true
		m.weixinAttempts[a.ID] = a
	}
	m.weixinMu.Unlock()
	if !valid {
		c.JSON(409, gin.H{"error": "请重新扫码确认，或等待当前换绑提交完成"})
		return
	}
	status, result := m.resetPodWeixinByID(c.Request.Context(), task.PodID, a.Credentials.BotID)
	c.JSON(status, result)
	m.consumeWeixinAttempt(a.ID)
}

// The worker owns provider polling and submission, independently of the page lifecycle.
func (m *Manager) runMiniProgramRebind(task schema.MiniProgramAgentTask, id string) {
	m.runMiniProgramRebindWithReset(task, id, m.resetPodWeixinByID)
}

func (m *Manager) runMiniProgramRebindWithReset(task schema.MiniProgramAgentTask, id string, reset func(context.Context, string, string) (int, gin.H)) {
	ctx, cancel := context.WithTimeout(context.Background(), weixinAttemptLifetime+time.Minute)
	defer cancel()
	for {
		m.weixinMu.Lock()
		a, exists := m.weixinAttempts[id]
		m.weixinMu.Unlock()
		if !exists || !ownsMiniProgramRebind(a, task) || a.Submitting {
			return
		}
		state, status, err := m.pollWeixinOnboardingAttempt(ctx, id)
		if err == nil && state.State == "connected" {
			m.weixinMu.Lock()
			a, exists = m.weixinAttempts[id]
			if !exists || a.Submitting || a.Credentials == nil {
				m.weixinMu.Unlock()
				return
			}
			a.Submitting = true
			a.RebindState = "submitting"
			m.weixinAttempts[id] = a
			m.weixinMu.Unlock()
			code, result := reset(ctx, task.PodID, a.Credentials.BotID)
			final := "uncertain"
			if code == http.StatusAccepted && result["completed"] == true {
				final = "completed"
			}
			m.weixinMu.Lock()
			if current, ok := m.weixinAttempts[id]; ok {
				current.RebindState = final
				current.Credentials = nil
				m.weixinAttempts[id] = current
			}
			m.weixinMu.Unlock()
			return // Never retry a reset whose delivery may have succeeded.
		}
		if status == 404 || status == 410 || state.State == "expired" {
			return
		}
		m.weixinMu.Lock()
		if current, ok := m.weixinAttempts[id]; ok {
			if err == nil {
				current.RebindState = state.State
			}
			if err != nil && status == 409 {
				current.RebindState = "failed"
			}
			m.weixinAttempts[id] = current
		}
		m.weixinMu.Unlock()
		if err != nil && status == 409 {
			return
		}
		select {
		case <-ctx.Done():
			m.consumeWeixinAttempt(id)
			return
		case <-time.After(2 * time.Second):
		}
	}
}
