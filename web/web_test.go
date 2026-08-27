package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func allowAll(c *gin.Context) { c.Next() }

func TestRegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	for _, path := range []string{"/admin", "/admin/users", "/admin/google", "/admin/browser", "/admin/telegram", "/admin/weixin", "/admin/hymatrix", "/admin/test"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, recorder.Code)
		}
		if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("GET %s content type = %q", path, got)
		}
	}
}

func TestAdminLoginPageHasDesignedAccessGateAndNonAdminErrorState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/login", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		`class="access-gate"`, `class="identity-panel"`, "运行时控制中心",
		`id="googleButton"`, `id="error"`, `role="alert"`, `aria-live="assertive"`,
		"admin_not_allowed", "该 Google 账号未被授权为管理员",
		"Math.min(320", "网络连接异常，请检查网络后重新选择 Google 账号",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("login page is missing %q", expected)
		}
	}
}

func TestAdminPagesShareRuntimeHubBrandAndNavigation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	for _, path := range []string{"/admin", "/admin/users", "/admin/google", "/admin/browser", "/admin/telegram", "/admin/weixin", "/admin/hymatrix", "/admin/test"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		body := recorder.Body.String()
		for _, expected := range []string{"AGENT RUNTIME CONTROL", "Agent Runtime Hub", `href="/admin/browser"`, `href="/admin/weixin"`, `href="/admin/test"`} {
			if !strings.Contains(body, expected) {
				t.Errorf("GET %s is missing shared navigation content %q", path, expected)
			}
		}
	}
}

func TestWeixinPageIsStandaloneLocalHermesTest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/weixin", nil))
	for _, expected := range []string{"Weixin Bot 授权", "一次性分配给 Hermes Pod", `id="userId"`, "WEIXIN_ACCOUNT_ID", "/v1/admin/weixin/onboarding", "复制 .env"} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("Weixin page is missing %q", expected)
		}
	}
}

func TestWeixinPageRevealsAndFocusesEnvironmentAfterScan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/weixin", nil))
	body := recorder.Body.String()
	for _, expected := range []string{`id="resultCard" class="card weixin-result"`, `$("resultCard").hidden=false`, `$("resultCard").scrollIntoView({behavior:"smooth",block:"start"})`} {
		if !strings.Contains(body, expected) {
			t.Errorf("Weixin success flow is missing %q", expected)
		}
	}
	if strings.Contains(body, `id="resultCard" class="card result"`) {
		t.Error("Weixin result card uses the globally hidden .result class")
	}
}

func TestAdminPagesAutoLoadWithGoogleSession(t *testing.T) {
	for path, expected := range map[string]string{
		"/admin":          "load();",
		"/admin/users":    "load();",
		"/admin/google":   "load();",
		"/admin/browser":  "loadBrowserResources();",
		"/admin/telegram": "loadTelegramResources()",
		"/admin/hymatrix": "load();",
	} {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		RegisterRoutes(router, allowAll)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("GET %s does not auto-load data with an authenticated session", path)
		}
	}
}

func TestBrowserResourcePageExplainsOnDemandInventory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/browser", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"Browser 资源池",
		"按需创建",
		"已创建 Profile",
		"活跃会话",
		"/v1/admin/browser/sessions",
		"打开 Live View",
		"closeBrowser",
		"CDP URL 属于自动化连接凭证，继续保持隐藏",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("browser resource page is missing %q", expected)
		}
	}
}

func TestUsersPageExplainsExistingAndNewUserIssuance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/users", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"用户与访问密钥",
		"填写已有用户 ID 可追加一把 Key",
		"填写新的用户 ID 会同时创建用户",
		"完整 API Key",
		"资源权限可在签发后调整",
		`id="allowGoogle"`,
		`id="allowBrowser"`,
		`id="allowTelegram"`,
		`class="grid key-form"`,
		`class="field-help"`,
		`data-edit-scopes`,
		`class="key-result issued-key"`,
		"仅显示一次",
		`id="newKey"`,
		`id="gatewayUrl"`,
		`id="copyGatewayUrl"`,
		"window.location.origin",
		"复制 Gateway URL",
		"离开或刷新页面后，将无法再次查看完整 Key",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("users page is missing issuance guidance %q", expected)
		}
	}
}

func TestResourceAPITestUsesAdminShell(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/test", nil))
	body := recorder.Body.String()
	for _, expected := range []string{`class="shell"`, `class="side"`, `class="active" href="/admin/test"`, `/admin/assets/common.css`} {
		if !strings.Contains(body, expected) {
			t.Errorf("Resources API test page is missing %q", expected)
		}
	}
	if strings.Contains(body, "返回 Manager 控制台") {
		t.Error("Resources API test page still renders the standalone-page return button")
	}
}

func TestHymatrixPageIncludesLiveTransactionPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/hymatrix", nil))
	body := recorder.Body.String()
	for _, expected := range []string{"待发送 Spawn 交易", "Envelope", "Protocol tags", "Container environment", "Hub-Spawn-Timestamp", "显示敏感值", "复制预览", "telegramBotLink", "telegramAcquireHint", "当前 API Key 未开通 Telegram 资源", "/v1/admin/telegram/bot-link", "01 · Pod 基础配置", "02 · Node 配置", "http://52.220.233.136:8081", "1LYYgkP4nRmnvGi2EN9ERuyYyFDzUMkSFBYW4_2DuyI", "randomPodName", "advanced-config"} {
		if !strings.Contains(body, expected) {
			t.Errorf("Hymatrix page is missing %q", expected)
		}
	}
}

func TestHymatrixPageBuildsSpawnAndStartAgentSeparately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/hymatrix", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"此处只构建 Spawn 交易",
		`id="startAgentDialog"`,
		`data-pod-start=`,
		`/v1/admin/hymatrix/pods/${encodeURIComponent($("startPodId").value)}/start`,
		`["Container-Env-RUNTIME_TYPE", $("runtimeType").value.trim(), false]`,
		`["Action", "Start-Agent", false]`,
		`"Encrypted-Container-Env-HERMES_AGENT_LLM_API_KEY"`,
		`"Encrypted-Container-Env-HUB_GATEWAY_API_KEY"`,
		`"Encrypted-Container-Env-HERMES_AGENT_TELEGRAM_BOT_TOKEN"`,
		`"Encrypted-Container-Env-HERMES_AGENT_WEIXIN_TOKEN"`,
		`class="pod-dialog start-agent-dialog"`,
		`class="pod-actions"`,
		`id="spawnKeyCheck"`,
		`spawnUserKeyState === "available"`,
		`没有可用 API Key`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("Hymatrix split transaction UI is missing %q", expected)
		}
	}
}

func TestHymatrixPageSupportsIndependentTelegramAndWeixinChannels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/hymatrix", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		`id="enableTelegram"`, `id="enableWeixin"`, `id="telegramBotField" class="wide" hidden`, `id="weixinBotField" class="wide" hidden`, `id="weixinBotId"`,
		`function syncChannelFields()`, `$("telegramBotField").hidden = !$("enableTelegram").checked`,
		`#telegramBotField[hidden]`, `#weixinBotField[hidden]`,
		`/v1/admin/weixin/bots?userId=`, `enableTelegram: $("enableTelegram").checked`,
		`weixinBotId: $("enableWeixin").checked`,
		"Container-Env-HERMES_AGENT_WEIXIN_TOKEN",
		`id="authorizeWeixin"`, `id="weixinAuthDialog"`, `id="weixinAuthQR"`,
		`class="weixin-auth-body"`, `class="weixin-qr-stage"`,
		`.pod-dialog.weixin-auth-dialog`,
		`/v1/admin/weixin/onboarding`, `function pollWeixinAuthorization`,
		`$("weixinBotId").value = state.botId`,
		`error.status === 404 || error.status === 410`,
		`$("weixinBotId").value !== state.botId`,
		`id="weixinAuthExpiry"`,
		`function startWeixinExpiryCountdown`,
		`预计剩余`,
		`$("weixinAuthDialog").close()`,
		`授权成功，正在返回启动配置`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("Hymatrix channel selection is missing %q", expected)
		}
	}
}

func TestHymatrixSpawnRequiresCompleteForm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/hymatrix", nil))
	body := recorder.Body.String()
	spawnFormStart := strings.Index(body, `<form id="form">`)
	if spawnFormStart < 0 {
		t.Fatal("Spawn form not found")
	}
	spawnFormEnd := strings.Index(body[spawnFormStart:], `</form>`)
	if spawnFormEnd < 0 {
		t.Fatal("Spawn form closing tag not found")
	}
	spawnForm := body[spawnFormStart : spawnFormStart+spawnFormEnd]
	for _, startOnlyField := range []string{`id="accessKeyId"`, `id="gatewayUrl"`, `id="llmApiKey"`, `id="hermesGatewayToken"`} {
		if strings.Contains(spawnForm, startOnlyField) {
			t.Errorf("Spawn form contains Start-Agent-only field %q", startOnlyField)
		}
	}
	for _, expected := range []string{
		`id="spawn" type="submit" disabled`,
		"function isSpawnFormComplete()",
		`"scheduler"`,
		`$("form").checkValidity()`,
		`$("spawn").disabled = spawnSubmitting || !ready`,
		`spawnRequirements`,
		`data-missing-field`,
		`createAccessKeyLink`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("Hymatrix complete-form validation is missing %q", expected)
		}
	}
}

func TestHymatrixPodHistorySupportsFilteringAndDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/hymatrix", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		`id="podStatusFilter"`,
		`id="podPagination"`,
		`id="podDetail"`,
		"function filteredPods()",
		"function openPodDetail(id)",
		"失败原因",
		"data-pod-detail",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("Hymatrix Pod history is missing %q", expected)
		}
	}
}

func TestAdminEnhancementsAssetIncludesMobileNavigationStyles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, allowAll)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/assets/admin-enhancements.css", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("enhancement stylesheet status = %d", recorder.Code)
	}
	for _, expected := range []string{".nav-toggle", ".side.menu-open .nav", ".pill.failed"} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("enhancement stylesheet is missing %q", expected)
		}
	}
}
