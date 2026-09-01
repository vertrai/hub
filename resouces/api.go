package resouces

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/common"
	resourcebrowser "github.com/vertrai/hub/resouces/browser"
	resourcegoogle "github.com/vertrai/hub/resouces/google"
	"github.com/vertrai/hub/resouces/schema"
	resourcetelegram "github.com/vertrai/hub/resouces/telegram"
	"gorm.io/gorm"
)

const gatewayPrincipalContext = "gatewayPrincipal"

const (
	resourceScopeGoogle   = "google"
	resourceScopeBrowser  = "browser"
	resourceScopeTelegram = "telegram"
)

type gatewayPrincipal struct {
	AccessKey schema.AccessKey
}

func (g *Resouces) router() *gin.Engine {
	r := gin.New()
	r.Use(common.RequestLogger(log), gin.Recovery(), common.CORSMiddleware())
	r.GET("/info", g.info)
	internal := r.Group("/v1/internal", g.requireAdmin)
	internal.POST("/access-keys", g.createAccessKey)
	internal.GET("/access-keys", g.listAccessKeys)
	internal.PATCH("/access-keys/:id/scopes", g.updateAccessKeyScopes)
	admin := internal
	admin.POST("/google/accounts", g.createGoogleAccount)
	admin.POST("/google/accounts/batch", g.createGoogleAccountsBatch)
	admin.GET("/google/accounts", g.listGoogleAccounts)
	admin.POST("/xbox/bots", g.createXBot)
	admin.GET("/xbox/bots", g.listXBots)
	admin.GET("/browser/sessions", g.listBrowserSessions)
	admin.POST("/browser/sessions/:id/close", g.closeBrowserSessionAdmin)
	admin.POST("/telegram/bots", g.importTelegramBot)
	admin.GET("/telegram/bots", g.listTelegramBots)
	admin.POST("/telegram/bots/create", g.createTelegramBots)
	admin.POST("/telegram/auth/init", g.initTelegramAuth)
	admin.POST("/telegram/auth/verify", g.verifyTelegramAuth)
	admin.POST("/telegram/auth/2fa", g.submitTelegram2FA)
	admin.GET("/telegram/auth/status", g.telegramAuthStatus)
	admin.GET("/telegram/auth/accounts", g.listTelegramAccounts)
	user := r.Group("/v1", g.requireGatewayAPIKey)
	user.GET("/google-user", g.requireResourceScope(resourceScopeGoogle), g.getGoogleUser)
	user.GET("/google-user/access-token", g.requireResourceScope(resourceScopeGoogle), g.issueGoogleToken)
	user.POST("/google-user/test/gmail/send", g.requireResourceScope(resourceScopeGoogle), g.testSendGmail)
	user.POST("/google-user/test/drive/folders", g.requireResourceScope(resourceScopeGoogle), g.testCreateDriveFolder)
	user.GET("/browser", g.requireResourceScope(resourceScopeBrowser), g.currentBrowser)
	user.POST("/browser/reset", g.requireResourceScope(resourceScopeBrowser), g.resetBrowser)
	user.POST("/browser/close", g.requireResourceScope(resourceScopeBrowser), g.closeBrowser)
	user.GET("/telegram-bot", g.requireResourceScope(resourceScopeTelegram), g.getTelegramBot)
	user.GET("/xbox/account", g.getXBotAccount)
	return r
}

func (g *Resouces) createXBot(c *gin.Context) {
	var req struct {
		GoogleUserID string `json:"googleUserId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.GoogleUserID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "googleUserId is required"})
		return
	}
	bot, err := g.xbot.Designate(req.GoogleUserID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Google user not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"bot": bot})
}

func (g *Resouces) listXBots(c *gin.Context) {
	rows, err := g.xbot.List()
	if err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (g *Resouces) getXBotAccount(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	bot, err := g.xbot.Acquire(principal.AccessKey.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusConflict, gin.H{"error": "no registered XBot account is available"})
		return
	}
	if err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"account": gin.H{
		"id": bot.ID, "username": bot.Email, "email": bot.Email, "password": bot.Password,
		"purpose": bot.Purpose, "status": bot.Status,
	}})
}

func (g *Resouces) listBrowserSessions(c *gin.Context) {
	type browserSessionSummary struct {
		ID                string     `json:"id"`
		AccessKeyID       string     `json:"accessKeyId"`
		ProviderBrowserID string     `json:"providerBrowserId,omitempty"`
		ProviderProfileID string     `json:"providerProfileId,omitempty"`
		ProfileName       string     `json:"profileName,omitempty"`
		LiveURL           string     `json:"liveUrl,omitempty"`
		ProxyCountryCode  string     `json:"proxyCountryCode,omitempty"`
		TimeoutMinutes    int        `json:"timeoutMinutes,omitempty"`
		Status            string     `json:"status"`
		ProviderTimeoutAt *time.Time `json:"timeoutAt,omitempty"`
		LastUsedAt        *time.Time `json:"lastUsedAt,omitempty"`
		CreatedAt         time.Time  `json:"createdAt"`
	}
	var rows []browserSessionSummary
	if err := g.wdb.Db.Model(&schema.Browser{}).Order("created_at desc").Find(&rows).Error; err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (g *Resouces) closeBrowserSessionAdmin(c *gin.Context) {
	var row schema.Browser
	if err := g.wdb.Db.First(&row, "id = ?", c.Param("id")).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "browser profile not found"})
		return
	} else if err != nil {
		g.internalError(c, err)
		return
	}

	unlock := g.lockBrowserAccessKey(row.AccessKeyID)
	defer unlock()
	if err := g.wdb.Db.First(&row, "id = ?", row.ID).Error; err != nil {
		g.internalError(c, err)
		return
	}
	if row.ProviderBrowserID != "" {
		if err := g.browserProvider.Stop(c.Request.Context(), row.ProviderBrowserID); err != nil && !resourcebrowser.IsBrowserProviderNotFound(err) {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
	}
	now := time.Now()
	if err := g.wdb.Db.Model(&row).Updates(map[string]any{
		"provider_browser_id": "",
		"cdp_url":             "",
		"live_url":            "",
		"status":              "stopped",
		"provider_started_at": nil,
		"provider_timeout_at": nil,
		"provider_checked_at": now,
	}).Error; err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"closed": true, "id": row.ID, "status": "stopped"})
}

func (g *Resouces) listAccessKeys(c *gin.Context) {
	type accessKeySummary struct {
		schema.AccessKey
		GoogleEmail string `json:"googleEmail,omitempty"`
		BrowserID   string `json:"browserId,omitempty"`
		TelegramBot string `json:"telegramBot,omitempty"`
	}
	var keys []schema.AccessKey
	if err := g.wdb.Db.Order("created_at desc").Find(&keys).Error; err != nil {
		g.internalError(c, err)
		return
	}
	items := make([]accessKeySummary, 0, len(keys))
	for _, key := range keys {
		keyItem := accessKeySummary{AccessKey: key}
		var account schema.GoogleAccount
		if err := g.wdb.Db.Where("assigned_access_key_id = ?", key.ID).First(&account).Error; err == nil {
			keyItem.GoogleEmail = account.Email
		}
		var browser schema.Browser
		if err := g.wdb.Db.Where("access_key_id = ?", key.ID).First(&browser).Error; err == nil {
			keyItem.BrowserID = browser.ID
		}
		var telegramBot schema.TelegramBot
		if err := g.wdb.Db.Where("assigned_access_key_id = ?", key.ID).First(&telegramBot).Error; err == nil {
			keyItem.TelegramBot = telegramBot.Username
		}
		items = append(items, keyItem)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (g *Resouces) runAPI(endpoint string) {
	g.apiServer = &http.Server{Addr: endpoint, Handler: g.router()}
	if err := g.apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http listen", "err", err)
	}
}
func (g *Resouces) info(c *gin.Context) {
	c.JSON(200, gin.H{"name": "hub", "env": g.env})
}
func (g *Resouces) requireAdmin(c *gin.Context) {
	got := firstNonEmpty(c.GetHeader("X-Admin-API-Key"), bearer(c.GetHeader("Authorization")))
	if g.config.AdminAPIKey == "" || len(got) != len(g.config.AdminAPIKey) || subtle.ConstantTimeCompare([]byte(got), []byte(g.config.AdminAPIKey)) != 1 {
		c.AbortWithStatusJSON(401, gin.H{"error": "valid admin api key is required"})
	}
}

func (g *Resouces) createAccessKey(c *gin.Context) {
	var req struct {
		OwnerUserID   string `json:"ownerUserId"`
		AllowGoogle   *bool  `json:"allowGoogle"`
		AllowBrowser  *bool  `json:"allowBrowser"`
		AllowTelegram *bool  `json:"allowTelegram"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.OwnerUserID) == "" {
		c.JSON(400, gin.H{"error": "ownerUserId is required"})
		return
	}
	ownerUserID := strings.TrimSpace(req.OwnerUserID)
	key, err := newAccessKey()
	if err != nil {
		g.internalError(c, err)
		return
	}
	keyID, err := newID("key_")
	if err != nil {
		g.internalError(c, err)
		return
	}
	allowGoogle := boolDefaultTrue(req.AllowGoogle)
	allowBrowser := boolDefaultTrue(req.AllowBrowser)
	allowTelegram := boolDefaultTrue(req.AllowTelegram)
	accessKey := schema.AccessKey{ID: keyID, OwnerUserID: ownerUserID, KeyHash: hashSecret(key), KeyPrefix: secretPrefix(key), Status: schema.StatusActive, AllowGoogle: true, AllowBrowser: true, AllowTelegram: true}
	err = g.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&accessKey).Error; err != nil {
			return err
		}
		return tx.Model(&accessKey).Updates(map[string]any{"allow_google": allowGoogle, "allow_browser": allowBrowser, "allow_telegram": allowTelegram}).Error
	})
	if err != nil {
		g.internalError(c, err)
		return
	}
	accessKey.AllowGoogle = allowGoogle
	accessKey.AllowBrowser = allowBrowser
	accessKey.AllowTelegram = allowTelegram
	c.JSON(200, gin.H{"accessKey": accessKey, "gatewayApiKey": key})
}

func boolDefaultTrue(value *bool) bool {
	return value == nil || *value
}

func (g *Resouces) updateAccessKeyScopes(c *gin.Context) {
	var req struct {
		AllowGoogle   *bool `json:"allowGoogle"`
		AllowBrowser  *bool `json:"allowBrowser"`
		AllowTelegram *bool `json:"allowTelegram"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.AllowGoogle == nil || req.AllowBrowser == nil || req.AllowTelegram == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "allowGoogle, allowBrowser and allowTelegram are required"})
		return
	}
	var key schema.AccessKey
	if err := g.wdb.Db.First(&key, "id = ?", c.Param("id")).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "access key not found"})
		return
	} else if err != nil {
		g.internalError(c, err)
		return
	}
	if err := g.wdb.Db.Model(&key).Updates(map[string]any{"allow_google": *req.AllowGoogle, "allow_browser": *req.AllowBrowser, "allow_telegram": *req.AllowTelegram}).Error; err != nil {
		g.internalError(c, err)
		return
	}
	key.AllowGoogle = *req.AllowGoogle
	key.AllowBrowser = *req.AllowBrowser
	key.AllowTelegram = *req.AllowTelegram
	c.JSON(http.StatusOK, gin.H{"accessKey": key})
}

func (g *Resouces) createGoogleAccount(c *gin.Context) {
	var req struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" {
		c.JSON(400, gin.H{"error": "email is required"})
		return
	}
	if req.Password == "" {
		var err error
		req.Password, err = resourcegoogle.RandomPassword()
		if err != nil {
			g.internalError(c, err)
			return
		}
	}
	row, err := g.google.CreateAccount(c.Request.Context(), req.Email, req.Password, firstNonEmpty(req.GivenName, "Agent"), firstNonEmpty(req.FamilyName, "User"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"account": row})
}

func (g *Resouces) createGoogleAccountsBatch(c *gin.Context) {
	var req struct {
		Count  int    `json:"count"`
		Domain string `json:"domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "count must be <= 100"})
		return
	}
	domain := firstNonEmpty(req.Domain, g.google.Domain())
	if !strings.EqualFold(domain, g.google.Domain()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain must match configured workspace domain"})
		return
	}
	created, err := g.google.CreateAccounts(c.Request.Context(), req.Count, domain)
	if err != nil {
		status := http.StatusBadGateway
		if len(created) > 0 {
			status = http.StatusMultiStatus
		}
		c.JSON(status, gin.H{"error": err.Error(), "created": created, "createdCount": len(created), "requestedCount": req.Count})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"created": created, "createdCount": len(created), "requestedCount": req.Count})
}
func (g *Resouces) listGoogleAccounts(c *gin.Context) {
	rows, err := g.google.ListAccounts()
	if err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows})
}

func (g *Resouces) getGoogleUser(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	purpose := strings.ToLower(strings.TrimSpace(c.Query("purpose")))
	if purpose == schema.GooglePurposeXbox {
		account, err := g.xbot.Acquire(principal.AccessKey.ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusConflict, gin.H{"error": "no Xbox Google user is available"})
			return
		}
		if err != nil {
			g.internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"googleUser": account})
		return
	}
	if purpose != "" && purpose != schema.GooglePurposeGeneral {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported Google user purpose"})
		return
	}
	account, err := g.google.AcquireAccount(c.Request.Context(), principal.AccessKey.ID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"googleUser": account})
}
func (g *Resouces) issueGoogleToken(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	token, account, err := g.google.IssueToken(c.Request.Context(), principal.AccessKey.ID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"accessToken": token.AccessToken, "tokenType": firstNonEmpty(token.TokenType, "Bearer"), "expiresAt": token.Expiry, "email": account.Email})
}

func (g *Resouces) testSendGmail(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	var req struct{ To, Subject, Body string }
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	messageID, threadID, account, err := g.google.SendGmail(c.Request.Context(), principal.AccessKey.ID, req.To, req.Subject, req.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": account.Email, "messageId": messageID, "threadId": threadID, "to": req.To})
}

func (g *Resouces) testCreateDriveFolder(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	var req struct{ Name string }
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder name is required"})
		return
	}
	folder, account, err := g.google.CreateDriveFolder(c.Request.Context(), principal.AccessKey.ID, req.Name)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": account.Email, "folder": folder})
}

func (g *Resouces) importTelegramBot(c *gin.Context) {
	var req struct {
		BotToken         string `json:"botToken"`
		Username         string `json:"username"`
		CreatedByAccount string `json:"createdByAccount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	bot, err := g.telegram.Import(req.BotToken, req.Username, req.CreatedByAccount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	bot.BotToken = resourcetelegram.MaskToken(bot.BotToken)
	c.JSON(http.StatusCreated, gin.H{"telegramBot": bot})
}

func (g *Resouces) listTelegramBots(c *gin.Context) {
	rows, err := g.telegram.List()
	if err != nil {
		g.internalError(c, err)
		return
	}
	for i := range rows {
		rows[i].BotToken = resourcetelegram.MaskToken(rows[i].BotToken)
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (g *Resouces) getTelegramBot(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	bot, err := g.telegram.Assign(principal.AccessKey.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no available telegram bot"})
		return
	}
	if err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"telegramBot": bot})
}

func (g *Resouces) currentBrowser(c *gin.Context) { g.startOrResetBrowser(c, false) }
func (g *Resouces) resetBrowser(c *gin.Context)   { g.startOrResetBrowser(c, true) }

func (g *Resouces) closeBrowser(c *gin.Context) {
	principal := mustGatewayPrincipal(c)
	accessKeyID := principal.AccessKey.ID
	unlock := g.lockBrowserAccessKey(accessKeyID)
	defer unlock()

	var row schema.Browser
	if err := g.wdb.Db.Where("access_key_id = ?", accessKeyID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, gin.H{"closed": false, "message": "browser has not been created"})
		return
	} else if err != nil {
		g.internalError(c, err)
		return
	}

	if row.ProviderBrowserID != "" {
		if err := g.browserProvider.Stop(c.Request.Context(), row.ProviderBrowserID); err != nil && !resourcebrowser.IsBrowserProviderNotFound(err) {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
	}
	now := time.Now()
	row.ProviderBrowserID = ""
	row.CDPURL = ""
	row.LiveURL = ""
	row.Status = "stopped"
	row.ProviderStartedAt = nil
	row.ProviderTimeoutAt = nil
	row.ProviderCheckedAt = &now
	if err := g.wdb.Db.Save(&row).Error; err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"browser": row, "closed": true})
}

func (g *Resouces) startOrResetBrowser(c *gin.Context, reset bool) {
	principal := mustGatewayPrincipal(c)
	accessKeyID := principal.AccessKey.ID
	unlock := g.lockBrowserAccessKey(accessKeyID)
	defer unlock()
	var row schema.Browser
	err := g.wdb.Db.Where("access_key_id = ?", accessKeyID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		id, idErr := newID("brw_")
		if idErr != nil {
			g.internalError(c, idErr)
			return
		}
		row = schema.Browser{ID: id, AccessKeyID: accessKeyID, ProfileName: "hub-gateway-" + accessKeyID, ProxyCountryCode: g.config.BrowserProxyCountryCode, TimeoutMinutes: g.config.BrowserTimeoutMinutes, Status: schema.StatusActive}
		if err := g.wdb.Db.Create(&row).Error; err != nil {
			g.internalError(c, err)
			return
		}
	} else if err != nil {
		g.internalError(c, err)
		return
	}
	now := time.Now()
	notNearExpiry := row.ProviderTimeoutAt == nil || time.Until(*row.ProviderTimeoutAt) > 10*time.Minute
	if !reset && row.ProviderBrowserID != "" && row.CDPURL != "" && notNearExpiry {
		if row.ProviderCheckedAt != nil && now.Sub(*row.ProviderCheckedAt) < g.config.BrowserStatusCheckInterval {
			row.LastUsedAt = &now
			_ = g.wdb.Db.Model(&row).Updates(map[string]any{"last_used_at": now}).Error
			c.JSON(200, gin.H{"browser": row, "cached": true})
			return
		}
		remote, statusErr := g.browserProvider.Get(c.Request.Context(), row.ProviderBrowserID)
		if statusErr == nil && remote.Status == "active" && remote.CDPURL != "" {
			row.CDPURL = remote.CDPURL
			row.LiveURL = remote.LiveURL
			row.Status = remote.Status
			row.ProviderStartedAt = remote.StartedAt
			row.ProviderTimeoutAt = remote.TimeoutAt
			row.ProviderCheckedAt = &now
			row.LastUsedAt = &now
			if err := g.wdb.Db.Save(&row).Error; err != nil {
				g.internalError(c, err)
				return
			}
			c.JSON(200, gin.H{"browser": row, "cached": false, "providerValidated": true})
			return
		}
		if statusErr != nil && !resourcebrowser.IsBrowserProviderNotFound(statusErr) {
			c.JSON(http.StatusBadGateway, gin.H{"error": statusErr.Error()})
			return
		}
	}
	if row.ProviderBrowserID != "" {
		if err := g.browserProvider.Stop(c.Request.Context(), row.ProviderBrowserID); err != nil && !resourcebrowser.IsBrowserProviderNotFound(err) {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
	}
	row.ProviderBrowserID = ""
	row.CDPURL = ""
	row.LiveURL = ""
	row.Status = "stopped"
	row.ProviderStartedAt = nil
	row.ProviderTimeoutAt = nil
	row.ProviderCheckedAt = &now
	if err := g.wdb.Db.Save(&row).Error; err != nil {
		g.internalError(c, err)
		return
	}
	sess, err := g.browserProvider.Start(c.Request.Context(), resourcebrowser.BrowserConfig{ProxyCountryCode: row.ProxyCountryCode, TimeoutMinutes: row.TimeoutMinutes, ProfileName: row.ProfileName, ProfileID: row.ProviderProfileID})
	if err != nil {
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	now = time.Now()
	row.ProviderBrowserID = sess.ID
	row.ProviderProfileID = sess.ProfileID
	row.CDPURL = sess.CDPURL
	row.LiveURL = sess.LiveURL
	row.Status = sess.Status
	row.ProviderStartedAt = sess.StartedAt
	row.ProviderTimeoutAt = sess.TimeoutAt
	row.ProviderCheckedAt = &now
	row.LastUsedAt = &now
	if err := g.wdb.Db.Save(&row).Error; err != nil {
		g.internalError(c, err)
		return
	}
	c.JSON(200, gin.H{"browser": row, "reset": reset})
}

func (g *Resouces) lockBrowserAccessKey(accessKeyID string) func() {
	g.browserMu.Lock()
	lock := g.browserLocks[accessKeyID]
	if lock == nil {
		lock = &sync.Mutex{}
		g.browserLocks[accessKeyID] = lock
	}
	g.browserMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (g *Resouces) requireGatewayAPIKey(c *gin.Context) {
	raw := firstNonEmpty(bearer(c.GetHeader("Authorization")), c.GetHeader("X-Gateway-API-Key"))
	if raw == "" {
		c.AbortWithStatusJSON(401, gin.H{"error": "gateway api key is required"})
		return
	}
	var key schema.AccessKey
	if err := g.wdb.Db.Where("key_hash = ? AND status = ?", hashSecret(raw), schema.StatusActive).First(&key).Error; err != nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "invalid gateway api key"})
		return
	}
	now := time.Now()
	_ = g.wdb.Db.Model(&key).Update("last_used_at", now).Error
	c.Set(gatewayPrincipalContext, gatewayPrincipal{AccessKey: key})
}

func (g *Resouces) requireResourceScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := mustGatewayPrincipal(c).AccessKey
		allowed := map[string]bool{
			resourceScopeGoogle:   key.AllowGoogle,
			resourceScopeBrowser:  key.AllowBrowser,
			resourceScopeTelegram: key.AllowTelegram,
		}[scope]
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": scope + " resource is not allowed for this api key", "resource": scope})
		}
	}
}

func mustGatewayPrincipal(c *gin.Context) gatewayPrincipal {
	return c.MustGet(gatewayPrincipalContext).(gatewayPrincipal)
}
func (g *Resouces) internalError(c *gin.Context, err error) {
	log.Error("request failed", "err", err)
	c.JSON(500, gin.H{"error": "internal server error"})
}
func bearer(v string) string {
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	return ""
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
