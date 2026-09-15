package manager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
	admin.GET("/stripe/settings", m.getStripeSettings)
	admin.PUT("/stripe/settings", m.saveStripeSettings)
	admin.GET("/invite-codes", m.listInviteCodes)
	admin.POST("/invite-codes", m.generateInviteCodes)
	admin.DELETE("/invite-codes/:code", m.revokeInviteCode)
	admin.GET("/billing", m.adminBilling)
	admin.GET("/web-agents", m.adminWebAgents)
	admin.POST("/web-agents/:id/retry", m.retryWebAgent)
}
func (m *Manager) commerceProduct(product string) (schema.AgentCatalogEntry, error) {
	var entry schema.AgentCatalogEntry
	if strings.TrimSpace(product) == "" {
		return entry, errors.New("product is required")
	}
	if err := m.wdb.Db.First(&entry, "product_id = ? AND published = ?", product, true).Error; err != nil || entry.Module == "" {
		return entry, errors.New("product is not available")
	}
	if err := validateDeploymentConfig(m.config.Deployment); err != nil {
		return entry, err
	}
	return entry, nil
}
func (m *Manager) listCommerceProducts(c *gin.Context) {
	items := []gin.H{}
	if err := validateDeploymentConfig(m.config.Deployment); err != nil {
		c.JSON(503, gin.H{"error": err.Error()})
		return
	}
	var entries []schema.AgentCatalogEntry
	if err := m.wdb.Db.Where("product_id <> '' AND published = ?", true).Order("sort_order, id").Find(&entries).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list products"})
		return
	}
	cfg, _, err := m.stripeRuntime()
	if err != nil {
		c.JSON(503, gin.H{"error": "payment settings unavailable"})
		return
	}
	for _, entry := range entries {
		items = append(items, gin.H{"product": entry.ProductID, "name": entry.Name, "catalogId": entry.ID, "subscriptionAvailable": cfg.Enabled && entry.StripePriceID != ""})
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
	rows := make([]schema.InviteCode, 0, req.Count)
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if req.Product != "" {
			var entry schema.AgentCatalogEntry
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entry, "product_id = ?", req.Product).Error; err != nil {
				return err
			}
		}
		const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		for attempts := 0; len(rows) < req.Count && attempts < req.Count*10; attempts++ {
			code := make([]byte, 6)
			for i := range code {
				n, e := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
				if e != nil {
					return e
				}
				code[i] = alphabet[n.Int64()]
			}
			row := schema.InviteCode{Code: string(code), Product: req.Product, Note: req.Note, ExpiresAt: req.ExpiresAt}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				rows = append(rows, row)
			}
		}
		if len(rows) != req.Count {
			return errors.New("cannot allocate unique invite codes")
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(400, gin.H{"error": "unknown product"})
		return
	}
	if err != nil {
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

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.UsedBy != "" {
			ids = append(ids, row.UsedBy)
		}
	}
	users, err := m.commerceUsers(ids)
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot load redemption users"})
		return
	}
	type inviteView struct {
		schema.InviteCode
		UsedByUser *schema.User `json:"usedByUser,omitempty"`
	}
	items := make([]inviteView, 0, len(rows))
	for _, row := range rows {
		items = append(items, inviteView{row, users[row.UsedBy]})
	}
	var total, used int64
	if err := m.wdb.Db.Model(&schema.InviteCode{}).Count(&total).Error; err != nil {
		c.Status(500)
		return
	}
	if err := m.wdb.Db.Model(&schema.InviteCode{}).Where("used_at IS NOT NULL").Count(&used).Error; err != nil {
		c.Status(500)
		return
	}
	c.JSON(200, gin.H{"codes": items, "total": total, "used": used})
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
	entry, err := m.commerceProduct(product)
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entry, "id = ? AND product_id = ? AND published = ?", entry.ID, product, true).Error; err != nil {
			return err
		}
		if entry.Module == "" {
			return errors.New("product is not available")
		}
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
	status := a.State
	switch status {
	case "queued", "processing":
		status = "starting"
	case "needs_review":
		status = "failed"
	}
	return gin.H{"agentId": a.ID, "product": a.Product, "status": status, "inviteCode": a.InviteCode, "botUsername": a.BotUsername, "telegramBotUrl": telegramBotLink(a.BotUsername), "telegramUrl": telegramBotLink(a.BotUsername), "createdAt": a.CreatedAt}
}
func (m *Manager) listWebAgents(c *gin.Context) {
	rows := []schema.WebAgent{}
	if err := m.wdb.Db.Where("user_id = ?", mustWebUser(c)).Order("created_at desc").Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list agents"})
		return
	}
	bills := []schema.Billing{}
	if err := m.wdb.Db.Where("user_id = ? AND agent_id <> ''", mustWebUser(c)).Find(&bills).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot load agent billing"})
		return
	}
	byAgent := make(map[string]schema.Billing, len(bills))
	for _, b := range bills {
		byAgent[b.AgentID] = b
	}
	items := []gin.H{}
	for _, a := range rows {
		item := webAgentResponse(a)
		if b, ok := byAgent[a.ID]; ok {
			item["billing"] = b
			item["currentPeriodStart"] = b.CurrentPeriodStart
			item["currentPeriodEnd"] = b.CurrentPeriodEnd
			item["cancelAtPeriodEnd"] = b.CancelAtPeriodEnd
		}
		items = append(items, item)
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
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}
	users, err := m.commerceUsers(ids)
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot load agent users"})
		return
	}
	items := []gin.H{}
	for _, a := range rows {
		r := webAgentResponse(a)
		r["status"] = a.State
		r["userId"] = a.UserID
		r["error"] = a.Error
		r["phase"] = a.Phase
		r["podId"] = a.PodID
		r["user"] = users[a.UserID]
		r["accessKeyId"] = a.AccessKeyID
		items = append(items, r)
	}
	var total, running int64
	if err := m.wdb.Db.Model(&schema.WebAgent{}).Count(&total).Error; err != nil {
		c.Status(500)
		return
	}
	if err := m.wdb.Db.Model(&schema.WebAgent{}).Where("state = ?", "running").Count(&running).Error; err != nil {
		c.Status(500)
		return
	}
	c.JSON(200, gin.H{"items": items, "total": total, "running": running})
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

func (m *Manager) commerceUsers(ids []string) (map[string]*schema.User, error) {
	result := make(map[string]*schema.User)
	if len(ids) == 0 {
		return result, nil
	}
	var users []schema.User
	if err := m.wdb.Db.Select("id", "name", "email", "picture").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for i := range users {
		result[users[i].ID] = &users[i]
	}
	return result, nil
}
