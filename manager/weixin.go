package manager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm/clause"
	"rsc.io/qr"
)

const (
	weixinAttemptLifetime         = 10 * time.Minute
	weixinQRCodeEstimatedLifetime = 90 * time.Second
)

type WeixinCredentials struct {
	BotID     string
	AccountID string
	Token     string
	BaseURL   string
	UserID    string
}

type weixinAttempt struct {
	ID, UserID, PollSecret, QRContent, ProviderBase string
	ExpiresAt, CredentialExpiresAt                  time.Time
	Credentials                                     *WeixinCredentials
	Polling                                         bool
}

type weixinOnboardingStart struct {
	AttemptID       string
	QRImage         string
	ExpiresAt       time.Time
	ExpireTime      int
	IntervalSeconds int
}

type weixinOnboardingState struct {
	State, BotID, AccountID, UserID string
}

func (m *Manager) startWeixinOnboarding(c *gin.Context) {
	var input struct {
		UserID string `json:"userId"`
	}
	if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.UserID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId is required"})
		return
	}
	input.UserID = strings.TrimSpace(input.UserID)
	if m.wdb != nil {
		var count int64
		if err := m.wdb.Db.Model(&schema.User{}).Where("id = ?", input.UserID).Count(&count).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		} else if count == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "manager user not found"})
			return
		}
	}
	result, err := m.createWeixinOnboarding(c.Request.Context(), input.UserID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"attemptId": result.AttemptID, "qrImage": result.QRImage, "expiresAt": result.ExpiresAt, "expireTime": result.ExpireTime, "intervalSeconds": result.IntervalSeconds})
}

func (m *Manager) createWeixinOnboarding(ctx context.Context, userID string) (weixinOnboardingStart, error) {
	var payload struct {
		QRCode     string `json:"qrcode"`
		Image      string `json:"qrcode_img_content"`
		ExpireTime int    `json:"expire_time"`
	}
	if err := m.weixinGET(ctx, m.weixinBaseURL+"/ilink/bot/get_bot_qrcode?bot_type=3", &payload); err != nil {
		return weixinOnboardingStart{}, err
	}
	if payload.QRCode == "" {
		return weixinOnboardingStart{}, fmt.Errorf("iLink QR response is incomplete")
	}
	content := payload.Image
	if content == "" {
		content = payload.QRCode
	}
	code, err := qr.Encode(content, qr.M)
	if err != nil {
		return weixinOnboardingStart{}, fmt.Errorf("encode iLink QR code: %w", err)
	}
	qrDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
	attempt := weixinAttempt{ID: uuid.NewString(), UserID: userID, PollSecret: payload.QRCode, QRContent: content, ProviderBase: m.weixinBaseURL, ExpiresAt: time.Now().UTC().Add(weixinAttemptLifetime)}
	m.weixinMu.Lock()
	for id, old := range m.weixinAttempts {
		deadline := old.ExpiresAt
		if old.Credentials != nil {
			deadline = old.CredentialExpiresAt
		}
		if old.UserID == attempt.UserID || time.Now().UTC().After(deadline) {
			delete(m.weixinAttempts, id)
		}
	}
	m.weixinAttempts[attempt.ID] = attempt
	m.weixinMu.Unlock()
	qrLifetime := weixinQRCodeEstimatedLifetime
	if payload.ExpireTime > 0 && time.Duration(payload.ExpireTime)*time.Second <= weixinAttemptLifetime {
		qrLifetime = time.Duration(payload.ExpireTime) * time.Second
	}
	expiresAt := time.Now().UTC().Add(qrLifetime)
	return weixinOnboardingStart{AttemptID: attempt.ID, QRImage: qrDataURL, ExpiresAt: expiresAt, ExpireTime: int(qrLifetime.Seconds()), IntervalSeconds: 2}, nil
}

func (m *Manager) pollWeixinOnboarding(c *gin.Context) {
	state, status, err := m.pollWeixinOnboardingAttempt(c.Request.Context(), c.Param("attempt"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(status, gin.H{"state": state.State, "botId": state.BotID, "accountId": state.AccountID, "userId": state.UserID})
}

func (m *Manager) pollWeixinOnboardingAttempt(ctx context.Context, attemptID string) (weixinOnboardingState, int, error) {
	m.weixinMu.Lock()
	attempt, ok := m.weixinAttempts[attemptID]
	if ok && attempt.Polling {
		m.weixinMu.Unlock()
		return weixinOnboardingState{}, http.StatusConflict, fmt.Errorf("Weixin onboarding poll is already in progress")
	}
	if ok {
		attempt.Polling = true
		m.weixinAttempts[attempt.ID] = attempt
	}
	m.weixinMu.Unlock()
	if !ok {
		return weixinOnboardingState{}, http.StatusNotFound, fmt.Errorf("Weixin onboarding attempt not found")
	}
	defer func() {
		m.weixinMu.Lock()
		if current, exists := m.weixinAttempts[attempt.ID]; exists {
			current.Polling = false
			m.weixinAttempts[attempt.ID] = current
		}
		m.weixinMu.Unlock()
	}()
	if attempt.Credentials != nil {
		if time.Now().UTC().After(attempt.CredentialExpiresAt) {
			m.consumeWeixinAttempt(attempt.ID)
			return weixinOnboardingState{State: "expired"}, http.StatusGone, nil
		}
		return weixinOnboardingState{State: "connected", BotID: attempt.Credentials.BotID, AccountID: attempt.Credentials.AccountID, UserID: attempt.Credentials.UserID}, http.StatusOK, nil
	}
	if time.Now().UTC().After(attempt.ExpiresAt) {
		m.consumeWeixinAttempt(attempt.ID)
		return weixinOnboardingState{State: "expired"}, http.StatusGone, nil
	}
	var payload map[string]any
	endpoint := strings.TrimRight(attempt.ProviderBase, "/") + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(attempt.PollSecret)
	if err := m.weixinGET(ctx, endpoint, &payload); err != nil {
		return weixinOnboardingState{}, http.StatusBadGateway, err
	}
	status, _ := payload["status"].(string)
	if status == "scaned_but_redirect" {
		if host, _ := payload["redirect_host"].(string); host != "" {
			base, err := allowedWeixinURL("https://" + host)
			if err != nil {
				return weixinOnboardingState{}, http.StatusBadGateway, err
			}
			attempt.ProviderBase = base
		}
	}
	if status == "expired" {
		m.consumeWeixinAttempt(attempt.ID)
		return weixinOnboardingState{State: "expired"}, http.StatusOK, nil
	}
	if status != "confirmed" {
		state := "waiting"
		if status == "scaned" || status == "scaned_but_redirect" {
			state = "scanned"
		}
		m.updateWeixinAttempt(attempt)
		return weixinOnboardingState{State: state}, http.StatusOK, nil
	}
	credential := &WeixinCredentials{AccountID: stringField(payload, "ilink_bot_id"), Token: stringField(payload, "bot_token"), BaseURL: stringField(payload, "baseurl"), UserID: stringField(payload, "ilink_user_id")}
	if credential.BaseURL == "" {
		credential.BaseURL = m.weixinBaseURL
	}
	base, err := allowedWeixinURL(credential.BaseURL)
	if err != nil || credential.AccountID == "" || credential.Token == "" || credential.UserID == "" {
		return weixinOnboardingState{}, http.StatusBadGateway, fmt.Errorf("iLink credential response is incomplete or untrusted")
	}
	credential.BaseURL = base
	active, storeStatus, storeErr := m.storeConfirmedWeixinAttempt(&attempt, credential)
	if storeErr != nil {
		return weixinOnboardingState{}, storeStatus, storeErr
	}
	if !active {
		return weixinOnboardingState{State: "expired"}, http.StatusGone, nil
	}
	return weixinOnboardingState{State: "connected", BotID: credential.BotID, AccountID: credential.AccountID, UserID: credential.UserID}, http.StatusOK, nil
}

// storeConfirmedWeixinAttempt linearizes cancellation and credential storage:
// once DELETE has removed the attempt, an in-flight provider poll cannot persist it.
func (m *Manager) storeConfirmedWeixinAttempt(attempt *weixinAttempt, credential *WeixinCredentials) (bool, int, error) {
	m.weixinMu.Lock()
	defer m.weixinMu.Unlock()
	current, exists := m.weixinAttempts[attempt.ID]
	if !exists {
		return false, 0, nil
	}
	attempt.Polling = current.Polling
	if m.wdb != nil {
		bot := schema.WeixinBot{
			ID: "wxb_" + strings.ReplaceAll(uuid.NewString(), "-", ""), UserID: attempt.UserID,
			AccountID: credential.AccountID, Token: credential.Token, BaseURL: credential.BaseURL,
			AllowedUserID: credential.UserID, Status: schema.WeixinBotStatusAvailable, AuthorizedAt: time.Now().UTC(),
		}
		result := m.wdb.Db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "account_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"token": bot.Token, "base_url": bot.BaseURL,
				"allowed_user_id": bot.AllowedUserID, "authorized_at": bot.AuthorizedAt,
			}),
			Where: clause.Where{Exprs: []clause.Expression{
				clause.Eq{Column: clause.Column{Table: "manager_weixin_bots", Name: "user_id"}, Value: bot.UserID},
				clause.Eq{Column: clause.Column{Table: "manager_weixin_bots", Name: "status"}, Value: schema.WeixinBotStatusAvailable},
			}},
		}).Create(&bot)
		if result.Error != nil {
			return true, http.StatusInternalServerError, fmt.Errorf("store Weixin bot: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return true, http.StatusConflict, fmt.Errorf("this Weixin bot belongs to another user or has already been assigned")
		}
		var stored schema.WeixinBot
		if err := m.wdb.Db.Where("account_id = ?", bot.AccountID).First(&stored).Error; err != nil {
			return true, http.StatusInternalServerError, fmt.Errorf("load stored Weixin bot: %w", err)
		}
		credential.BotID = stored.ID
	}
	attempt.Credentials = credential
	attempt.CredentialExpiresAt = time.Now().UTC().Add(24 * time.Hour)
	m.weixinAttempts[attempt.ID] = *attempt
	return true, 0, nil
}

func (m *Manager) cancelWeixinOnboarding(c *gin.Context) {
	m.consumeWeixinAttempt(c.Param("attempt"))
	c.Status(http.StatusNoContent)
}

func (m *Manager) getWeixinOnboardingCredentials(c *gin.Context) {
	m.weixinMu.Lock()
	defer m.weixinMu.Unlock()
	attempt, ok := m.weixinAttempts[c.Param("attempt")]
	if !ok || attempt.Credentials == nil || time.Now().UTC().After(attempt.CredentialExpiresAt) {
		c.JSON(http.StatusConflict, gin.H{"error": "Weixin binding is not confirmed or has expired"})
		return
	}
	credentials := attempt.Credentials
	for name, value := range map[string]string{"WEIXIN_ACCOUNT_ID": credentials.AccountID, "WEIXIN_TOKEN": credentials.Token, "WEIXIN_BASE_URL": credentials.BaseURL, "WEIXIN_ALLOWED_USERS": credentials.UserID} {
		if !safeDotenvValue(value) {
			c.JSON(http.StatusBadGateway, gin.H{"error": "iLink returned an unsafe value for " + name})
			return
		}
	}
	env := map[string]string{
		"WEIXIN_ACCOUNT_ID":    credentials.AccountID,
		"WEIXIN_TOKEN":         credentials.Token,
		"WEIXIN_BASE_URL":      credentials.BaseURL,
		"WEIXIN_DM_POLICY":     "allowlist",
		"WEIXIN_ALLOWED_USERS": credentials.UserID,
	}
	dotenv := fmt.Sprintf("WEIXIN_ACCOUNT_ID=%s\nWEIXIN_TOKEN=%s\nWEIXIN_BASE_URL=%s\nWEIXIN_DM_POLICY=allowlist\nWEIXIN_ALLOWED_USERS=%s\n", credentials.AccountID, credentials.Token, credentials.BaseURL, credentials.UserID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"botId": credentials.BotID, "env": env, "dotenv": dotenv})
}

func (m *Manager) listWeixinBots(c *gin.Context) {
	if m.wdb == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "manager database is unavailable"})
		return
	}
	userID := strings.TrimSpace(c.Query("userId"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId is required"})
		return
	}
	query := m.wdb.Db.Where("user_id = ?", userID).Order("created_at desc")
	var bots []schema.WeixinBot
	if err := query.Find(&bots).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": bots})
}

func safeDotenvValue(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsAny(value, "\r\n\x00#\"'")
}

func (m *Manager) updateWeixinAttempt(attempt weixinAttempt) {
	m.weixinMu.Lock()
	if current, ok := m.weixinAttempts[attempt.ID]; ok && current.Credentials == nil {
		attempt.Polling = current.Polling
		m.weixinAttempts[attempt.ID] = attempt
	}
	m.weixinMu.Unlock()
}
func (m *Manager) consumeWeixinAttempt(id string) {
	if id == "" {
		return
	}
	m.weixinMu.Lock()
	delete(m.weixinAttempts, id)
	m.weixinMu.Unlock()
}
func stringField(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func (m *Manager) weixinGET(ctx context.Context, endpoint string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", "131584")
	res, err := m.weixinClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("iLink returned HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(output)
}

func allowedWeixinURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid iLink base URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "weixin.qq.com" && !strings.HasSuffix(host, ".weixin.qq.com") {
		return "", fmt.Errorf("untrusted iLink host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}
