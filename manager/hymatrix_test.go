package manager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	serverSchema "github.com/hymatrix/hymx/server/schema"
	goarSchema "github.com/permadao/goar/schema"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type nodeWithoutEncryptionKeySDK struct {
	encryptCalled bool
	spawnTags     []goarSchema.Tag
}

func (s *nodeWithoutEncryptionKeySDK) EncryptTags([]goarSchema.Tag) ([]goarSchema.Tag, error) {
	s.encryptCalled = true
	return nil, errors.New("err_invalid_public_key")
}

func (s *nodeWithoutEncryptionKeySDK) SpawnAndWait(_, _ string, tags []goarSchema.Tag) (*serverSchema.Response, error) {
	s.spawnTags = tags
	return &serverSchema.Response{Id: "pid-test"}, nil
}

func (s *nodeWithoutEncryptionKeySDK) SendMessageWithEncryptedParamsAndWait(_ string, _ string, _, _ []goarSchema.Tag) (*serverSchema.Response, error) {
	return nil, errors.New("err_invalid_public_key")
}

type recordingPodSDK struct {
	calls          []string
	spawnTags      []goarSchema.Tag
	spawnErr       error
	startTarget    string
	startPlain     []goarSchema.Tag
	startEncrypted []goarSchema.Tag
	startErr       error
}

func (s *recordingPodSDK) SpawnAndWait(_, _ string, tags []goarSchema.Tag) (*serverSchema.Response, error) {
	s.calls = append(s.calls, "spawn")
	s.spawnTags = tags
	if s.spawnErr != nil {
		return nil, s.spawnErr
	}
	return &serverSchema.Response{Id: "pid-new"}, nil
}

func (s *recordingPodSDK) SendMessageWithEncryptedParamsAndWait(target, _ string, plain, encrypted []goarSchema.Tag) (*serverSchema.Response, error) {
	s.calls = append(s.calls, "start-agent")
	s.startTarget = target
	s.startPlain = plain
	s.startEncrypted = encrypted
	if s.startErr != nil {
		return nil, s.startErr
	}
	return &serverSchema.Response{Id: "message-start"}, nil
}

func TestSpawnOnlyBuildsSpawnTransaction(t *testing.T) {
	fake := &recordingPodSDK{}
	client := &HymatrixClient{config: HymatrixConfig{
		Module: "module", Scheduler: "scheduler", LLMProvider: "custom",
		LLMModel: "model", LLMBaseURL: "https://llm.example/v1", LLMAPIKey: "llm-secret",
	}, sdk: fake}

	pid, err := client.Spawn(t.Context(), PodSpawnInput{RuntimeType: "hermes"})
	if err != nil {
		t.Fatal(err)
	}
	if pid != "pid-new" {
		t.Fatalf("pid = %q", pid)
	}
	if strings.Join(fake.calls, ",") != "spawn" {
		t.Fatalf("calls = %v", fake.calls)
	}
	spawnValues := tagsByName(fake.spawnTags)
	if spawnValues["Container-Env-RUNTIME_TYPE"] != "hermes" || spawnValues["Hub-Spawn-Timestamp"] == "" || len(spawnValues) != 2 {
		t.Fatalf("spawn tags = %v", spawnValues)
	}
}

func TestStartAgentBuildsIndependentEncryptedMessage(t *testing.T) {
	fake := &recordingPodSDK{}
	client := &HymatrixClient{config: HymatrixConfig{
		LLMProvider: "custom", LLMModel: "model", LLMBaseURL: "https://llm.example/v1", LLMAPIKey: "llm-secret",
	}, sdk: fake}

	err := client.StartAgent(t.Context(), "pid-existing", PodStartInput{
		GatewayURL: "https://hub.example", GatewayAPIKey: "gateway-secret",
		HermesGatewayToken: "hermes-secret", BotToken: "telegram-secret",
		WeixinAccountID: "bot@im.bot", WeixinToken: "weixin-secret",
		WeixinBaseURL: "https://ilinkai.weixin.qq.com", WeixinAllowedUsers: "user@im.wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fake.calls, ",") != "start-agent" {
		t.Fatalf("calls = %v", fake.calls)
	}
	if fake.startTarget != "pid-existing" {
		t.Fatalf("start target = %q", fake.startTarget)
	}
	assertTagsEqual(t, fake.startPlain, map[string]string{
		"Action": "Start-Agent",
		"Container-Env-HERMES_AGENT_LLM_PROVIDER": "custom",
		"Container-Env-HERMES_AGENT_LLM_MODEL":    "model",
		"Container-Env-HERMES_AGENT_LLM_BASE_URL": "https://llm.example/v1",
		"Container-Env-HUB_GATEWAY_URL":           "https://hub.example",
		"Container-Env-API_SERVER_ENABLED":        "true",
	})
	assertTagsEqual(t, fake.startEncrypted, map[string]string{
		"Container-Env-HERMES_AGENT_LLM_API_KEY":          "llm-secret",
		"Container-Env-HUB_GATEWAY_API_KEY":               "gateway-secret",
		"Container-Env-API_SERVER_KEY":                    "hermes-secret",
		"Container-Env-HERMES_GATEWAY_TOKEN":              "hermes-secret",
		"Container-Env-HERMES_AGENT_TELEGRAM_BOT_TOKEN":   "telegram-secret",
		"Container-Env-HERMES_AGENT_WEIXIN_ACCOUNT_ID":    "bot@im.bot",
		"Container-Env-HERMES_AGENT_WEIXIN_TOKEN":         "weixin-secret",
		"Container-Env-HERMES_AGENT_WEIXIN_BASE_URL":      "https://ilinkai.weixin.qq.com",
		"Container-Env-HERMES_AGENT_WEIXIN_ALLOWED_USERS": "user@im.wechat",
	})
}

func TestSpawnFailureDoesNotSendStartAgent(t *testing.T) {
	fake := &recordingPodSDK{spawnErr: errors.New("spawn rejected")}
	client := &HymatrixClient{config: HymatrixConfig{Module: "module", Scheduler: "scheduler"}, sdk: fake}

	_, err := client.Spawn(t.Context(), PodSpawnInput{
		RuntimeType: "hermes",
	})
	if err == nil || !strings.Contains(err.Error(), "spawn rejected") {
		t.Fatalf("error = %v", err)
	}
	if strings.Join(fake.calls, ",") != "spawn" {
		t.Fatalf("calls = %v", fake.calls)
	}
}

func TestStartAgentFailureReturnsMessageError(t *testing.T) {
	fake := &recordingPodSDK{startErr: errors.New("message rejected")}
	client := &HymatrixClient{config: HymatrixConfig{Module: "module", Scheduler: "scheduler"}, sdk: fake}

	err := client.StartAgent(t.Context(), "pid-new", PodStartInput{
		GatewayURL: "https://hub.example", GatewayAPIKey: "gateway-secret",
		HermesGatewayToken: "hermes-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "start Hermes agent: message rejected") {
		t.Fatalf("error = %v", err)
	}
	if strings.Join(fake.calls, ",") != "start-agent" {
		t.Fatalf("calls = %v", fake.calls)
	}
}

func assertTagsEqual(t *testing.T, tags []goarSchema.Tag, want map[string]string) {
	t.Helper()
	if len(tags) != len(want) {
		t.Fatalf("tag count = %d, want %d: %#v", len(tags), len(want), tags)
	}
	for _, tag := range tags {
		if value, ok := want[tag.Name]; !ok || value != tag.Value {
			t.Fatalf("unexpected tag %q=%q; want %v", tag.Name, tag.Value, want)
		}
	}
}

func tagsByName(tags []goarSchema.Tag) map[string]string {
	values := make(map[string]string, len(tags))
	for _, tag := range tags {
		values[tag.Name] = tag.Value
	}
	return values
}

func TestStartAgentRequiresNodeEncryptionPublicKey(t *testing.T) {
	fake := &nodeWithoutEncryptionKeySDK{}
	client := &HymatrixClient{config: HymatrixConfig{Module: "module", Scheduler: "scheduler", LLMAPIKey: "llm-secret"}, sdk: fake}
	err := client.StartAgent(t.Context(), "pid-test", PodStartInput{
		GatewayURL: "https://hub.example", GatewayAPIKey: "gateway-secret",
		HermesGatewayToken: "gateway-token", BotToken: "telegram-token",
		WeixinAccountID: "bot@im.bot", WeixinToken: "weixin-token",
		WeixinBaseURL: "https://ilinkai.weixin.qq.com", WeixinAllowedUsers: "user@im.wechat",
	})
	if err == nil || !strings.Contains(err.Error(), "err_invalid_public_key") {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchHymatrixNodeInfoUsesInfoEndpoint(t *testing.T) {
	previous := nodeInfoHTTPClient
	t.Cleanup(func() { nodeInfoHTTPClient = previous })
	nodeInfoHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://1.1.1.1/node/info" {
			t.Fatalf("request URL = %q", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(strings.NewReader(`{
				"Protocol":"hymx",
				"Node-Version":"v0.4.8",
				"Node":{"Acc-Id":"0x67cBa2FEDaaA07627169a60Bc690aD9571Ed2265","Name":"node"}
			}`)),
		}, nil
	})}

	info, err := fetchHymatrixNodeInfo(context.Background(), "https://1.1.1.1/node/?ignored=yes")
	if err != nil {
		t.Fatal(err)
	}
	if info.Node.AccountID != "0x67cBa2FEDaaA07627169a60Bc690aD9571Ed2265" {
		t.Fatalf("scheduler = %q", info.Node.AccountID)
	}
}

func TestFetchHymatrixNodeInfoRejectsPrivateAddress(t *testing.T) {
	_, err := fetchHymatrixNodeInfo(context.Background(), "http://127.0.0.1:8081")
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildStartAgentTagSetsIncludesCompleteEnvironment(t *testing.T) {
	config := HymatrixConfig{
		LLMProvider: "custom",
		LLMModel:    "deepseek-chat",
		LLMBaseURL:  "https://llm.example/v1",
		LLMAPIKey:   "llm-secret",
	}
	plain, secret, err := buildStartAgentTagSets(config, PodStartInput{
		GatewayURL:         "https://hub.example",
		GatewayAPIKey:      "gw_sk_test",
		BotToken:           "telegram-token",
		HermesGatewayToken: "hermes-health-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPlain := map[string]string{
		"Action": "Start-Agent",
		"Container-Env-HERMES_AGENT_LLM_PROVIDER": "custom",
		"Container-Env-HERMES_AGENT_LLM_MODEL":    "deepseek-chat",
		"Container-Env-HERMES_AGENT_LLM_BASE_URL": "https://llm.example/v1",
		"Container-Env-HUB_GATEWAY_URL":           "https://hub.example",
		"Container-Env-API_SERVER_ENABLED":        "true",
	}
	wantSecret := map[string]string{
		"Container-Env-HERMES_AGENT_LLM_API_KEY":        "llm-secret",
		"Container-Env-HUB_GATEWAY_API_KEY":             "gw_sk_test",
		"Container-Env-API_SERVER_KEY":                  "hermes-health-secret",
		"Container-Env-HERMES_GATEWAY_TOKEN":            "hermes-health-secret",
		"Container-Env-HERMES_AGENT_TELEGRAM_BOT_TOKEN": "telegram-token",
	}
	assertTagsEqual(t, plain, wantPlain)
	assertTagsEqual(t, secret, wantSecret)
}

func TestBuildStartAgentTagSetsDefaultsProviderAndOmitsEmptyValues(t *testing.T) {
	plain, secret, err := buildStartAgentTagSets(HymatrixConfig{}, PodStartInput{GatewayURL: "https://hub.example", GatewayAPIKey: "gw_sk_test", HermesGatewayToken: "hermes-health-secret"})
	if err != nil {
		t.Fatal(err)
	}
	values := tagsByName(plain)
	if values["Container-Env-HERMES_AGENT_LLM_PROVIDER"] != "custom" {
		t.Fatalf("provider = %q", values["Container-Env-HERMES_AGENT_LLM_PROVIDER"])
	}
	if _, exists := tagsByName(secret)["Container-Env-HERMES_AGENT_LLM_API_KEY"]; exists {
		t.Fatal("empty LLM API key should not be emitted")
	}
}

func TestBuildStartAgentTagSetsRequiresHermesGatewayToken(t *testing.T) {
	_, _, err := buildStartAgentTagSets(HymatrixConfig{}, PodStartInput{GatewayURL: "https://hub.example", GatewayAPIKey: "gw_sk_test"})
	if err == nil || !strings.Contains(err.Error(), "Hermes gateway token") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildStartAgentTagSetsPassesWeixinAsEncryptedParams(t *testing.T) {
	_, tags, err := buildStartAgentTagSets(HymatrixConfig{LLMAPIKey: "llm-secret"}, PodStartInput{GatewayURL: "https://hub.example", GatewayAPIKey: "gateway-secret", HermesGatewayToken: "gateway-token", WeixinAccountID: "bot@im.bot", WeixinToken: "weixin-token", WeixinBaseURL: "https://ilinkai.weixin.qq.com", WeixinAllowedUsers: "user@im.wechat"})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, tag := range tags {
		values[tag.Name] = tag.Value
		if strings.HasPrefix(tag.Name, "Encrypted-") {
			t.Fatalf("node without Encryption-Public-Key cannot receive %q", tag.Name)
		}
	}
	for name, want := range map[string]string{"Container-Env-HERMES_AGENT_LLM_API_KEY": "llm-secret", "Container-Env-HUB_GATEWAY_API_KEY": "gateway-secret", "Container-Env-HERMES_GATEWAY_TOKEN": "gateway-token", "Container-Env-HERMES_AGENT_WEIXIN_ACCOUNT_ID": "bot@im.bot", "Container-Env-HERMES_AGENT_WEIXIN_TOKEN": "weixin-token", "Container-Env-HERMES_AGENT_WEIXIN_BASE_URL": "https://ilinkai.weixin.qq.com", "Container-Env-HERMES_AGENT_WEIXIN_ALLOWED_USERS": "user@im.wechat"} {
		if values[name] != want {
			t.Errorf("tag %s = %q, want %q", name, values[name], want)
		}
	}
}

func TestBuildStartAgentTagSetsRejectsPartialWeixinCredentials(t *testing.T) {
	_, _, err := buildStartAgentTagSets(HymatrixConfig{}, PodStartInput{
		GatewayURL: "https://hub.example", GatewayAPIKey: "gateway-secret",
		HermesGatewayToken: "gateway-token", WeixinToken: "partial-token",
	})
	if err == nil || !strings.Contains(err.Error(), "complete set") {
		t.Fatalf("expected incomplete Weixin credentials error, got %v", err)
	}
}
