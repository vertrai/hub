package manager

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func wechatCopy(a schema.AgentCatalogEntry) *schema.WechatCatalogCopy {
	return &schema.WechatCatalogCopy{Name: a.Name, LogoURL: a.LogoURL, Intro: a.Intro, Summary: a.Summary, Capabilities: a.Capabilities, CompatibilityNote: a.CompatibilityNote}
}
func wechatCatalogView(a schema.AgentCatalogEntry) schema.AgentCatalogEntry {
	if w := a.Wechat; w != nil {
		a.Name = w.Name
		a.LogoURL = w.LogoURL
		a.Intro = w.Intro
		a.Summary = w.Summary
		a.Capabilities = w.Capabilities
		a.CompatibilityNote = w.CompatibilityNote
	}
	return a
}
func validCatalogScope(scope string) bool {
	return scope == "core" || scope == "wechat" || scope == "web"
}

// Each editor can update only its owned fields. Merge against a locked current row,
// never the editor's stale copy, so independent channel saves cannot overwrite each other.
func mergeCatalogScope(old, input schema.AgentCatalogEntry, scope string) (schema.AgentCatalogEntry, error) {
	a := old
	switch scope {
	case "core":
		// Freeze existing WeChat content before changing shared defaults.
		if a.Wechat == nil {
			a.Wechat = wechatCopy(old)
		}
		a.Name = input.Name
		a.LogoURL = input.LogoURL
		a.Intro = input.Intro
		a.Module = strings.TrimSpace(input.Module)
	case "wechat":
		a.Wechat = wechatCopy(input)
		a.Kicker = input.Kicker
		a.LoginCopy = input.LoginCopy
		a.CapabilityCopy = input.CapabilityCopy
		a.CreationDetail = input.CreationDetail
		a.CTALabel = input.CTALabel
		a.Published = input.Published
		a.SortOrder = input.SortOrder
	case "web":
		if input.Web == nil {
			return a, errors.New("请提供网页展示配置")
		}
		if old.ProductID != "" && strings.TrimSpace(input.ProductID) != "" && old.ProductID != strings.TrimSpace(input.ProductID) {
			return a, errProductIdentityChange
		}
		a.Web = input.Web
		a.ProductID = old.ProductID
		if a.ProductID == "" {
			sum := sha256.Sum256([]byte("hub-web-product:" + old.ID))
			a.ProductID = fmt.Sprintf("web_%x", sum[:24])
		}
		a.StripePriceID = strings.TrimSpace(input.StripePriceID)
	default:
		return a, errors.New("未知的展示入口")
	}
	if err := validateCatalogFields(a, false); err != nil {
		return a, err
	}
	if err := validateCatalogFields(wechatCatalogView(a), scope == "wechat" || a.Published); err != nil {
		return a, err
	}
	return a, nil
}
func (m *Manager) adminScopedCatalog(c *gin.Context) {
	if !validCatalogScope(c.Param("scope")) {
		c.JSON(404, gin.H{"error": "未知的展示入口"})
		return
	}
	if !m.catalogDB(c) {
		return
	}
	var entries []schema.AgentCatalogEntry
	if err := m.wdb.Db.Order("sort_order, id").Find(&entries).Error; err != nil {
		catalogError(c, err)
		return
	}
	if c.Param("scope") == "wechat" {
		for i := range entries {
			entries[i] = wechatCatalogView(entries[i])
		}
	}
	c.JSON(200, gin.H{"agents": entries})
}
func (m *Manager) adminCreateCatalogCore(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var input schema.AgentCatalogEntry
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32768)
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "无效的助手信息"})
		return
	}
	a := schema.AgentCatalogEntry{ID: generatedCatalogID(input.Name), Name: input.Name, LogoURL: input.LogoURL, Intro: input.Intro, Module: strings.TrimSpace(input.Module)}
	if err := validateCatalogFields(a, false); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := m.wdb.Db.Create(&a).Error; err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(201, a)
}
func (m *Manager) adminSaveScopedCatalog(c *gin.Context) {
	scope := c.Param("scope")
	if !validCatalogScope(scope) {
		c.JSON(404, gin.H{"error": "未知的展示入口"})
		return
	}
	if !m.catalogDB(c) {
		return
	}
	var input, saved schema.AgentCatalogEntry
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32768)
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "无效的展示配置"})
		return
	}
	var validation error
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var old schema.AgentCatalogEntry
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, "id = ?", c.Param("id")).Error; err != nil {
			return err
		}
		var err error
		saved, err = mergeCatalogScope(old, input, scope)
		if err != nil {
			validation = err
			return err
		}
		return tx.Save(&saved).Error
	})
	if validation != nil {
		if errors.Is(validation, errProductIdentityChange) {
			catalogError(c, validation)
		} else {
			c.JSON(400, gin.H{"error": validation.Error()})
		}
		return
	}
	if err != nil {
		catalogError(c, err)
		return
	}
	if scope == "wechat" {
		saved = wechatCatalogView(saved)
	}
	c.JSON(200, saved)
}
