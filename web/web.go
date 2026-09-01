// Package web owns the administration frontend shared by the resouces and
// manager services. Backend packages mount these routes without owning copies
// of the frontend assets.
package web

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

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

// RegisterRoutes mounts the shared administration frontend on a backend.
func RegisterRoutes(routes *gin.Engine, authentication gin.HandlerFunc) {
	routes.GET("/admin/login", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", loginHTML) })
	protected := routes.Group("", authentication)
	protected.GET("/admin", adminPage)
	protected.GET("/admin/users", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", usersHTML) })
	protected.GET("/admin/google", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", googleHTML) })
	protected.GET("/admin/browser", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", browserHTML) })
	protected.GET("/admin/xbot", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", xbotHTML) })
	protected.GET("/admin/telegram", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", telegramHTML) })
	protected.GET("/admin/weixin", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", weixinHTML) })
	protected.GET("/admin/hymatrix", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", hymatrixHTML) })
	protected.GET("/admin/hymatrix/eval", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", hymatrixEvalHTML) })
	protected.GET("/admin/hymatrix/weixin-reset", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", hymatrixWeixinResetHTML) })
	protected.GET("/admin/test", testPage)
	routes.GET("/admin/assets/common.css", func(c *gin.Context) { c.Data(http.StatusOK, "text/css; charset=utf-8", commonCSS) })
	routes.GET("/admin/assets/admin-enhancements.css", func(c *gin.Context) { c.Data(http.StatusOK, "text/css; charset=utf-8", adminEnhancementsCSS) })
	routes.GET("/admin/assets/common.js", func(c *gin.Context) { c.Data(http.StatusOK, "application/javascript; charset=utf-8", commonJS) })
}

func adminPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", adminHTML)
}

func testPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", testHTML)
}
