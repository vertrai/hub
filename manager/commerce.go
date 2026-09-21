package manager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"sort"
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
	user.POST("/agents/:id/telegram", m.acquireWebTelegram)
	user.POST("/agents/:id/start", m.configureWebAgent)
	user.POST("/agents/:id/weixin", m.startWebWeixin)
	user.GET("/agents/:id/weixin/:attempt", m.pollWebWeixin)
	user.POST("/agents/free", m.createFreeAgent)
	user.POST("/invite-codes/redeem", func(c *gin.Context) { m.redeemInvite(c, "") })
	user.GET("/invite-codes/me", m.myInviteCodes)
	user.GET("/billing", m.listBilling)
	user.POST("/billing/checkout-sessions", m.createCheckoutSession)
	user.GET("/billing/checkout-sessions/:sessionId", m.getCheckoutSessionStatus)
	user.POST("/billing/portal-sessions", m.createPortalSession)
	admin := r.Group("/v1/admin", m.requireAdmin)
	admin.GET("/stripe/settings", m.getStripeSettings)
	admin.PATCH("/stripe/products/:id", m.saveStripeProduct)
	admin.GET("/invite-codes", m.listInviteCodes)
	admin.POST("/invite-codes", m.generateInviteCodes)
	admin.POST("/invite-codes/claim", m.claimInviteCodes)
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
	if err := m.wdb.Db.First(&entry, "product_id = ?", product).Error; err != nil || entry.Module == "" || !commercePublished(entry) {
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
	if err := m.wdb.Db.Where("product_id <> ''").Order("sort_order, id").Find(&entries).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list products"})
		return
	}
	cfg, _, err := m.stripeRuntime()
	if err != nil {
		c.JSON(503, gin.H{"error": "payment settings unavailable"})
		return
	}
	for _, entry := range entries {
		if !commercePublished(entry) {
			continue
		}
		items = append(items, gin.H{"product": entry.ProductID, "name": entry.Name, "catalogId": entry.ID, "subscriptionAvailable": webSubscriptionRequired(entry) && cfg.Enabled && entry.StripePriceID != ""})
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
		InviteCode      string `json:"inviteCode"`
		Product         string `json:"product"`
		ConsentAccepted bool   `json:"consentAccepted"`
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
	if entry.Web != nil && (!entry.Web.InviteEnabled || (webRequiresConsent(entry) && !req.ConsentAccepted)) {
		c.JSON(400, gin.H{"error": "invitation unavailable or consent required"})
		return
	}
	agent, err := m.reserveInviteAgent(strings.ToUpper(strings.TrimSpace(req.InviteCode)), mustWebUser(c), product, entry, req.ConsentAccepted)
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
func (m *Manager) reserveInviteAgent(code, user, product string, entry schema.AgentCatalogEntry, consentAccepted bool) (schema.WebAgent, error) {
	var a schema.WebAgent
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entry, "id = ? AND product_id = ?", entry.ID, product).Error; err != nil {
			return err
		}
		if entry.Module == "" || !commercePublished(entry) || (entry.Web != nil && (!entry.Web.InviteEnabled || (webRequiresConsent(entry) && !consentAccepted))) {
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
	return gin.H{"agentId": a.ID, "product": a.Product, "status": status, "phase": a.Phase, "inviteCode": a.InviteCode, "botUsername": a.BotUsername, "telegramBotUrl": telegramBotLink(a.BotUsername), "telegramUrl": telegramBotLink(a.BotUsername), "createdAt": a.CreatedAt}
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
	var catalog []schema.AgentCatalogEntry
	if err := m.wdb.Db.Find(&catalog).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot load agent details"})
		return
	}
	byCatalog := map[string]schema.AgentCatalogEntry{}
	byProduct := map[string]schema.AgentCatalogEntry{}
	for _, entry := range catalog {
		byCatalog[entry.ID] = entry
		byProduct[entry.ProductID] = entry
	}
	items := []gin.H{}
	for _, a := range rows {
		item := webAgentResponse(a)
		entry, ok := byCatalog[a.CatalogID]
		if !ok {
			entry, ok = byProduct[a.Product]
		}
		if ok {
			item["catalogId"] = entry.ID
			item["agent"] = m.webCatalogEntry(entry, webLocale(c))
		}
		item["connection"] = gin.H{"type": "telegram", "url": ""}
		if a.ChannelConfigured && !a.EnableTelegram {
			item["connection"] = gin.H{"type": "weixin", "url": ""}
		}
		if a.State == "running" && (!a.ChannelConfigured || a.EnableTelegram) {
			item["connection"] = gin.H{"type": "telegram", "url": telegramBotLink(a.BotUsername)}
		}
		connections := []gin.H{}
		if a.EnableTelegram || (!a.ChannelConfigured && a.BotUsername != "") {
			url := ""
			if a.State == "running" {
				url = telegramBotLink(a.BotUsername)
			}
			connections = append(connections, gin.H{"type": "telegram", "url": url})
		}
		if a.ChannelConfigured && a.WeixinAuthorizedBotID != "" {
			connections = append(connections, gin.H{"type": "weixin", "url": ""})
		}
		item["connections"] = connections
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

// Claim marks distribution, not redemption. A claimed code remains redeemable once.
func (m *Manager) claimInviteCodes(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var req struct {
		Codes []string `json:"codes"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.Codes) < 1 || len(req.Codes) > 100 {
		c.JSON(400, gin.H{"error": "select 1–100 invitation codes"})
		return
	}
	seen := make(map[string]bool)
	for i, code := range req.Codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" || seen[code] {
			c.JSON(400, gin.H{"error": "empty or duplicate code"})
			return
		}
		seen[code] = true
		req.Codes[i] = code
	}
	sort.Strings(req.Codes)
	unavailable := errors.New("one or more codes are already claimed, redeemed, revoked or expired; refresh the list")
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		for _, code := range req.Codes {
			now := time.Now()
			result := tx.Model(&schema.InviteCode{}).Where("code = ? AND claimed_at IS NULL AND used_at IS NULL AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", code, now).Update("claimed_at", now)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return unavailable
			}
		}
		return nil
	})
	if errors.Is(err, unavailable) {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot claim invitation codes"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"codes": req.Codes})
}

// Serialize free creation on the catalog row and reuse the user's existing free
// instance. Retries and concurrent clicks must not allocate extra runtimes.
func (m *Manager) createFreeAgent(c *gin.Context) {
	var req struct {
		Product         string `json:"product"`
		ConsentAccepted bool   `json:"consentAccepted"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.ConsentAccepted {
		c.JSON(400, gin.H{"error": "consent required"})
		return
	}
	entry, err := m.commerceProduct(req.Product)
	if err != nil {
		c.JSON(409, gin.H{"error": "product unavailable"})
		return
	}
	var agent schema.WebAgent
	denied := errors.New("free creation unavailable")
	err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entry, "id = ?", entry.ID).Error; err != nil {
			return err
		}
		if !commercePublished(entry) || !webFreeEnabled(entry) || entry.Module == "" {
			return denied
		}
		err := tx.Where("user_id = ? AND catalog_id = ? AND source IN ?", mustWebUser(c), entry.ID, []string{"free", "free:" + mustWebUser(c) + ":" + entry.ID}).First(&agent).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		agent = schema.WebAgent{ID: commerceID("agent_"), UserID: mustWebUser(c), Product: entry.ProductID, CatalogID: entry.ID, Module: entry.Module, Source: "free:" + mustWebUser(c) + ":" + entry.ID, State: "queued", Desired: "running"}
		return tx.Create(&agent).Error
	})
	if errors.Is(err, denied) {
		c.JSON(403, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot create agent"})
		return
	}
	c.JSON(200, gin.H{"agent": webAgentResponse(agent)})
}
