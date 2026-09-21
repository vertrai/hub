package manager

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
)

func commercePublished(a schema.AgentCatalogEntry) bool {
	if a.Web != nil {
		return a.Web.Published
	}
	return a.Published
}

func webSubscriptionRequired(a schema.AgentCatalogEntry) bool {
	if a.Web != nil && a.Web.SubscriptionEnabled != nil {
		return *a.Web.SubscriptionEnabled
	}
	return a.StripePriceID != ""
}
func webFreeEnabled(a schema.AgentCatalogEntry) bool {
	return a.Web != nil && !a.Web.InviteEnabled && !webSubscriptionRequired(a)
}

func validateWebCatalog(a schema.AgentCatalogEntry) error {
	if a.Web == nil {
		return nil
	}
	if a.Web.Published && webSubscriptionRequired(a) && a.StripePriceID == "" {
		return errors.New("请选择 Stripe 订阅价格后再上架")
	}
	if a.Web.Published && a.ProductID == "" {
		return errors.New("网页上架需要填写网站商品标识")
	}
	for locale, copy := range a.Web.Content {
		if locale != "en" && locale != "zh" {
			return errors.New("网页语言仅支持 en 和 zh")
		}
		for _, value := range []string{copy.Name, copy.Intro, copy.Summary, copy.CompatibilityNote, copy.ConsentText} {
			if utf8.RuneCountInString(value) > 2000 {
				return errors.New("网页文案不能超过 2000 字")
			}
		}
		if len(copy.Capabilities) > 8 {
			return errors.New("网页最多填写 8 项能力")
		}
		for _, value := range copy.Capabilities {
			if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 120 {
				return errors.New("网页能力需填写 1 至 120 字")
			}
		}
	}
	return nil
}

func webLocale(c *gin.Context) string {
	if c.Query("locale") == "zh" {
		return "zh"
	}
	return "en"
}

func webRequiresConsent(a schema.AgentCatalogEntry) bool { return a.Web != nil }

func (m *Manager) webCatalogEntry(a schema.AgentCatalogEntry, locale string) gin.H {
	name, intro, summary, note := a.Name, a.Intro, "", ""
	var caps []string
	consent := ""
	if a.Web != nil {
		// Apply fallback copy first, then requested language; never synthesize translations.
		other := "zh"
		if locale == "zh" {
			other = "en"
		}
		for _, lang := range []string{other, locale} {
			copy := a.Web.Content[lang]
			if copy.Name != "" {
				name = copy.Name
			}
			if copy.Intro != "" {
				intro = copy.Intro
			}
			if copy.Summary != "" {
				summary = copy.Summary
			}
			if copy.CompatibilityNote != "" {
				note = copy.CompatibilityNote
			}
			if len(copy.Capabilities) > 0 {
				caps = copy.Capabilities
			}
		}
	}
	if summary == "" {
		summary = intro
	}
	if caps == nil {
		caps = []string{}
	}
	published := a.Web != nil && a.Web.Published
	ready := published && a.ProductID != "" && a.Module != "" && validateDeploymentConfig(m.config.Deployment) == nil
	cfg := m.config.Stripe
	subscription := ready && webSubscriptionRequired(a) && a.StripePriceID != "" && cfg.Enabled && cfg.SecretKey != "" && cfg.WebhookSecret != "" && cfg.SuccessURL != "" && cfg.CancelURL != ""
	invite := ready && a.Web.InviteEnabled
	return gin.H{
		"id": a.ID, "name": name, "logoUrl": a.LogoURL, "intro": intro, "summary": summary, "capabilities": caps,
		"compatibilityNote": note, "consentText": strings.TrimSpace(consent), "published": published,
		"acquisition": gin.H{"productId": a.ProductID, "invite": gin.H{"enabled": invite}, "subscription": gin.H{"enabled": subscription}, "free": gin.H{"enabled": ready && webFreeEnabled(a)}},
		"connection":  gin.H{"type": "telegram"},
	}
}

// New website routes do not alter the public WeChat route, payload or publication rules.
func webCatalogQuery(c *gin.Context) bool {
	if channel := c.DefaultQuery("channel", "web"); channel != "web" {
		c.JSON(400, gin.H{"error": "use the existing WeChat catalog for that channel"})
		return false
	}
	if locale := c.DefaultQuery("locale", "en"); locale != "en" && locale != "zh" {
		c.JSON(400, gin.H{"error": "unsupported locale"})
		return false
	}
	return true
}
func (m *Manager) listWebCatalog(c *gin.Context) {
	if !webCatalogQuery(c) || !m.catalogDB(c) {
		return
	}
	var rows []schema.AgentCatalogEntry
	if err := m.wdb.Db.Find(&rows).Error; err != nil {
		catalogError(c, err)
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := 0, 0
		if rows[i].Web != nil {
			left = rows[i].Web.SortOrder
		}
		if rows[j].Web != nil {
			right = rows[j].Web.SortOrder
		}
		if left == right {
			return rows[i].ID < rows[j].ID
		}
		return left < right
	})
	agents := []gin.H{}
	for _, a := range rows {
		if a.Web != nil && a.Web.Published {
			agents = append(agents, m.webCatalogEntry(a, webLocale(c)))
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"agents": agents})
}
func (m *Manager) getWebCatalogEntry(c *gin.Context) {
	if !webCatalogQuery(c) || !m.catalogDB(c) {
		return
	}
	var a schema.AgentCatalogEntry
	if err := m.wdb.Db.First(&a, "id = ?", c.Param("id")).Error; err != nil {
		catalogError(c, err)
		return
	}
	if a.Web == nil || !a.Web.Published {
		c.JSON(404, gin.H{"error": "agent is not available on the website"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, m.webCatalogEntry(a, webLocale(c)))
}
