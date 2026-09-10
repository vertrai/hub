package manager

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/api/idtoken"
)

const adminSessionCookie = "hub_manager_admin_session"

type adminIdentity struct{ Subject, Email, Name, Picture string }

type googleTokenValidator interface {
	Validate(context.Context, string, string) (adminIdentity, error)
}
type googleIDTokenValidator struct{}

func (googleIDTokenValidator) Validate(ctx context.Context, raw, audience string) (adminIdentity, error) {
	payload, err := idtoken.Validate(ctx, raw, audience)
	if err != nil {
		return adminIdentity{}, err
	}
	email, _ := payload.Claims["email"].(string)
	verified, _ := payload.Claims["email_verified"].(bool)
	if payload.Subject == "" || strings.TrimSpace(email) == "" || !verified {
		return adminIdentity{}, errors.New("Google identity must contain a subject and verified email")
	}
	name, _ := payload.Claims["name"].(string)
	picture, _ := payload.Claims["picture"].(string)
	return adminIdentity{Subject: payload.Subject, Email: strings.ToLower(strings.TrimSpace(email)), Name: name, Picture: picture}, nil
}

type adminTokenClaims struct {
	Email   string `json:"email"`
	Name    string `json:"name,omitempty"`
	Picture string `json:"picture,omitempty"`
	jwt.RegisteredClaims
}

type adminAuthenticator struct {
	clientID, issuer, audience string
	allowed                    map[string]struct{}
	privateKey                 ed25519.PrivateKey
	publicKey                  ed25519.PublicKey
	validator                  googleTokenValidator
	secure                     bool
	lifetime                   time.Duration
}

func newAdminAuthenticator(config AdminGoogleConfig) (*adminAuthenticator, error) {
	allowed := make(map[string]struct{}, len(config.AllowedEmails))
	for _, email := range config.AllowedEmails {
		if normalized := strings.ToLower(strings.TrimSpace(email)); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	if config.AccessTokenTTL <= 0 {
		config.AccessTokenTTL = 10 * time.Hour
	}
	if config.JWTIssuer == "" {
		config.JWTIssuer = "agent-hub-auth"
	}
	if config.JWTAudience == "" {
		config.JWTAudience = "manager"
	}
	configured := config.ClientID != "" || config.PrivateKeyFile != "" || config.PublicKeyFile != "" || len(allowed) > 0
	auth := &adminAuthenticator{clientID: config.ClientID, issuer: config.JWTIssuer, audience: config.JWTAudience, allowed: allowed, validator: googleIDTokenValidator{}, secure: config.CookieSecure, lifetime: config.AccessTokenTTL}
	if !configured {
		return auth, nil
	}
	if config.ClientID == "" || config.PrivateKeyFile == "" || config.PublicKeyFile == "" || len(allowed) == 0 {
		return nil, errors.New("admin Google clientId, allowedEmails, jwt privateKeyFile and publicKeyFile are required")
	}
	privateKey, err := loadAdminPrivateKey(config.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load admin JWT private key: %w", err)
	}
	publicKey, err := loadAdminPublicKey(config.PublicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load admin JWT public key: %w", err)
	}
	if !privateKey.Public().(ed25519.PublicKey).Equal(publicKey) {
		return nil, errors.New("admin JWT private and public keys do not match")
	}
	auth.privateKey, auth.publicKey = privateKey, publicKey
	return auth, nil
}

func loadAdminPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private key PEM not found")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not ed25519")
	}
	return key, nil
}

func loadAdminPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("public key PEM not found")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not ed25519")
	}
	return key, nil
}

func (a *adminAuthenticator) issueSession(identity adminIdentity) (string, error) {
	if len(a.privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("admin JWT signer is not configured")
	}
	now := time.Now()
	claims := adminTokenClaims{Email: identity.Email, Name: identity.Name, Picture: identity.Picture, RegisteredClaims: jwt.RegisteredClaims{Issuer: a.issuer, Subject: identity.Subject, Audience: jwt.ClaimStrings{a.audience}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(a.lifetime))}}
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(a.privateKey)
}

func (a *adminAuthenticator) verifySession(raw string) (adminIdentity, bool) {
	claims := &adminTokenClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA {
			return nil, errors.New("unexpected signing method")
		}
		return a.publicKey, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(a.issuer), jwt.WithAudience(a.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute))
	if err != nil || token == nil || !token.Valid || claims.Subject == "" || claims.Email == "" {
		return adminIdentity{}, false
	}
	if _, ok := a.allowed[strings.ToLower(claims.Email)]; !ok {
		return adminIdentity{}, false
	}
	return adminIdentity{Subject: claims.Subject, Email: claims.Email, Name: claims.Name, Picture: claims.Picture}, true
}

func adminCookie(value string, secure bool, maxAge int) *http.Cookie {
	return &http.Cookie{Name: adminSessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}
func (m *Manager) adminAuthInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"googleClientId": m.adminAuth.clientID})
}

func (m *Manager) adminGoogleLogin(c *gin.Context) {
	var req struct {
		IDToken string `json:"id_token"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.IDToken) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id_token is required"})
		return
	}
	identity, err := m.adminAuth.validator.Validate(c.Request.Context(), req.IDToken, m.adminAuth.clientID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid Google id_token"})
		return
	}
	if _, ok := m.adminAuth.allowed[identity.Email]; !ok {
		c.JSON(http.StatusForbidden, gin.H{"code": "admin_not_allowed", "error": "该 Google 账号未被授权为管理员"})
		return
	}
	token, err := m.adminAuth.issueSession(identity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot create administrator session"})
		return
	}
	http.SetCookie(c.Writer, adminCookie(token, m.adminAuth.secure, int(m.adminAuth.lifetime.Seconds())))
	c.JSON(http.StatusOK, gin.H{"user": identity, "redirect": "/admin"})
}

func (m *Manager) currentAdmin(c *gin.Context) (adminIdentity, bool) {
	cookie, err := c.Request.Cookie(adminSessionCookie)
	if err != nil || cookie.Value == "" {
		return adminIdentity{}, false
	}
	return m.adminAuth.verifySession(cookie.Value)
}
func (m *Manager) requireAdminPage(c *gin.Context) {
	if _, ok := m.currentAdmin(c); !ok {
		c.Redirect(http.StatusFound, "/admin/login")
		c.Abort()
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Next()
}
func (m *Manager) requireAdmin(c *gin.Context) {
	identity, ok := m.currentAdmin(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Google administrator login is required"})
		return
	}
	c.Set("adminEmail", identity.Email)
	c.Header("Cache-Control", "no-store")
	c.Next()
}
func (m *Manager) adminMe(c *gin.Context) {
	identity, ok := m.currentAdmin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": identity.Email, "name": identity.Name, "picture": identity.Picture})
}
func (m *Manager) adminLogout(c *gin.Context) {
	http.SetCookie(c.Writer, adminCookie("", m.adminAuth.secure, -1))
	c.Redirect(http.StatusFound, "/admin/login")
}
