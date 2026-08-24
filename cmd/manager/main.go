package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/inconshreveable/log15"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
	"github.com/vertrai/hub/common"
	"github.com/vertrai/hub/manager"
)

var log = common.NewLog(Name + "-" + Version)

func main() {
	cli.VersionFlag = flagVersion

	app := &cli.App{
		Name:     Name,
		Version:  Version,
		Flags:    flags,
		Commands: cmds,
		Action:   action,
	}

	if err := app.Run(os.Args); err != nil {
		log.Error("run server failed", "err", err)
	}
}

func action(c *cli.Context) error {
	configPath := c.String("config")
	if configPath == "" {
		configPath = DefaultConfig
	}
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		return err
	}
	return run(c)
}

func run(_ *cli.Context) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	ginMode := viper.GetString("ginMode")
	gin.SetMode(ginMode)
	if ginMode == "release" {
		log15.Root().SetHandler(log15.LvlFilterHandler(log15.LvlInfo, log15.StderrHandler))
	}

	wdb, err := manager.NewWdb(viper.GetString("postgres.dsn"))
	if err != nil {
		return err
	}
	service, err := manager.New(viper.GetString("env"), manager.Config{
		AdminGoogle: manager.AdminGoogleConfig{
			ClientID: viper.GetString("auth.google.clientId"), AllowedEmails: viper.GetStringSlice("auth.google.allowedEmails"),
			JWTIssuer: viper.GetString("auth.jwt.issuer"), JWTAudience: viper.GetString("auth.jwt.audience"), PrivateKeyFile: resolveConfigPath(viper.GetString("auth.jwt.privateKeyFile")), PublicKeyFile: resolveConfigPath(viper.GetString("auth.jwt.publicKeyFile")),
			CookieSecure: viper.GetBool("auth.jwt.cookieSecure"), AccessTokenTTL: time.Duration(viper.GetInt("auth.jwt.accessTokenTTLMinutes")) * time.Minute,
		},
		Resources: manager.ResourcesConfig{
			BaseURL:     viper.GetString("resources.baseURL"),
			AdminAPIKey: viper.GetString("resources.adminAPIKey"),
			Timeout:     viper.GetDuration("resources.timeout"),
		},
		MiniProgram: manager.MiniProgramConfig{
			AppID: viper.GetString("miniProgram.appId"), AppSecret: viper.GetString("miniProgram.appSecret"), WeixinAPIBase: viper.GetString("miniProgram.weixinAPIBase"),
			NodeURL: viper.GetString("miniProgram.pod.nodeURL"), PrivateKey: viper.GetString("miniProgram.pod.privateKey"), Module: viper.GetString("miniProgram.pod.module"), RuntimeType: viper.GetString("miniProgram.pod.runtimeType"),
			GatewayURL: viper.GetString("miniProgram.agent.gatewayURL"), HermesGatewayToken: viper.GetString("miniProgram.agent.hermesGatewayToken"),
			LLMAPIKey: viper.GetString("miniProgram.agent.llm.apiKey"), LLMBaseURL: viper.GetString("miniProgram.agent.llm.baseURL"), LLMModel: viper.GetString("miniProgram.agent.llm.model"), LLMProvider: viper.GetString("miniProgram.agent.llm.provider"),
		},
	}, wdb)
	if err != nil {
		_ = wdb.Close()
		return err
	}

	service.Run(viper.GetString("port"))
	<-signals
	service.Close()
	return nil
}

func resolveConfigPath(value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		return value
	}
	resolved, err := filepath.Abs(filepath.Join(filepath.Dir(configFile), value))
	if err != nil {
		return filepath.Join(filepath.Dir(configFile), value)
	}
	return resolved
}
