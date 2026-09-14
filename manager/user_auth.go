package manager

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm/clause"
)

// Google verification and signing are shared. Administrator authorization is
// evaluated separately against the current allowlist on every admin request.
func (m *Manager) googleLogin(c *gin.Context, admin bool) {
	var req struct {
		IDToken string `json:"id_token"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.IDToken) == "" {
		c.JSON(400, gin.H{"error": "id_token is required"})
		return
	}
	identity, err := m.adminAuth.validator.Validate(c.Request.Context(), req.IDToken, m.adminAuth.clientID)
	if err != nil || identity.Subject == "" || identity.Email == "" {
		c.JSON(401, gin.H{"error": "invalid Google id_token"})
		return
	}
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	if admin {
		if _, ok := m.adminAuth.allowed[identity.Email]; !ok {
			c.JSON(403, gin.H{"code": "admin_not_allowed", "error": "该 Google 账号未被授权为管理员"})
			return
		}
	}
	// Older admin-only tests do not attach a database; ordinary user login always requires it.
	if m.wdb != nil {
		sub := identity.Subject
		user := schema.User{ID: "google_" + sub, GoogleSub: &sub, Email: identity.Email, Name: identity.Name, Picture: identity.Picture, Status: "active"}
		err = m.wdb.Db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"google_sub", "email", "name", "picture", "updated_at"})}).Create(&user).Error
		if err != nil {
			c.JSON(500, gin.H{"error": "cannot save user"})
			return
		}
		if err = m.wdb.Db.First(&user, "id = ?", user.ID).Error; err != nil || user.Status != "active" {
			c.JSON(403, gin.H{"error": "user is disabled"})
			return
		}
	} else if !admin {
		c.JSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	token, err := m.adminAuth.issueSession(identity)
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot create session"})
		return
	}
	// Shared cookie enables an administrator signed in on the ordinary Manager
	// web page to enter /admin without another Google exchange.
	http.SetCookie(c.Writer, adminCookie(token, m.adminAuth.secure, int(m.adminAuth.lifetime.Seconds())))
	c.Header("Cache-Control", "no-store")
	redirect := "/app"
	if admin {
		redirect = "/admin"
	}
	c.JSON(200, gin.H{"accessToken": token, "tokenType": "Bearer", "expiresIn": int(m.adminAuth.lifetime.Seconds()), "user": googleUserResponse(identity), "redirect": redirect})
}

func googleUserResponse(i adminIdentity) gin.H {
	return gin.H{"userId": "google_" + i.Subject, "email": i.Email, "name": i.Name, "picture": i.Picture, "roles": []string{"user"}}
}
func (m *Manager) requireUser(c *gin.Context) {
	// Public API deliberately uses Bearer auth, including same-origin /app. This
	// keeps cross-origin vertr.ai requests independent of cookies and avoids CSRF.
	i, ok := m.adminAuth.verifyIdentity(bearerToken(c))
	if !ok {
		c.AbortWithStatusJSON(401, gin.H{"error": "Google login is required"})
		return
	}
	if m.wdb == nil {
		c.AbortWithStatusJSON(503, gin.H{"error": "manager database is unavailable"})
		return
	}
	var user schema.User
	if m.wdb.Db.First(&user, "id = ? AND status = ?", "google_"+i.Subject, "active").Error != nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "user is unavailable"})
		return
	}
	c.Set("userID", user.ID)
	c.Set("userIdentity", i)
	c.Header("Cache-Control", "no-store")
	c.Next()
}
func mustWebUser(c *gin.Context) string { return c.GetString("userID") }
