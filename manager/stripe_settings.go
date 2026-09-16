package manager

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Global payment settings are exclusively owned by config.yaml. Legacy database
// settings must not override them, even if an earlier release saved a row.
func (m *Manager) stripeRuntime() (StripeConfig, stripeGateway, error) {
	return m.config.Stripe, m.stripeAPI, nil
}
func (m *Manager) getStripeSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	cfg := m.config.Stripe
	c.JSON(200, gin.H{"enabled": cfg.Enabled, "secretKeyConfigured": cfg.SecretKey != "", "webhookSecretConfigured": cfg.WebhookSecret != ""})
}
func (m *Manager) saveStripeProduct(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var req struct {
		ProductID     string `json:"productId"`
		StripePriceID string `json:"stripePriceId"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "无效的订阅价格配置"})
		return
	}
	req.ProductID = strings.TrimSpace(req.ProductID)
	req.StripePriceID = strings.TrimSpace(req.StripePriceID)
	if err := validateCatalogCommerce(schema.AgentCatalogEntry{ProductID: req.ProductID, StripePriceID: req.StripePriceID}); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var entry schema.AgentCatalogEntry
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entry, "id = ?", c.Param("id")).Error; err != nil {
			return err
		}
		if entry.ProductID != "" && entry.ProductID != req.ProductID {
			return errProductIdentityChange
		}
		return tx.Model(&entry).Updates(map[string]any{"product_id": req.ProductID, "stripe_price_id": req.StripePriceID}).Error
	})
	if err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, gin.H{"id": entry.ID, "productId": req.ProductID, "stripePriceId": req.StripePriceID})
}
