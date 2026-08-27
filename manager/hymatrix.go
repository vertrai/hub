package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/everFinance/goether"
	"github.com/hymatrix/hymx/sdk"
	serverSchema "github.com/hymatrix/hymx/server/schema"
	"github.com/permadao/goar"
	goarSchema "github.com/permadao/goar/schema"
)

const containerEnvTagPrefix = "Container-Env-"

var nodeInfoHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type HymatrixNodeInfo struct {
	Protocol    string `json:"Protocol"`
	NodeVersion string `json:"Node-Version"`
	Node        struct {
		AccountID string `json:"Acc-Id"`
		Name      string `json:"Name"`
		URL       string `json:"URL"`
	} `json:"Node"`
}

type HymatrixConfig struct {
	NodeURL, PrivateKey, Module, Scheduler       string
	LLMAPIKey, LLMBaseURL, LLMModel, LLMProvider string
}

type HymatrixClient struct {
	config HymatrixConfig
	sdk    hymatrixSpawnSDK
}

type hymatrixSpawnSDK interface {
	SpawnAndWait(module, scheduler string, params []goarSchema.Tag) (*serverSchema.Response, error)
	SendMessageWithEncryptedParamsAndWait(target, data string, params, encryptedParams []goarSchema.Tag) (*serverSchema.Response, error)
}

func NewHymatrixClient(config HymatrixConfig) (*HymatrixClient, error) {
	if strings.TrimSpace(config.NodeURL) == "" || strings.TrimSpace(config.PrivateKey) == "" || strings.TrimSpace(config.Module) == "" || strings.TrimSpace(config.Scheduler) == "" {
		return nil, fmt.Errorf("hymatrix nodeUrl, privateKey, module and scheduler are required")
	}
	signer, err := goether.NewSigner(config.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("create hymatrix signer: %w", err)
	}
	bundler, err := goar.NewBundler(signer)
	if err != nil {
		return nil, fmt.Errorf("create hymatrix bundler: %w", err)
	}
	return &HymatrixClient{config: config, sdk: sdk.NewFromBundler(config.NodeURL, bundler)}, nil
}

func (h *HymatrixClient) Spawn(_ context.Context, in PodSpawnInput) (string, error) {
	tags, err := buildPodSpawnTags(h.config, in)
	if err != nil {
		return "", err
	}
	tags = append(tags, goarSchema.Tag{
		Name:  "Hub-Spawn-Timestamp",
		Value: strconv.FormatInt(time.Now().UnixNano(), 10),
	})
	res, err := h.sdk.SpawnAndWait(h.config.Module, h.config.Scheduler, tags)
	if err != nil {
		return "", err
	}
	return res.Id, nil
}

func (h *HymatrixClient) StartAgent(_ context.Context, pid string, in PodStartInput) error {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return fmt.Errorf("pid is required")
	}
	plain, secret, err := buildStartAgentTagSets(h.config, in)
	if err != nil {
		return err
	}
	if _, err := h.sdk.SendMessageWithEncryptedParamsAndWait(pid, "", plain, secret); err != nil {
		return fmt.Errorf("start Hermes agent: %w", err)
	}
	return nil
}

func buildPodSpawnTags(config HymatrixConfig, in PodSpawnInput) ([]goarSchema.Tag, error) {
	_ = config
	runtimeType := strings.TrimSpace(in.RuntimeType)
	if runtimeType == "" {
		return nil, fmt.Errorf("runtimeType is required")
	}
	return []goarSchema.Tag{{Name: containerEnvTagPrefix + "RUNTIME_TYPE", Value: runtimeType}}, nil
}

func buildStartAgentTagSets(config HymatrixConfig, in PodStartInput) ([]goarSchema.Tag, []goarSchema.Tag, error) {
	if strings.TrimSpace(in.GatewayURL) == "" || strings.TrimSpace(in.GatewayAPIKey) == "" {
		return nil, nil, fmt.Errorf("gateway URL and API key are required")
	}
	if strings.TrimSpace(in.HermesGatewayToken) == "" {
		return nil, nil, fmt.Errorf("Hermes gateway token is required")
	}
	weixinValues := []string{in.WeixinAccountID, in.WeixinToken, in.WeixinBaseURL, in.WeixinAllowedUsers}
	weixinConfigured := 0
	for _, value := range weixinValues {
		if strings.TrimSpace(value) != "" {
			weixinConfigured++
		}
	}
	if weixinConfigured != 0 && weixinConfigured != len(weixinValues) {
		return nil, nil, fmt.Errorf("Weixin account ID, token, base URL and allowed users are required as a complete set")
	}
	provider := strings.TrimSpace(config.LLMProvider)
	if provider == "" {
		provider = "custom"
	}
	plainValues := [][2]string{
		{"HERMES_AGENT_LLM_PROVIDER", provider},
		{"HERMES_AGENT_LLM_MODEL", config.LLMModel},
		{"HERMES_AGENT_LLM_BASE_URL", config.LLMBaseURL},
		{"HUB_GATEWAY_URL", in.GatewayURL},
		{"API_SERVER_ENABLED", "true"},
	}
	secretValues := [][2]string{{"HERMES_AGENT_LLM_API_KEY", config.LLMAPIKey}, {"HUB_GATEWAY_API_KEY", in.GatewayAPIKey}, {"API_SERVER_KEY", in.HermesGatewayToken}, {"HERMES_GATEWAY_TOKEN", in.HermesGatewayToken}}
	if in.BotToken != "" {
		secretValues = append(secretValues, [2]string{"HERMES_AGENT_TELEGRAM_BOT_TOKEN", in.BotToken})
	}
	for _, value := range [][2]string{
		{"HERMES_AGENT_WEIXIN_ACCOUNT_ID", in.WeixinAccountID},
		{"HERMES_AGENT_WEIXIN_TOKEN", in.WeixinToken},
		{"HERMES_AGENT_WEIXIN_BASE_URL", in.WeixinBaseURL},
		{"HERMES_AGENT_WEIXIN_ALLOWED_USERS", in.WeixinAllowedUsers},
	} {
		if value[1] != "" {
			secretValues = append(secretValues, value)
		}
	}
	toTags := func(values [][2]string) []goarSchema.Tag {
		tags := make([]goarSchema.Tag, 0, len(values))
		for _, value := range values {
			if value[1] != "" {
				tags = append(tags, goarSchema.Tag{Name: containerEnvTagPrefix + value[0], Value: value[1]})
			}
		}
		return tags
	}
	plain := append([]goarSchema.Tag{{Name: "Action", Value: "Start-Agent"}}, toTags(plainValues)...)
	return plain, toTags(secretValues), nil
}

type PodSpawnInput struct {
	RuntimeType string
}

type PodStartInput struct {
	GatewayURL, GatewayAPIKey, BotToken, HermesGatewayToken         string
	WeixinAccountID, WeixinToken, WeixinBaseURL, WeixinAllowedUsers string
}

func fetchHymatrixNodeInfo(ctx context.Context, nodeURL string) (HymatrixNodeInfo, error) {
	var info HymatrixNodeInfo
	parsed, err := url.Parse(strings.TrimSpace(nodeURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return info, fmt.Errorf("invalid hymatrix node URL")
	}
	if parsed.User != nil {
		return info, fmt.Errorf("hymatrix node URL must not contain credentials")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return info, fmt.Errorf("resolve hymatrix node host: %w", err)
	}
	for _, address := range addresses {
		if isBlockedNodeAddress(address.IP) {
			return info, fmt.Errorf("hymatrix node URL resolves to a private or local address")
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/info"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return info, fmt.Errorf("create hymatrix node info request: %w", err)
	}
	res, err := nodeInfoHTTPClient.Do(req)
	if err != nil {
		return info, fmt.Errorf("fetch hymatrix node info: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return info, fmt.Errorf("fetch hymatrix node info: unexpected HTTP status %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil {
		return info, fmt.Errorf("read hymatrix node info: %w", err)
	}
	if len(body) > 1<<20 {
		return info, fmt.Errorf("hymatrix node info response exceeds 1 MiB")
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return info, fmt.Errorf("decode hymatrix node info: %w", err)
	}
	if strings.TrimSpace(info.Node.AccountID) == "" {
		return info, fmt.Errorf("hymatrix node info is missing Node.Acc-Id")
	}
	return info, nil
}

func isBlockedNodeAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
