package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type resourceAlertRule struct {
	Pool        string `json:"pool"`
	Enabled     bool   `json:"enabled"`
	Threshold   int64  `json:"threshold"`
	RepeatHours int    `json:"repeatHours"`
	Telegram    bool   `json:"telegram"`
	Email       bool   `json:"email"`
}
type resourceAlertSettings struct {
	Rules          []resourceAlertRule `json:"rules"`
	TelegramToken  string              `json:"telegramToken"`
	TelegramChatID string              `json:"telegramChatId"`
	SMTPHost       string              `json:"smtpHost"`
	SMTPPort       int                 `json:"smtpPort"`
	SMTPUser       string              `json:"smtpUser"`
	SMTPPassword   string              `json:"smtpPassword"`
	SMTPMode       string              `json:"smtpMode"`
	EmailFrom      string              `json:"emailFrom"`
	EmailTo        string              `json:"emailTo"`
}

var alertPoolNames = map[string]string{"xbox-child": "Xbox Child 资源池", "netease": "网易游戏账户资源池"}

func defaultResourceAlerts() resourceAlertSettings {
	return resourceAlertSettings{Rules: []resourceAlertRule{{Pool: "xbox-child", Threshold: 3, RepeatHours: 24}, {Pool: "netease", Threshold: 3, RepeatHours: 24}}, SMTPPort: 587, SMTPMode: "starttls"}
}
func (m *Manager) readResourceAlerts(ctx context.Context) (resourceAlertSettings, error) {
	var row schema.ResourceAlertConfig
	err := m.wdb.Db.WithContext(ctx).First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultResourceAlerts(), nil
	}
	if err != nil {
		return resourceAlertSettings{}, err
	}
	var cfg resourceAlertSettings
	err = json.Unmarshal([]byte(row.Config), &cfg)
	return cfg, err
}
func validateResourceAlerts(cfg resourceAlertSettings) error {
	seen := map[string]bool{}
	for _, r := range cfg.Rules {
		if alertPoolNames[r.Pool] == "" || seen[r.Pool] || r.Threshold < 0 || r.Threshold > 1000000 || r.RepeatHours < 1 || r.RepeatHours > 720 {
			return errors.New("资源池、阈值或提醒间隔无效（间隔为 1–720 小时）")
		}
		seen[r.Pool] = true
		if r.Enabled && !r.Telegram && !r.Email {
			return errors.New("启用的资源池至少选择一个通知渠道")
		}
		if r.Enabled && r.Telegram && (cfg.TelegramToken == "" || strings.TrimSpace(cfg.TelegramChatID) == "") {
			return errors.New("请配置 Telegram Bot Token 和 Chat ID")
		}
		if r.Enabled && r.Email {
			if cfg.SMTPHost == "" || strings.ContainsAny(cfg.SMTPHost, "/\r\n ") || cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 || (cfg.SMTPMode != "tls" && cfg.SMTPMode != "starttls") {
				return errors.New("请配置有效的 SMTP 主机、端口和 TLS 模式")
			}
			for _, v := range []string{cfg.EmailFrom, cfg.EmailTo} {
				a, e := mail.ParseAddress(v)
				if e != nil || a.Address != v || strings.ContainsAny(v, "\r\n") {
					return errors.New("发件人和收件人须填写单个有效邮箱地址")
				}
			}
		}
	}
	if len(seen) != len(alertPoolNames) {
		return errors.New("请配置全部支持的资源池")
	}
	if cfg.TelegramToken != "" {
		for _, c := range cfg.TelegramToken {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ':' || c == '_' || c == '-') {
				return errors.New("Telegram Bot Token 格式无效")
			}
		}
	}
	return nil
}
func (m *Manager) alertInventory(ctx context.Context, pool string) (int64, error) {
	var count int64
	db := m.wdb.Db.WithContext(ctx)
	switch pool {
	case "xbox-child":
		db = db.Model(&schema.XboxChild{})
	case "netease":
		db = db.Model(&schema.NetEaseAccount{})
	default:
		return 0, errors.New("unknown pool")
	}
	err := db.Where("hub_access_key_id IS NULL").Count(&count).Error
	return count, err
}
func (m *Manager) adminResourceAlerts(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !m.catalogDB(c) {
		return
	}
	cfg, err := m.readResourceAlerts(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "读取告警配置失败，请确认已执行数据库迁移"})
		return
	}
	if c.Request.Method == http.MethodPut {
		var input resourceAlertSettings
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "无效的告警配置"})
			return
		}
		if input.TelegramToken == "" {
			input.TelegramToken = cfg.TelegramToken
		}
		if input.SMTPPassword == "" {
			input.SMTPPassword = cfg.SMTPPassword
		}
		if err = validateResourceAlerts(input); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		data, _ := json.Marshal(input)
		err = m.wdb.Db.WithContext(c.Request.Context()).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Clauses(clause.OnConflict{UpdateAll: true}).Create(&schema.ResourceAlertConfig{ID: 1, Config: string(data)}).Error
		if err != nil {
			c.JSON(500, gin.H{"error": "保存告警配置失败"})
			return
		}
		c.JSON(200, gin.H{"saved": true})
		return
	}
	inventory := map[string]int64{}
	for pool := range alertPoolNames {
		n, e := m.alertInventory(c.Request.Context(), pool)
		if e != nil {
			c.JSON(500, gin.H{"error": "读取资源库存失败"})
			return
		}
		inventory[pool] = n
	}
	var deliveries []schema.ResourceAlertDelivery
	if err = m.wdb.Db.WithContext(c.Request.Context()).Order("id").Find(&deliveries).Error; err != nil {
		c.JSON(500, gin.H{"error": "读取通知记录失败"})
		return
	}
	tokenSet, passwordSet := cfg.TelegramToken != "", cfg.SMTPPassword != ""
	cfg.TelegramToken = ""
	cfg.SMTPPassword = ""
	c.JSON(200, gin.H{"settings": cfg, "inventory": inventory, "deliveries": deliveries, "telegramTokenConfigured": tokenSet, "smtpPasswordConfigured": passwordSet})
}
func (m *Manager) runResourceAlerts(ctx context.Context) {
	if m.wdb == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := m.checkResourceAlerts(ctx, time.Now(), sendResourceAlert); err != nil && ctx.Err() == nil {
			log.Error("resource alert check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type resourceAlertSender func(context.Context, resourceAlertSettings, string, string) error

func (m *Manager) checkResourceAlerts(ctx context.Context, now time.Time, send resourceAlertSender) error {
	cfg, err := m.readResourceAlerts(ctx)
	if err != nil {
		return err
	}
	for _, r := range cfg.Rules {
		n, err := m.alertInventory(ctx, r.Pool)
		if err != nil {
			return err
		}
		for _, ch := range []string{"telegram", "email"} {
			db := m.wdb.Db.WithContext(ctx)
			id := r.Pool + ":" + ch
			enabled := r.Enabled && ((ch == "telegram" && r.Telegram) || (ch == "email" && r.Email))
			if !enabled || n > r.Threshold {
				if err = db.Model(&schema.ResourceAlertDelivery{}).Where("id = ? AND lease_until <= ?", id, now).Updates(map[string]any{"next_attempt": time.Time{}, "last_error": ""}).Error; err != nil {
					return err
				}
				continue
			}
			state := schema.ResourceAlertDelivery{ID: id}
			if err = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
				return err
			}
			lease := now.Add(2 * time.Minute)
			claim := db.Model(&schema.ResourceAlertDelivery{}).Where("id = ? AND next_attempt <= ? AND lease_until <= ?", id, now, now).Update("lease_until", lease)
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected == 0 {
				continue
			}
			body := fmt.Sprintf("【资源库存告警】%s\n当前可用：%d\n告警阈值：≤ %d\n请前往 Hub 后台手动补充资源。", alertPoolNames[r.Pool], n, r.Threshold)
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			sendErr := send(sendCtx, cfg, ch, body)
			cancel()
			updates := map[string]any{"lease_until": time.Time{}, "last_error": "", "next_attempt": now.Add(time.Duration(r.RepeatHours) * time.Hour)}
			if sendErr != nil {
				updates["last_error"] = "发送失败，请检查渠道配置与网络；5 分钟后重试"
				updates["next_attempt"] = now.Add(5 * time.Minute)
			} else {
				updates["last_sent"] = now
			}
			if err = db.Model(&schema.ResourceAlertDelivery{}).Where("id = ? AND lease_until = ?", id, lease).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// SMTP uses bounded network deadlines; credentials are sent only over TLS.
func smtpAddress(cfg resourceAlertSettings) string {
	return net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
}
