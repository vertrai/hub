package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
	"github.com/vertrai/hub/manager"
)

var cmds = []*cli.Command{
	{
		Name:  "migrate",
		Usage: "apply database schema changes before starting a new release",
		Action: func(c *cli.Context) error {
			if err := loadConfig(c); err != nil {
				return err
			}
			db, err := manager.NewWdb(viper.GetString("postgres.dsn"))
			if err != nil {
				return err
			}
			defer db.Close()
			return db.Migrate()
		},
	},
	{
		Name:  "start",
		Usage: "run server in daemon mode",
		Action: func(c *cli.Context) error {
			if _, err := os.Stat(Pid); err == nil {
				return errors.New("daemon is already running")
			}
			configPath := c.String("config")
			if configPath == "" {
				configPath = DefaultConfig
			}
			path, err := os.Executable()
			if err != nil {
				return err
			}
			command := exec.Command(path, "--config", configPath)
			logName := fmt.Sprintf("%s_%s_%d.log", Name, Version, time.Now().Unix())
			logFile, err := os.OpenFile(logName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
			if err != nil {
				return err
			}
			defer logFile.Close()
			command.Stdout = logFile
			command.Stderr = logFile
			if err := command.Start(); err != nil {
				return err
			}
			return os.WriteFile(Pid, []byte(fmt.Sprintf("%d", command.Process.Pid)), 0o666)
		},
	},
	{
		Name:  "stop",
		Usage: "stop server in daemon mode",
		Action: func(_ *cli.Context) error {
			pid, err := os.ReadFile(Pid)
			if err != nil {
				return err
			}
			if err := exec.Command("kill", string(pid)).Run(); err != nil {
				return err
			}
			if err := os.Remove(Pid); err != nil {
				return err
			}
			log.Info("server stopped")
			return nil
		},
	},
}
