package manager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/vertrai/hub/common"
	"github.com/vertrai/hub/manager/llm"
)

var log = common.NewLog("manager")

type Config struct {
	Deployment  DeploymentConfig
	Stripe      StripeConfig
	AdminGoogle AdminGoogleConfig
	Resources   ResourcesConfig
	MiniProgram MiniProgramConfig
}

type MiniProgramConfig struct {
	AppID, AppSecret, WeixinAPIBase string
}

// DeploymentConfig is shared by website and mini-program provisioning.
type DeploymentConfig struct {
	NodeURL, AdminURL, PrivateKey               string
	RuntimeType, GatewayURL, HermesGatewayToken string
}

type AdminGoogleConfig struct {
	ClientID                      string
	AllowedEmails                 []string
	JWTIssuer, JWTAudience        string
	PrivateKeyFile, PublicKeyFile string
	CookieSecure                  bool
	AccessTokenTTL                time.Duration
}

type ResourcesConfig struct {
	BaseURL, AdminAPIKey string
	Timeout              time.Duration
}

type Manager struct {
	commerceHymatrix      func(HymatrixConfig) (*HymatrixClient, error)
	commerceContext       context.Context
	commerceCancel        context.CancelFunc
	commerceDone          chan struct{}
	stripeAPI             stripeGateway
	llmOAuthMu            sync.Mutex
	llmOAuthSessions      map[string]*llmOAuthSession
	codexOAuth            *llm.DeviceOAuthClient
	llmMu                 sync.Mutex
	llmClient             *http.Client
	codex                 *llm.CodexAdapter
	env                   string
	config                Config
	wdb                   *Wdb
	resources             *ResourcesClient
	apiServer             *http.Server
	weixinMu              sync.Mutex
	weixinAttempts        map[string]weixinAttempt
	weixinBaseURL         string
	weixinClient          *http.Client
	adminAuth             *adminAuthenticator
	miniProgramHTTPClient *http.Client
	hymatrixAdminClient   *http.Client
}

func New(env string, config Config, wdb *Wdb) (*Manager, error) {

	if config.Resources.Timeout <= 0 {
		config.Resources.Timeout = 30 * time.Second
	}
	auth, err := newAdminAuthenticator(config.AdminGoogle)
	if err != nil {
		return nil, err
	}
	if env != "test" && len(auth.publicKey) == 0 {
		return nil, errors.New("admin Google authentication is required")
	}
	if env == "test" && len(auth.publicKey) == 0 {
		publicKey, privateKey, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			return nil, keyErr
		}
		auth.privateKey, auth.publicKey = privateKey, publicKey
	}
	return &Manager{
		env: env, config: config, wdb: wdb, resources: NewResourcesClient(config.Resources),
		commerceHymatrix:      NewHymatrixClient,
		llmOAuthSessions:      make(map[string]*llmOAuthSession),
		codexOAuth:            llm.NewDeviceOAuthClient(&http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}),
		llmClient:             newLLMClient(),
		codex:                 llm.NewCodexAdapter(newLLMClient()),
		weixinAttempts:        make(map[string]weixinAttempt),
		weixinBaseURL:         "https://ilinkai.weixin.qq.com",
		weixinClient:          &http.Client{Timeout: 15 * time.Second},
		adminAuth:             auth,
		miniProgramHTTPClient: newMiniProgramHTTPClient(),
		hymatrixAdminClient:   &http.Client{Timeout: 2 * time.Minute},
		stripeAPI:             newStripeGateway(config.Stripe),
	}, nil
}

func (m *Manager) Run(endpoint string) {
	m.commerceContext, m.commerceCancel = context.WithCancel(context.Background())
	m.commerceDone = make(chan struct{})
	go func() { defer close(m.commerceDone); m.runJobs() }()
	go m.runAPI(endpoint)
}
func (m *Manager) Close() {
	if m.commerceCancel != nil {
		m.commerceCancel()
		<-m.commerceDone
	}
	if m.apiServer != nil {
		_ = m.apiServer.Shutdown(context.Background())
	}
	if m.wdb != nil {
		_ = m.wdb.Close()
	}
}
