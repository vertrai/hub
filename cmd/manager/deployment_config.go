package main

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
	"github.com/vertrai/hub/manager"
)

func readDeploymentConfig(v *viper.Viper) (manager.DeploymentConfig, error) {
	if v.IsSet("commerce") {
		return manager.DeploymentConfig{}, fmt.Errorf("commerce configuration has moved: keep one deployment block, and configure productId/stripePriceId in /admin/agents; remove commerce after migration")
	}
	values := map[string]string{}
	legacy := map[string]string{"nodeURL": "miniProgram.pod.nodeURL", "adminURL": "miniProgram.pod.adminURL", "privateKey": "miniProgram.pod.privateKey", "runtimeType": "miniProgram.pod.runtimeType", "gatewayURL": "miniProgram.agent.gatewayURL", "hermesGatewayToken": "miniProgram.agent.hermesGatewayToken"}
	for name, oldPath := range legacy {
		current, old := strings.TrimSpace(v.GetString("deployment."+name)), strings.TrimSpace(v.GetString(oldPath))
		if current != "" && old != "" && current != old {
			return manager.DeploymentConfig{}, fmt.Errorf("deployment.%s conflicts with %s; keep only deployment", name, oldPath)
		}
		if current == "" {
			current = old
		}
		values[name] = current
	}
	if values["runtimeType"] == "" {
		values["runtimeType"] = "hermes"
	}
	return manager.DeploymentConfig{NodeURL: values["nodeURL"], AdminURL: values["adminURL"], PrivateKey: values["privateKey"], RuntimeType: values["runtimeType"], GatewayURL: values["gatewayURL"], HermesGatewayToken: values["hermesGatewayToken"]}, nil
}
