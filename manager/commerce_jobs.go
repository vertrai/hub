package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) runJobs() {
	if m.wdb == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.commerceContext.Done():
			return
		case <-ticker.C:
			if err := m.runCommerceJob(m.commerceContext); err != nil {
				log.Error("commerce worker", "err", err)
			}
		}
	}
}

func (m *Manager) runCommerceJob(ctx context.Context) error {
	// Never automatically repeat an interrupted irreversible resource/Spawn call.
	if err := m.wdb.Db.Model(&schema.WebAgent{}).Where("state = ? AND lease_until < ?", "processing", time.Now()).Updates(map[string]any{"state": "needs_review", "error": "worker interrupted; reconcile remote operation before retry"}).Error; err != nil {
		return err
	}
	var a schema.WebAgent
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where(`state = ? OR (state = ? AND desired = ?) OR (state = ? AND desired = ?) OR
 (state IN ('failed','needs_review','awaiting_setup') AND desired = 'stopped' AND EXISTS
 (SELECT 1 FROM manager_hymatrix_pods p WHERE p.id = manager_web_agents.pod_id
 AND p.p_id <> '' AND p.p_id NOT LIKE 'pending_%' AND p.status <> 'stopped'))`, "queued", "running", "stopped", "stopped", "running").Order("created_at").First(&a).Error
		if err != nil {
			return err
		}
		until := time.Now().Add(10 * time.Minute)
		claimedState := a.State
		r := tx.Model(&a).Where("state = ?", a.State).Updates(map[string]any{"state": "processing", "lease_until": until})
		a.State = claimedState
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	err = m.reconcileWebAgent(ctx, &a)
	if err != nil {
		state := "failed"
		if a.Phase == "allocating" || a.Phase == "spawning" || a.Phase == "starting" {
			state = "needs_review"
		}
		return m.wdb.Db.Model(&a).Updates(map[string]any{"state": state, "error": err.Error(), "lease_until": nil}).Error
	}
	return nil
}
func (m *Manager) agentPhase(a *schema.WebAgent, phase string) error {
	a.Phase = phase
	return m.wdb.Db.Model(a).Update("phase", phase).Error
}
func (m *Manager) finishWebAgent(a *schema.WebAgent, state string) error {
	return m.wdb.Db.Model(a).Updates(map[string]any{"state": state, "error": "", "lease_until": nil}).Error
}
func (m *Manager) reconcileWebAgent(ctx context.Context, a *schema.WebAgent) error {
	var pod schema.HymatrixPod
	if a.PodID != "" {
		if err := m.wdb.Db.First(&pod, "id = ?", a.PodID).Error; err != nil {
			return err
		}
	}
	if a.Desired == "stopped" {
		if pod.PID != "" {
			if err := m.stopHymatrixVM(ctx, pod.AdminURL, pod.NodeURL, pod.PID); err != nil {
				return err
			}
			if err := m.wdb.Db.Model(&pod).Update("status", "stopped").Error; err != nil {
				return err
			}
		}
		if a.State == "needs_review" {
			return m.wdb.Db.Model(a).Updates(map[string]any{"state": "needs_review", "lease_until": nil}).Error
		}
		return m.finishWebAgent(a, "stopped")
	}
	if pod.Status == "stopped" {
		if err := m.resumeHymatrixVM(ctx, pod.AdminURL, pod.NodeURL, pod.PID); err != nil {
			return err
		}
		if err := m.wdb.Db.Model(&pod).Update("status", schema.PodStatusRunning).Error; err != nil {
			return err
		}
		if a.Phase == "ready" {
			return m.finishWebAgent(a, "running")
		}
	}
	cfg := m.config.Deployment
	if a.AccessKeyID == "" {
		if err := m.agentPhase(a, "allocating"); err != nil {
			return err
		}
		created, _, err := m.resources.createAccessKey(ctx, a.UserID, ResourceScopes{AllowGoogle: true, AllowBrowser: true, AllowTelegram: true})
		if err != nil {
			return err
		}
		key := schema.AccessKey{ID: commerceID("mak_"), UserID: a.UserID, ResourceKeyID: created.AccessKey.ID, KeyPrefix: created.AccessKey.KeyPrefix, Secret: created.GatewayAPIKey, Status: "available"}
		if err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&key).Error; err != nil {
				return err
			}
			return tx.Model(a).Updates(map[string]any{"access_key_id": key.ID, "phase": "resources"}).Error
		}); err != nil {
			return err
		}
		a.AccessKeyID = key.ID
		a.Phase = "resources"
	}
	var key schema.AccessKey
	if err := m.wdb.Db.First(&key, "id = ?", a.AccessKeyID).Error; err != nil {
		return err
	}
	if err := m.agentPhase(a, "resources"); err != nil {
		return err
	}
	resource, err := m.hermesLLMResource(ctx, key.Secret, "hub-chat")
	if err != nil {
		return err
	}
	if a.PodID == "" {
		info, err := fetchHymatrixNodeInfo(ctx, cfg.NodeURL)
		if err != nil {
			return err
		}
		var catalog schema.AgentCatalogEntry
		name := a.Product
		query := m.wdb.Db.Where("id = ?", a.CatalogID)
		if a.CatalogID == "" {
			query = m.wdb.Db.Where("product_id = ?", a.Product)
		}
		if err := query.First(&catalog).Error; err == nil {
			name = catalog.Name
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		pod = schema.HymatrixPod{ID: commerceID("pod_"), UserID: a.UserID, Name: name, RuntimeType: "hermes", PID: "pending_" + a.ID, Status: schema.PodStatusSpawned, NodeURL: cfg.NodeURL, AdminURL: cfg.AdminURL, PrivateKey: cfg.PrivateKey, Module: a.Module, Scheduler: info.Node.AccountID, AccessKeyID: key.ID, GatewayAPIKey: key.Secret, LLMAPIKey: resource.APIKey, LLMBaseURL: resource.BaseURL, LLMModel: resource.Model, LLMProvider: resource.Provider}
		if err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&pod).Error; err != nil {
				return err
			}
			r := tx.Model(&key).Where("status = ?", "available").Updates(map[string]any{"status": "assigned", "assigned_pod_id": pod.ID})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return fmt.Errorf("access key already assigned")
			}
			return tx.Model(a).Update("pod_id", pod.ID).Error
		}); err != nil {
			return err
		}
		a.PodID = pod.ID
	}
	client, err := m.commerceHymatrix(HymatrixConfig{NodeURL: pod.NodeURL, PrivateKey: pod.PrivateKey, Module: pod.Module, Scheduler: pod.Scheduler, LLMAPIKey: resource.APIKey, LLMBaseURL: resource.BaseURL, LLMModel: resource.Model, LLMProvider: resource.Provider})
	if err != nil {
		return err
	}
	if pod.PID == "pending_"+a.ID {
		if err = m.agentPhase(a, "spawning"); err != nil {
			return err
		}
		pid, err := client.Spawn(ctx, PodSpawnInput{RuntimeType: "hermes"})
		if err != nil {
			return err
		}
		if err = m.wdb.Db.Model(&pod).Updates(map[string]any{"p_id": pid, "status": schema.PodStatusSpawned}).Error; err != nil {
			return err
		}
		pod.PID = pid
	}
	if !a.ChannelConfigured && a.Phase != "ready" {
		if err := m.agentPhase(a, "awaiting_setup"); err != nil {
			return err
		}
		return m.finishWebAgent(a, "awaiting_setup")
	}
	var weixin schema.WeixinBot
	if pod.WeixinBotID != "" {
		if err := m.wdb.Db.First(&weixin, "id = ? AND user_id = ? AND assigned_pod_id = ?", pod.WeixinBotID, a.UserID, pod.ID).Error; err != nil {
			return err
		}
	}
	if a.EnableTelegram && pod.BotToken == "" {
		bot, err := m.resources.telegramBotDetails(ctx, key.Secret)
		if err != nil {
			return err
		}
		pod.BotToken = bot.BotToken
		if err := m.wdb.Db.Model(&pod).Update("bot_token", bot.BotToken).Error; err != nil {
			return err
		}
		if err := m.wdb.Db.Model(a).Update("bot_username", bot.Username).Error; err != nil {
			return err
		}
	}
	// A cancellation may arrive while provisioning. Check before starting; the
	// next worker iteration applies any subsequent desired-state change.
	var latest schema.WebAgent
	if err = m.wdb.Db.First(&latest, "id = ?", a.ID).Error; err != nil {
		return err
	}
	if latest.Desired == "stopped" {
		a.Desired = "stopped"
		return m.reconcileWebAgent(ctx, a)
	}
	if err = m.agentPhase(a, "starting"); err != nil {
		return err
	}
	if err = client.StartAgent(ctx, pod.PID, PodStartInput{GatewayURL: cfg.GatewayURL, GatewayAPIKey: key.Secret, BotToken: pod.BotToken, HermesGatewayToken: cfg.HermesGatewayToken, WeixinAccountID: weixin.AccountID, WeixinToken: weixin.Token, WeixinBaseURL: weixin.BaseURL, WeixinAllowedUsers: weixin.AllowedUserID}); err != nil {
		return err
	}
	if err = m.wdb.Db.Model(&pod).Update("status", schema.PodStatusRunning).Error; err != nil {
		return err
	}
	if err = m.agentPhase(a, "ready"); err != nil {
		return err
	}
	return m.finishWebAgent(a, "running")
}
