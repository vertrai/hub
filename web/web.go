// Package web owns the administration frontend shared by the resouces and
// manager services. Backend packages mount these routes without owning copies
// of the frontend assets.
package web

import (
	"bytes"
	_ "embed"
	"html"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed llm_test.html
var llmTestHTML []byte

//go:embed llm-test.js
var llmTestJS []byte

//go:embed llm.js
var llmJS []byte

//go:embed llm.html
var llmHTML []byte

//go:embed admin.html
var adminHTML []byte

//go:embed login.html
var loginHTML []byte

//go:embed test.html
var testHTML []byte

//go:embed users.html
var usersHTML []byte

//go:embed google.html
var googleHTML []byte

//go:embed browser.html
var browserHTML []byte

//go:embed xbot.html
var xbotHTML []byte

//go:embed telegram.html
var telegramHTML []byte

//go:embed weixin.html
var weixinHTML []byte

//go:embed hymatrix.html
var hymatrixHTML []byte

//go:embed hymatrix_eval.html
var hymatrixEvalHTML []byte

//go:embed hymatrix_weixin_reset.html
var hymatrixWeixinResetHTML []byte

//go:embed common.css
var commonCSS []byte

//go:embed common.js
var commonJS []byte

//go:embed admin-enhancements.css
var adminEnhancementsCSS []byte

//go:embed navigation.html
var navigationHTML []byte

// RegisterRoutes mounts the shared administration frontend on a backend.
func RegisterRoutes(routes *gin.Engine, authentication gin.HandlerFunc) {
	routes.GET("/admin/login", func(c *gin.Context) { renderAdminDocument(c, loginHTML) })
	protected := routes.Group("", authentication)
	protected.GET("/admin", adminPage)
	protected.GET("/admin/llm/test", func(c *gin.Context) { renderAdminDocument(c, llmTestHTML) })
	protected.GET("/admin/llm", func(c *gin.Context) { renderAdminDocument(c, llmHTML) })
	protected.GET("/admin/users", func(c *gin.Context) { renderAdminDocument(c, usersHTML) })
	protected.GET("/admin/google", func(c *gin.Context) { renderAdminDocument(c, googleHTML) })
	protected.GET("/admin/browser", func(c *gin.Context) { renderAdminDocument(c, browserHTML) })
	protected.GET("/admin/xbot", func(c *gin.Context) { renderAdminDocument(c, xbotHTML) })
	protected.GET("/admin/telegram", func(c *gin.Context) { renderAdminDocument(c, telegramHTML) })
	protected.GET("/admin/weixin", func(c *gin.Context) { renderAdminDocument(c, weixinHTML) })
	protected.GET("/admin/hymatrix", func(c *gin.Context) { renderAdminDocument(c, hymatrixHTML) })
	protected.GET("/admin/hymatrix/eval", func(c *gin.Context) { renderAdminDocument(c, hymatrixEvalHTML) })
	protected.GET("/admin/hymatrix/weixin-reset", func(c *gin.Context) { renderAdminDocument(c, hymatrixWeixinResetHTML) })
	protected.GET("/admin/test", testPage)
	routes.GET("/admin/assets/common.css", func(c *gin.Context) { c.Data(http.StatusOK, "text/css; charset=utf-8", commonCSS) })
	routes.GET("/admin/assets/admin-enhancements.css", func(c *gin.Context) { c.Data(http.StatusOK, "text/css; charset=utf-8", adminEnhancementsCSS) })
	routes.GET("/admin/assets/llm-test.js", func(c *gin.Context) { c.Data(http.StatusOK, "application/javascript; charset=utf-8", llmTestJS) })
	routes.GET("/admin/assets/llm.js", func(c *gin.Context) { c.Data(http.StatusOK, "application/javascript; charset=utf-8", llmJS) })
	routes.GET("/admin/assets/common.js", func(c *gin.Context) { c.Data(http.StatusOK, "application/javascript; charset=utf-8", commonJS) })
}

func adminPage(c *gin.Context) {
	renderAdminDocument(c, adminHTML)
}

func testPage(c *gin.Context) {
	renderAdminDocument(c, testHTML)
}

// renderAdminDocument supplies the same navigation to every administration
// page before it reaches the browser, avoiding page-dependent menu changes.
func renderAdminDocument(c *gin.Context, page []byte) {
	href := []byte(`href="` + html.EscapeString(c.Request.URL.Path) + `"`)
	active := []byte(`class="active" ` + string(href) + ` aria-current="page"`)
	navigation := bytes.Replace(navigationHTML, href, active, 1)
	body := bytes.Replace(page, []byte("<!-- admin-navigation -->"), navigation, 1)
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}
