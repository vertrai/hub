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
)

var log = common.NewLog("manager")

type Config struct {
	AdminGoogle AdminGoogleConfig
	Resources   ResourcesConfig
	MiniProgram MiniProgramConfig
}

type MiniProgramConfig struct {
	AppID, AppSecret, WeixinAPIBase           string
	NodeURL, NodeAdminURL, PrivateKey, Module string
	RuntimeType, GatewayURL                   string
	HermesGatewayToken                        string
	LLMAPIKey, LLMBaseURL                     string
	LLMModel, LLMProvider                     string
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
		weixinAttempts:        make(map[string]weixinAttempt),
		weixinBaseURL:         "https://ilinkai.weixin.qq.com",
		weixinClient:          &http.Client{Timeout: 15 * time.Second},
		adminAuth:             auth,
		miniProgramHTTPClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (m *Manager) Run(endpoint string) { go m.runJobs(); go m.runAPI(endpoint) }
func (m *Manager) Close() {
	if m.apiServer != nil {
		_ = m.apiServer.Shutdown(context.Background())
	}
	if m.wdb != nil {
		_ = m.wdb.Close()
	}
}
