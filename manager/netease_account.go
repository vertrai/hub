package manager

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func (m *Manager) adminNetEaseAccounts(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	db := m.wdb.Db.WithContext(c.Request.Context())
	if c.Request.Method == http.MethodGet {
		items := []schema.NetEaseAccount{}
		if err := db.Order("created_at DESC").Find(&items).Error; err != nil {
			c.JSON(500, gin.H{"error": "读取账户失败"})
			return
		}
		type adminItem struct {
			schema.NetEaseAccount
			Password string `json:"password"`
		}
		result := make([]adminItem, 0, len(items))
		for _, item := range items {
			result = append(result, adminItem{NetEaseAccount: item, Password: item.Password})
		}
		c.JSON(200, gin.H{"items": result})
		return
	}
	var input struct {
		Username string `json:"username" binding:"required,max=254"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Username) == "" || strings.TrimSpace(input.Password) == "" {
		c.JSON(400, gin.H{"error": "请填写有效的网易登录账号和密码"})
		return
	}
	item := schema.NetEaseAccount{ID: "nea_" + strings.ReplaceAll(uuid.NewString(), "-", ""), Username: strings.ToLower(strings.TrimSpace(input.Username)), Password: input.Password}
	result := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "username"}}, DoNothing: true}).Create(&item)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "保存账户失败"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(409, gin.H{"error": "该网易游戏账号已存在"})
		return
	}
	c.JSON(201, item)
}

func (m *Manager) getNetEaseAccount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	secret := c.GetHeader("X-Gateway-API-Key")
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		secret = parts[1]
	}
	identity, err := m.gatewayLLMIdentity(c.Request.Context(), secret)
	if err != nil {
		llmResourceFailure(c, err)
		return
	}
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	db := m.wdb.Db.WithContext(c.Request.Context())
	// The conditional UPDATE owns allocation across manager processes. The unique
	// owner index also makes concurrent requests from the same key idempotent.
	for attempt := 0; attempt < 8; attempt++ {
		var item schema.NetEaseAccount
		err = db.Where("hub_access_key_id = ?", identity.ID).First(&item).Error
		if err == nil {
			c.JSON(200, gin.H{"id": item.ID, "username": item.Username, "password": item.Password})
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		candidate := db.Model(&schema.NetEaseAccount{}).Select("id").Where("hub_access_key_id IS NULL").Order("created_at, id").Limit(1)
		now := time.Now()
		result := db.Model(&schema.NetEaseAccount{}).Where("id = (?) AND hub_access_key_id IS NULL", candidate).Updates(map[string]any{"hub_access_key_id": identity.ID, "used_at": now})
		if result.Error != nil {
			// A competing request may have just bound this same key.
			if db.Where("hub_access_key_id = ?", identity.ID).First(&item).Error == nil {
				c.JSON(200, gin.H{"id": item.ID, "username": item.Username, "password": item.Password})
				return
			}
			err = result.Error
			break
		}
		if result.RowsAffected == 0 {
			var available int64
			if err = db.Model(&schema.NetEaseAccount{}).Where("hub_access_key_id IS NULL").Count(&available).Error; err != nil {
				break
			}
			if available == 0 {
				if db.Where("hub_access_key_id = ?", identity.ID).First(&item).Error == nil {
					c.JSON(200, gin.H{"id": item.ID, "username": item.Username, "password": item.Password})
					return
				}
				c.JSON(409, gin.H{"error": "暂无可用网易游戏账户"})
				return
			}
		}
	}
	c.JSON(503, gin.H{"error": "账户领取暂时失败，请重试"})
}

func (m *Manager) adminUpdateNetEaseAccount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	var input struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Password) == "" {
		c.JSON(400, gin.H{"error": "请手动填写有效密码"})
		return
	}
	result := m.wdb.Db.WithContext(c.Request.Context()).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Model(&schema.NetEaseAccount{}).Where("id = ?", c.Param("id")).Update("password", input.Password)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "修改密码失败"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "账户不存在"})
		return
	}
	c.JSON(200, gin.H{"saved": true})
}

func (m *Manager) adminDeleteNetEaseAccount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if m.wdb == nil {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	result := m.wdb.Db.WithContext(c.Request.Context()).Where("id = ?", c.Param("id")).Delete(&schema.NetEaseAccount{})
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "删除账户失败"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "账户不存在"})
		return
	}
	c.JSON(200, gin.H{"deleted": true})
}
