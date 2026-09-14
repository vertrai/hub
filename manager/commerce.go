package manager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommerceProduct struct {
	CatalogID string
	PriceID   string
}
type CommerceConfig struct {
	Products                                                      map[string]CommerceProduct
	NodeURL, AdminURL, PrivateKey, GatewayURL, HermesGatewayToken string
}

func commerceID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}
func (m *Manager) registerCommerceRoutes(r *gin.Engine) {
	r.GET("/v1/auth/info", func(c *gin.Context) { c.JSON(200, gin.H{"googleClientId": m.adminAuth.clientID}) })
	r.POST("/v1/auth/google", func(c *gin.Context) { m.googleLogin(c, false) })
	r.POST("/v1/auth/logout", func(c *gin.Context) { http.SetCookie(c.Writer, adminCookie("", m.adminAuth.secure, -1)); c.Status(204) })
	r.POST("/v1/stripe/webhook", m.stripeWebhook)
	user := r.Group("/v1", m.requireUser)
	user.GET("/me", func(c *gin.Context) {
		c.JSON(200, gin.H{"user": googleUserResponse(c.MustGet("userIdentity").(adminIdentity))})
	})
	user.GET("/agents", m.listWebAgents)
	user.GET("/products", m.listCommerceProducts)
	for path, product := range map[string]string{"telegram-customer": "telegram_customer_agent", "x": "x_agent", "google": "google_agent"} {
		p := product
		user.POST("/agents/"+path, func(c *gin.Context) { m.redeemInvite(c, p) })
	}
	user.POST("/invite-codes/redeem", func(c *gin.Context) { m.redeemInvite(c, "") })
	user.GET("/invite-codes/me", m.myInviteCodes)
	user.GET("/billing", m.listBilling)
	user.POST("/billing/checkout-sessions", m.createCheckoutSession)
	user.GET("/billing/checkout-sessions/:sessionId", m.getCheckoutSessionStatus)
	user.POST("/billing/portal-sessions", m.createPortalSession)
	admin := r.Group("/v1/admin", m.requireAdmin)
	admin.GET("/invite-codes", m.listInviteCodes)
	admin.POST("/invite-codes", m.generateInviteCodes)
	admin.DELETE("/invite-codes/:code", m.revokeInviteCode)
	admin.GET("/billing", m.adminBilling)
	admin.GET("/web-agents", m.adminWebAgents)
	admin.POST("/web-agents/:id/retry", m.retryWebAgent)
}
func (m *Manager) commerceProduct(product string) (CommerceProduct, schema.AgentCatalogEntry, error) {
	p, ok := m.config.Commerce.Products[product]
	if !ok || p.CatalogID == "" {
		return p, schema.AgentCatalogEntry{}, errors.New("product is not configured")
	}
	var entry schema.AgentCatalogEntry
	if err := m.wdb.Db.First(&entry, "id = ? AND published = ?", p.CatalogID, true).Error; err != nil || entry.Module == "" {
		return p, entry, errors.New("product is not available")
	}
	cfg := m.config.Commerce
	if cfg.NodeURL == "" || cfg.PrivateKey == "" || cfg.GatewayURL == "" || cfg.HermesGatewayToken == "" {
		return p, entry, errors.New("web agent deployment is not configured")
	}
	return p, entry, nil
}
func (m *Manager) listCommerceProducts(c *gin.Context) {
	items := []gin.H{}
	for product := range m.config.Commerce.Products {
		p, e, err := m.commerceProduct(product)
		if err == nil {
			items = append(items, gin.H{"product": product, "name": e.Name, "catalogId": e.ID, "subscriptionAvailable": m.config.Stripe.Enabled && p.PriceID != ""})
		}
	}
	c.JSON(200, gin.H{"items": items})
}
func (m *Manager) generateInviteCodes(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var req struct {
		Count     int        `json:"count"`
		Product   string     `json:"product"`
		Note      string     `json:"note"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Count < 1 || req.Count > 100 || len(req.Note) > 500 {
		c.JSON(400, gin.H{"error": "count must be 1–100; note at most 500 bytes"})
		return
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		c.JSON(400, gin.H{"error": "expiry must be in the future"})
		return
	}
	if req.Product != "" {
		if _, ok := m.config.Commerce.Products[req.Product]; !ok {
			c.JSON(400, gin.H{"error": "unknown product"})
			return
		}
	}
	rows := make([]schema.InviteCode, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		rows = append(rows, schema.InviteCode{Code: strings.ToUpper(commerceID("")[:16]), Product: req.Product, Note: req.Note, ExpiresAt: req.ExpiresAt})
	}
	if err := m.wdb.Db.Create(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot create invitation codes"})
		return
	}
	c.JSON(201, gin.H{"codes": rows})
}
func (m *Manager) listInviteCodes(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	rows := []schema.InviteCode{}
	q := m.wdb.Db.Order("created_at desc").Limit(1000)
	if status := c.Query("status"); status == "unused" {
		q = q.Where("used_at IS NULL AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", time.Now())
	}
	if err := q.Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list codes"})
		return
	}
	c.JSON(200, gin.H{"codes": rows})
}
func (m *Manager) revokeInviteCode(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	result := m.wdb.Db.Model(&schema.InviteCode{}).Where("code = ? AND used_at IS NULL AND revoked_at IS NULL", strings.ToUpper(strings.TrimSpace(c.Param("code")))).Update("revoked_at", time.Now())
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "cannot revoke code"})
		return
	}
	if result.RowsAffected != 1 {
		c.JSON(409, gin.H{"error": "code is missing, redeemed or revoked"})
		return
	}
	c.Status(204)
}
func (m *Manager) myInviteCodes(c *gin.Context) {
	rows := []schema.InviteCode{}
	if err := m.wdb.Db.Where("used_by = ?", mustWebUser(c)).Order("used_at desc").Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list redeemed codes"})
		return
	}
	var latest any
	if len(rows) > 0 {
		latest = rows[0]
	}
	c.JSON(200, gin.H{"redeemed": len(rows) > 0, "code": latest, "codes": rows})
}

var errInviteUnavailable = errors.New("invitation code is invalid, expired or already used")

func (m *Manager) redeemInvite(c *gin.Context, product string) {
	var req struct {
		InviteCode string `json:"inviteCode"`
		Product    string `json:"product"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.InviteCode) == "" {
		c.JSON(400, gin.H{"error": "inviteCode is required"})
		return
	}
	if product == "" {
		product = req.Product
	}
	_, entry, err := m.commerceProduct(product)
	if err != nil {
		c.JSON(503, gin.H{"error": err.Error()})
		return
	}
	agent, err := m.reserveInviteAgent(strings.ToUpper(strings.TrimSpace(req.InviteCode)), mustWebUser(c), product, entry)
	if errors.Is(err, errInviteUnavailable) {
		c.JSON(403, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot redeem code"})
		return
	}
	c.JSON(200, gin.H{"agent": webAgentResponse(agent)})
}
func (m *Manager) reserveInviteAgent(code, user, product string, entry schema.AgentCatalogEntry) (schema.WebAgent, error) {
	var a schema.WebAgent
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var invite schema.InviteCode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&invite, "code = ?", code).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errInviteUnavailable
			}
			return err
		}
		// A retry of a successful redemption returns the same instance only to its owner.
		if invite.UsedBy == user && invite.Product == product && invite.AgentID != "" {
			return tx.First(&a, "id = ? AND user_id = ?", invite.AgentID, user).Error
		}
		if invite.UsedAt != nil || invite.RevokedAt != nil || (invite.ExpiresAt != nil && !invite.ExpiresAt.After(time.Now())) || (invite.Product != "" && invite.Product != product) {
			return errInviteUnavailable
		}
		a = schema.WebAgent{ID: commerceID("agent_"), UserID: user, Product: product, CatalogID: entry.ID, Module: entry.Module, Source: "invite:" + code, InviteCode: code, State: "queued", Desired: "running"}
		if err := tx.Create(&a).Error; err != nil {
			return err
		}
		r := tx.Model(&invite).Where("used_at IS NULL AND revoked_at IS NULL").Updates(map[string]any{"used_at": time.Now(), "used_by": user, "product": product, "agent_id": a.ID})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return errInviteUnavailable
		}
		return nil
	})
	return a, err
}
func webAgentResponse(a schema.WebAgent) gin.H {
	return gin.H{"agentId": a.ID, "product": a.Product, "status": a.State, "inviteCode": a.InviteCode, "botUsername": a.BotUsername, "telegramBotUrl": telegramBotLink(a.BotUsername), "telegramUrl": telegramBotLink(a.BotUsername), "createdAt": a.CreatedAt}
}
func (m *Manager) listWebAgents(c *gin.Context) {
	rows := []schema.WebAgent{}
	if err := m.wdb.Db.Where("user_id = ?", mustWebUser(c)).Order("created_at desc").Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list agents"})
		return
	}
	items := []gin.H{}
	for _, a := range rows {
		items = append(items, webAgentResponse(a))
	}
	c.JSON(200, gin.H{"items": items})
}
func (m *Manager) adminWebAgents(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	rows := []schema.WebAgent{}
	if err := m.wdb.Db.Order("created_at desc").Limit(1000).Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list agents"})
		return
	}
	items := []gin.H{}
	for _, a := range rows {
		r := webAgentResponse(a)
		r["userId"] = a.UserID
		r["error"] = a.Error
		r["phase"] = a.Phase
		r["podId"] = a.PodID
		items = append(items, r)
	}
	c.JSON(200, gin.H{"items": items})
}
func (m *Manager) retryWebAgent(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	r := m.wdb.Db.Model(&schema.WebAgent{}).Where("id = ? AND state = ?", c.Param("id"), "failed").Updates(map[string]any{"state": "queued", "error": "", "lease_until": nil})
	if r.Error != nil {
		c.JSON(500, gin.H{"error": "cannot retry"})
		return
	}
	if r.RowsAffected != 1 {
		c.JSON(409, gin.H{"error": "only failed tasks can be retried; uncertain deployment requires reconciliation"})
		return
	}
	c.Status(204)
}
