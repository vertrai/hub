package manager

import (
	"github.com/vertrai/hub/manager/schema"
	"reflect"
	"testing"
)

func TestCatalogScopesPreserveOtherChannels(t *testing.T) {
	original := schema.AgentCatalogEntry{ID: "assistant", Name: "原名称", LogoURL: "https://example.com/icon.png", Intro: "原介绍", Capabilities: []string{"分析"}, Module: "module", Published: true, ProductID: "assistant", Web: &schema.WebCatalogConfig{Published: true, InviteEnabled: true}}
	before := publicCatalogEntry(original)
	core := original
	core.Name = "基础名称"
	core.Module = "new-module"
	core.Published = false
	core.Web = nil
	updated, err := mergeCatalogScope(original, core, "core")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, publicCatalogEntry(updated)) {
		t.Fatal("core edit changed existing WeChat rendering")
	}
	if updated.Web != original.Web || !updated.Published || updated.Module != "new-module" {
		t.Fatal("core ownership failed")
	}
	wx := wechatCatalogView(updated)
	wx.Name = "微信名称"
	wx.Module = "bad-module"
	wx.ProductID = "bad-product"
	wx.Web = nil
	wx.Published = false
	updated, err = mergeCatalogScope(updated, wx, "wechat")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "基础名称" || updated.Module != "new-module" || updated.ProductID != "assistant" || updated.Web != original.Web || publicCatalogEntry(updated)["name"] != "微信名称" {
		t.Fatal("WeChat overwrote shared or web fields")
	}
	web := schema.AgentCatalogEntry{ProductID: "assistant", Web: &schema.WebCatalogConfig{Published: false}, Name: "bad name", Published: true}
	result, err := mergeCatalogScope(updated, web, "web")
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != updated.Name || result.Published || !reflect.DeepEqual(result.Wechat, updated.Wechat) || result.Web.Published {
		t.Fatal("web edit overwrote WeChat or core fields")
	}
	if _, err = mergeCatalogScope(result, schema.AgentCatalogEntry{ProductID: "changed", Web: web.Web}, "web"); err == nil {
		t.Fatal("allowed product identity change")
	}
	if _, err = mergeCatalogScope(result, schema.AgentCatalogEntry{}, "web"); err == nil {
		t.Fatal("allowed missing web configuration")
	}
	if _, err = mergeCatalogScope(result, schema.AgentCatalogEntry{}, "unknown"); err == nil {
		t.Fatal("allowed unknown scope")
	}
}

func TestRegistrationWithoutPresentation(t *testing.T) {
	a := schema.AgentCatalogEntry{ID: "new-agent", Name: "新助手", LogoURL: "https://example.com/icon.png", Intro: "简单介绍", Module: "module"}
	if err := validateCatalogFields(a, false); err != nil {
		t.Fatal(err)
	}
	input := a
	input.Name = "新名称"
	input.Summary = "不应保存"
	input.Capabilities = []string{"不应保存"}
	updated, err := mergeCatalogScope(a, input, "core")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Summary != "" || len(updated.Capabilities) != 0 {
		t.Fatal("registration accepted presentation fields")
	}
	wx := wechatCatalogView(updated)
	wx.Capabilities = []string{"微信能力"}
	wx.Summary = "微信详情"
	updated, err = mergeCatalogScope(updated, wx, "wechat")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Wechat.Summary != "微信详情" || updated.Summary != "" {
		t.Fatal("channel content not isolated")
	}
	legacy := a
	legacy.Summary = "旧微信详情"
	legacy.Capabilities = []string{"旧微信能力"}
	view := (&Manager{}).webCatalogEntry(legacy, "zh")
	if view["summary"] == legacy.Summary {
		t.Fatal("web inherited WeChat detail")
	}
}

func TestWebProductIdentityIsAutomaticAndStable(t *testing.T) {
	a := schema.AgentCatalogEntry{ID: "new-web-agent", Name: "助手", Intro: "介绍", LogoURL: "https://example.com/icon.png", Module: "module"}
	input := schema.AgentCatalogEntry{Web: &schema.WebCatalogConfig{Published: true, InviteEnabled: true}}
	saved, err := mergeCatalogScope(a, input, "web")
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProductID == "" || !websiteProductID.MatchString(saved.ProductID) {
		t.Fatal("missing or invalid generated identity")
	}
	again, err := mergeCatalogScope(saved, input, "web")
	if err != nil {
		t.Fatal(err)
	}
	if again.ProductID != saved.ProductID {
		t.Fatal("identity changed on repeat save")
	}
	a.ProductID = "legacy_product"
	legacy, err := mergeCatalogScope(a, input, "web")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ProductID != "legacy_product" {
		t.Fatal("legacy identity replaced")
	}
	a.ProductID = ""
	a.ID = "other-agent"
	other, err := mergeCatalogScope(a, input, "web")
	if err != nil {
		t.Fatal(err)
	}
	if other.ProductID == saved.ProductID {
		t.Fatal("different agents share identity")
	}
}
