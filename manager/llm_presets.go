package manager

import (
	"errors"
	"github.com/gin-gonic/gin"
	"strings"
)

type llmPreset struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	BaseURL         string   `json:"baseUrl"`
	Help            string   `json:"help"`
	DocsURL         string   `json:"docsUrl"`
	Models          []string `json:"models,omitempty"`
	ModelsCheckedAt string   `json:"modelsCheckedAt,omitempty"`
}

// API roots verified against provider documentation. Model permissions vary
// by account and are deliberately configured by the administrator.
// Text-generation models from the official personal overview, checked
// 2026-09-08. Image/audio/video generation requires other gateway endpoints.
var tokenPlanPersonalModels = []string{
	"qwen3.8-max", "qwen3.8-flash", "qwen3.7-plus", "qwen3.7-max", "qwen3.6-flash",
	"deepseek-v4-pro-0813", "deepseek-v4-pro", "deepseek-v4-flash-0731", "glm-5.2",
}
var llmPresets = []llmPreset{
	{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Help: "填写 OpenAI API Key。", DocsURL: "https://platform.openai.com/docs/api-reference"},
	{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Help: "填写 DeepSeek API Key。已预填官方模型，可自行增删；deepseek-v4-flash-vision-exp 为支持图片输入的实验模型。", DocsURL: "https://api-docs.deepseek.com/", Models: []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp"}, ModelsCheckedAt: "2026-09-08"},
	{ID: "aliyun-tokenplan", Name: "阿里云 Token Plan（个人版）", BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", Help: "使用个人版专属 API Key。已预填官方对话模型，可自行增删；图片、音频和视频生成接口暂未接入。", DocsURL: "https://help.aliyun.com/zh/model-studio/token-plan-personal-overview", Models: tokenPlanPersonalModels, ModelsCheckedAt: "2026-09-08"},
	{ID: "aliyun-codingplan", Name: "阿里云 Coding Plan", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", Help: "使用 Coding Plan 专属 API Key，不能与百炼通用 Key 混用。", DocsURL: "https://help.aliyun.com/zh/model-studio/other-tools-token-plan"},
	{ID: "aliyun-dashscope", Name: "阿里云百炼（按量）", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Help: "此预设为北京地域；其他地域请修改 Base URL。", DocsURL: "https://help.aliyun.com/zh/model-studio/base-url"},
	{ID: "custom", Name: "其他 OpenAI 兼容服务", Help: "填写服务商提供的 OpenAI 兼容 API 根地址。"},
}

var codexModelPreset = llmPreset{ID: "openai-codex", Name: "OpenAI Codex", DocsURL: "https://learn.chatgpt.com/docs/models", Models: []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.3-codex-spark", "gpt-5.5"}, ModelsCheckedAt: "2026-09-08"}

func (m *Manager) listLLMPresets(c *gin.Context) {
	c.JSON(200, gin.H{"items": llmPresets, "codex": codexModelPreset})
}
func findLLMPreset(id string) (llmPreset, bool) {
	for _, p := range llmPresets {
		if p.ID == id {
			return p, true
		}
	}
	return llmPreset{}, false
}
func normalizeLLMModels(models []string) ([]string, error) {
	if len(models) == 0 || len(models) > 200 {
		return nil, errors.New("请填写 1 至 200 个模型名")
	}
	result := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 200 || seen[model] {
			return nil, errors.New("模型名不能为空、重复或超过 200 字符")
		}
		seen[model] = true
		result = append(result, model)
	}
	return result, nil
}
