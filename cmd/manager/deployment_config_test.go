package main

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func deploymentTestConfig(t *testing.T, raw string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestDeploymentConfigSharedAndLegacyMigration(t *testing.T) {
	unified := deploymentTestConfig(t, `deployment:
  nodeURL: https://node
  adminURL: http://admin
  privateKey: secret
  gatewayURL: https://hub
  hermesGatewayToken: gateway-secret
miniProgram:
  appId: wx-test
`)
	cfg, err := readDeploymentConfig(unified)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeURL != "https://node" || cfg.AdminURL != "http://admin" || cfg.RuntimeType != "hermes" || cfg.GatewayURL != "https://hub" {
		t.Fatal("incorrect shared config")
	}
	legacy := deploymentTestConfig(t, `miniProgram:
  pod:
    nodeURL: https://node
    adminURL: http://admin
    privateKey: secret
  agent:
    gatewayURL: https://hub
    hermesGatewayToken: gateway-secret
`)
	previous, err := readDeploymentConfig(legacy)
	if err != nil || previous != cfg {
		t.Fatal("legacy deployment was not preserved", err)
	}
	legacy.Set("deployment.privateKey", "different-secret")
	_, err = readDeploymentConfig(legacy)
	if err == nil || strings.Contains(err.Error(), "different-secret") {
		t.Fatal("conflicting configuration must fail without exposing secrets")
	}
}
func TestDeploymentConfigRejectsRemovedCommerce(t *testing.T) {
	v := deploymentTestConfig(t, "commerce:\n  nodeURL: https://old\n")
	_, err := readDeploymentConfig(v)
	if err == nil || !strings.Contains(err.Error(), "/admin/agents") {
		t.Fatal("missing migration instructions")
	}
}
