# 中文说明

项目默认 README 已使用中文，请阅读 [README.md](./README.md)。

## LLM 中转站

Manager 提供 `/admin/llm` 管理页面，支持多个 OpenAI Codex 账号通过 OAuth 授权接入、自动刷新，以及 DeepSeek、阿里云 Token Plan / Coding Plan 等 OpenAI 兼容 Provider，支持通过 Hub API Key 领取 LLM 密钥、按 Key 管理模型权限及 hub-chat 默认路由。客户端统一接入 `/llm/v1`，详见 [LLM 中转站配置与接口](docs/llm-gateway.md)。
