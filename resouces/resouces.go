package resouces

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/vertrai/hub/common"
	resourcebrowser "github.com/vertrai/hub/resouces/browser"
	resourcegoogle "github.com/vertrai/hub/resouces/google"
	resourcetelegram "github.com/vertrai/hub/resouces/telegram"
	resourcexbot "github.com/vertrai/hub/resouces/xbot"
)

var log = common.NewLog("resouces")

type Config struct {
	AdminAPIKey                    string
	BrowserAPIKey                  string
	BrowserAPIBaseURL              string
	BrowserTimeoutMinutes          int
	BrowserProxyCountryCode        string
	BrowserStatusCheckInterval     time.Duration
	GoogleCreationCredentials      string
	GoogleCreationAdminEmail       string
	GoogleCreationDomain           string
	GoogleAuthorizationCredentials string
	GoogleAuthorizationDomain      string
	GoogleAuthorizationScopes      []string
	TelegramDataDir                string
	TelegramMinAvailableBots       int
	TelegramBotName                string
	TelegramBotUsernamePrefix      string
	TelegramRequestTimeout         time.Duration
}

type Resouces struct {
	env             string
	config          Config
	wdb             *Wdb
	scheduler       *gocron.Scheduler
	apiServer       *http.Server
	browserProvider resourcebrowser.BrowserProvider
	google          *resourcegoogle.Service
	telegram        *resourcetelegram.Service
	xbot            *resourcexbot.Service
	browserMu       sync.Mutex
	browserLocks    map[string]*sync.Mutex
}

func New(env string, config Config, wdb *Wdb) *Resouces {
	if config.BrowserTimeoutMinutes <= 0 {
		config.BrowserTimeoutMinutes = 240
	}
	if config.BrowserProxyCountryCode == "" {
		config.BrowserProxyCountryCode = "us"
	}
	if config.BrowserAPIBaseURL == "" {
		config.BrowserAPIBaseURL = "https://api.browser-use.com/api/v3"
	}
	if config.BrowserStatusCheckInterval <= 0 {
		config.BrowserStatusCheckInterval = 30 * time.Second
	}
	g := &Resouces{env: env, config: config, wdb: wdb, scheduler: gocron.NewScheduler(time.Local), browserLocks: make(map[string]*sync.Mutex)}
	g.browserProvider = resourcebrowser.NewBrowserUseProvider(config.BrowserAPIKey, config.BrowserAPIBaseURL)
	googleCreator := resourcegoogle.NewWorkspaceAdminCreator(config.GoogleCreationCredentials, config.GoogleCreationAdminEmail)
	tokenIssuer := resourcegoogle.NewCachedGoogleTokenIssuer(
		resourcegoogle.NewDWDTokenIssuer(config.GoogleAuthorizationCredentials, config.GoogleAuthorizationDomain, config.GoogleAuthorizationScopes),
		5*time.Minute,
	)
	if wdb != nil {
		g.google = resourcegoogle.NewService(wdb.Db, googleCreator, tokenIssuer, config.GoogleCreationDomain)
		g.telegram = resourcetelegram.New(wdb.Db, resourcetelegram.Config{
			DataDir: config.TelegramDataDir, MinAvailableTokens: config.TelegramMinAvailableBots,
			BotName: config.TelegramBotName, BotUsernamePrefix: config.TelegramBotUsernamePrefix,
			RequestTimeout: config.TelegramRequestTimeout,
		})
		g.xbot = resourcexbot.New(wdb.Db)
		_, _ = g.scheduler.Every(1).Minute().Do(func() { g.telegram.EnsureBotTokens(context.Background()) })
	}
	return g
}

func (g *Resouces) Run(endpoint string) { go g.runJobs(); go g.runAPI(endpoint) }

func (g *Resouces) Close() {
	g.scheduler.Stop()
	if g.apiServer != nil {
		_ = g.apiServer.Shutdown(context.Background())
	}
	if g.wdb != nil {
		_ = g.wdb.Close()
	}
}
