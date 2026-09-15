package manager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Settings are encrypted with a key derived from the persistent Manager signing key.
func (m *Manager) stripeSettingsCipher() (cipher.AEAD, error) {
	if m.adminAuth == nil || len(m.adminAuth.privateKey) == 0 {
		return nil, errors.New("persistent signing key is required")
	}
	key := sha256.Sum256(append([]byte("hub/stripe-settings/v1:"), m.adminAuth.privateKey...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (m *Manager) encodeStripeSettings(cfg StripeConfig) ([]byte, error) {
	a, err := m.stripeSettingsCipher()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, raw, []byte("stripe")), nil
}
func (m *Manager) decodeStripeSettings(row schema.StripeSettings) (StripeConfig, error) {
	var cfg StripeConfig
	a, err := m.stripeSettingsCipher()
	if err != nil {
		return cfg, err
	}
	if len(row.EncryptedConfig) < a.NonceSize() {
		return cfg, errors.New("invalid encrypted settings")
	}
	raw, err := a.Open(nil, row.EncryptedConfig[:a.NonceSize()], row.EncryptedConfig[a.NonceSize():], []byte("stripe"))
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(raw, &cfg)
	return cfg, err
}
func (m *Manager) stripeRuntime() (StripeConfig, stripeGateway, error) {
	var row schema.StripeSettings
	if m.wdb == nil {
		return m.config.Stripe, m.stripeAPI, nil
	}
	err := m.wdb.Db.First(&row, "id = ?", "stripe").Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.config.Stripe, m.stripeAPI, nil
	}
	if err != nil {
		return StripeConfig{}, nil, err
	}
	cfg, err := m.decodeStripeSettings(row)
	if err != nil {
		return cfg, nil, err
	}
	return cfg, newStripeGateway(cfg), nil
}
func stripeSettingsResponse(cfg StripeConfig) gin.H {
	return gin.H{"enabled": cfg.Enabled, "secretKeyConfigured": cfg.SecretKey != "", "webhookSecretConfigured": cfg.WebhookSecret != "", "successURL": cfg.SuccessURL, "cancelURL": cfg.CancelURL, "portalReturnURL": cfg.PortalReturnURL, "managedPayments": cfg.ManagedPayments, "requireTermsOfServiceConsent": cfg.RequireTermsOfServiceConsent, "stopAgentOnPaymentFailure": cfg.StopAgentOnPaymentFailure}
}
func (m *Manager) getStripeSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	cfg, _, err := m.stripeRuntime()
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot load Stripe settings; check migration and signing key"})
		return
	}
	c.JSON(200, stripeSettingsResponse(cfg))
}
func validateStripeSettings(cfg StripeConfig) error {
	for _, value := range []string{cfg.SuccessURL, cfg.CancelURL, cfg.PortalReturnURL} {
		if value == "" && !cfg.Enabled {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return errors.New("valid HTTP(S) redirect URLs are required")
		}
	}
	if cfg.Enabled && (cfg.SecretKey == "" || cfg.WebhookSecret == "") {
		return errors.New("Stripe secret key and webhook signing secret are required")
	}
	if cfg.SecretKey != "" && !strings.HasPrefix(cfg.SecretKey, "sk_") && !strings.HasPrefix(cfg.SecretKey, "rk_") {
		return errors.New("use a Stripe secret key, not a publishable key")
	}
	if cfg.WebhookSecret != "" && !strings.HasPrefix(cfg.WebhookSecret, "whsec_") {
		return errors.New("webhook signing secret must start with whsec_")
	}
	return nil
}
func (m *Manager) saveStripeSettings(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	var req struct {
		Enabled                      bool   `json:"enabled"`
		SecretKey                    string `json:"secretKey"`
		WebhookSecret                string `json:"webhookSecret"`
		SuccessURL                   string `json:"successURL"`
		CancelURL                    string `json:"cancelURL"`
		PortalReturnURL              string `json:"portalReturnURL"`
		ManagedPayments              bool   `json:"managedPayments"`
		RequireTermsOfServiceConsent bool   `json:"requireTermsOfServiceConsent"`
		StopAgentOnPaymentFailure    bool   `json:"stopAgentOnPaymentFailure"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "invalid settings"})
		return
	}
	var result StripeConfig
	var validation error
	err := m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		initial, err := m.encodeStripeSettings(m.config.Stripe)
		if err != nil {
			return err
		}
		if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&schema.StripeSettings{ID: "stripe", EncryptedConfig: initial}).Error; err != nil {
			return err
		}
		var row schema.StripeSettings
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", "stripe").Error; err != nil {
			return err
		}
		old, err := m.decodeStripeSettings(row)
		if err != nil {
			return err
		}
		result = StripeConfig{Enabled: req.Enabled, SecretKey: strings.TrimSpace(req.SecretKey), WebhookSecret: strings.TrimSpace(req.WebhookSecret), SuccessURL: strings.TrimSpace(req.SuccessURL), CancelURL: strings.TrimSpace(req.CancelURL), PortalReturnURL: strings.TrimSpace(req.PortalReturnURL), ManagedPayments: req.ManagedPayments, RequireTermsOfServiceConsent: req.RequireTermsOfServiceConsent, StopAgentOnPaymentFailure: req.StopAgentOnPaymentFailure}
		if result.SecretKey == "" {
			result.SecretKey = old.SecretKey
		}
		if result.WebhookSecret == "" {
			result.WebhookSecret = old.WebhookSecret
		}
		if validation = validateStripeSettings(result); validation != nil {
			return validation
		}
		row.EncryptedConfig, err = m.encodeStripeSettings(result)
		if err != nil {
			return err
		}
		return tx.Save(&row).Error
	})
	if validation != nil {
		c.JSON(400, gin.H{"error": validation.Error()})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot save Stripe settings; check migration and signing key"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, stripeSettingsResponse(result))
}
