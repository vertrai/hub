# agent-hub 已实现功能盘点

研究日期：2026-09-14。本地源码：`/Users/sandyzhou/codex-project/agent-hub`；commit `71dcc032f215b4f474bb9fced5d652fe3ead3721`。工作区不干净：`go.mod` 已修改，`tools/` 未跟踪。本文只做静态源码核对，没有启动外部服务或验证真实支付、Google、Telegram、Hymatrix 部署。不将文档宣称等同于生产可用。

## 业务控制面：platformServer

| 功能 | 实现证据及边界 |
| --- | --- |
| 普通用户 Google 登录与个人资料 | 校验 Google ID token、邮箱验证及允许域，建立用户，签发有 audience/role 的 JWT；[auth/api.go:31](/Users/sandyzhou/codex-project/agent-hub/platformServer/auth/api.go:31)。这是面向产品用户的登录，不只是管理员登录。 |
| 邀请码发放、兑换、用户查询 | 批量随机码、用户兑换约束、绑定产品与 Agent；[invite_code_store.go:49](/Users/sandyzhou/codex-project/agent-hub/platformServer/invite_code_store.go:49)、[invite_code_store.go:83](/Users/sandyzhou/codex-project/agent-hub/platformServer/invite_code_store.go:83)。用户及管理 API 已注册：[api.go:25](/Users/sandyzhou/codex-project/agent-hub/platformServer/api.go:25)。 |
| Stripe 订阅商业化 | Checkout 创建、查询、Billing Portal、账单列表；[stripe_billing.go:59](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:59)、[stripe_billing.go:178](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:178)。Webhook 验签和事件去重，处理支付成功/失败、订阅更新/删除、Checkout 过期；[stripe_billing.go:200](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:200)。 |
| 付款与实例联动 | invoice.paid 时配置实例并关联账单；付款失败可配置停止实例，取消订阅停止实例；[stripe_billing.go:297](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:297)。注意续费成功对已存在且 stopped 实例没有显式恢复调用，不能宣称完整自动复机。 |
| 三类产品资源组合 | Telegram 客服=Bot；X=Bot+Browser；Google=Bot+Browser+Google 账号资源。[provisioning.go:18](/Users/sandyzhou/codex-project/agent-hub/platformServer/provisioning.go:18)。产品分支硬编码，并非可配置产品目录。 |
| 实例停止、重启及后台启动 | TG/X 支持 Hymatrix Spawn/Start/Stop；[agent_manager.go:11](/Users/sandyzhou/codex-project/agent-hub/platformServer/agent_manager.go:11)、[tg_hymx_manager.go:61](/Users/sandyzhou/codex-project/agent-hub/platformServer/tg_hymx_manager.go:61)。每 10 秒处理 starting；[jobs.go:25](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:25)。 |
| 停服 Bot 提示 | stopped 实例的 Telegram Bot 由平台接管消息轮询，向用户提示订阅过期或联系客服；[jobs.go:95](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:95)。这是独立业务逻辑，合并时需避免与运行时同时轮询同一 Bot。 |

限制：Google 产品仅分配资源，`deployGoogleAgent` 写 starting，后台直接推进 running，注释明确交由外部 googleAgent runtime 启动；[provisioning.go:167](/Users/sandyzhou/codex-project/agent-hub/platformServer/provisioning.go:167)、[jobs.go:86](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:86)。TG/X 在 Hymatrix 未启用时也直接写 running；[jobs.go:50](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:50)。因此数据库 running 不等于运行实例经健康验证。

## 资源服务：accessServer

| 功能 | 实现证据及边界 |
| --- | --- |
| 多调用平台与 API Key | 平台创建、列举、轮换 Key；平台 Key 认证后申请及查询资源。[api.go:24](/Users/sandyzhou/codex-project/agent-hub/accessServer/api.go:24)、[resource_api.go:27](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_api.go:27)。这里的平台是调用资源服务的业务平台，不是终端用户社交账号合并。 |
| 资源归属与幂等 | Resource 有 platformId、externalId、idempotencyKey、purpose、labels；[resource_store.go:27](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_store.go:27)。按平台及幂等键/外部 ID 查已有资源；[resource_store.go:199](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_store.go:199)。 |
| 按资源颁发凭据 | Browser Key 与 Google User Key 独立认证；平台管理凭据与运行时使用凭据分离；[resource_api.go:61](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_api.go:61)。资源记录仍保存明文调试字段，迁移不能直接照搬密钥存储；[resource_store.go:36](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_store.go:36)。 |
| Telegram 账号及 Bot 池 | 手机账号认证、验证码/2FA、Bot Token 池创建/导入及 BotFather 自动建 Bot；[telegram/service.go:61](/Users/sandyzhou/codex-project/agent-hub/accessServer/telegram/service.go:61)、[telegram/newbot.go:97](/Users/sandyzhou/codex-project/agent-hub/accessServer/telegram/newbot.go:97)。 |
| 持久远程 Browser | 获取/创建/重置会话、profile 复用、代理国家、过期回收和 profile 丢失恢复；[browser/browser_service.go:49](/Users/sandyzhou/codex-project/agent-hub/accessServer/browser/browser_service.go:49)、[browser/browser_service.go:388](/Users/sandyzhou/codex-project/agent-hub/accessServer/browser/browser_service.go:388)。底层依赖 Browser Use 云服务。 |
| Google Workspace 账号池 | 通过 Admin API 创建账号、批量预建池、状态管理与分配；[googleuser/service.go:87](/Users/sandyzhou/codex-project/agent-hub/accessServer/googleuser/service.go:87)、[googleuser/service.go:219](/Users/sandyzhou/codex-project/agent-hub/accessServer/googleuser/service.go:219)。 |
| Google 交互式 OAuth 授权 | start 生成 consent URL/state/offline 请求，callback 换取 token，存储并让资源调用者取 token JSON；[googleuser/service.go:294](/Users/sandyzhou/codex-project/agent-hub/accessServer/googleuser/service.go:294)。与 DWD 代签 token 是不同授权流。Token 接口返回所存 JSON，不在接口内自动刷新；[googleuser/service.go:362](/Users/sandyzhou/codex-project/agent-hub/accessServer/googleuser/service.go:362)。 |

未接通：微信扫码、凭据服务有实现 [weixin/weixin_service.go:42](/Users/sandyzhou/codex-project/agent-hub/accessServer/weixin/weixin_service.go:42)，但当前 [accessServer/api.go:17](/Users/sandyzhou/codex-project/agent-hub/accessServer/api.go:17) 未调用其 RegisterRoutes，不能把这些路由当成现行可访问 API。

## 独立运行时与应用，不属于平台控制面缺口

- **通用 agentServer**：JWT WebSocket 聊天、事件流、Telegram/微信渠道配置；[api.go:37](/Users/sandyzhou/codex-project/agent-hub/agentServer/api.go:37)、[channel_api.go:18](/Users/sandyzhou/codex-project/agent-hub/agentServer/channel_api.go:18)。选择 Claude/Hermes CLI、按用户恢复会话、判断是否需要浏览器、接入 browser-harness；[agent.go:223](/Users/sandyzhou/codex-project/agent-hub/agentServer/agent.go:223)、[agent.go:422](/Users/sandyzhou/codex-project/agent-hub/agentServer/agent.go:422)。渠道收发有具体实现 [telegram_channel.go:175](/Users/sandyzhou/codex-project/agent-hub/agentServer/telegram_channel.go:175)、[weixin_channel.go:218](/Users/sandyzhou/codex-project/agent-hub/agentServer/weixin_channel.go:218)。这些能力适合作为一个可部署 runtime 接入，不应要求控制面重写 Hermes 能力。
- **googleAgent / hermesAgent**：配置 Hermes LLM、Telegram Gateway、Google OAuth 文件、浏览器及附带 skills；[googleAgent/google_agent.go:103](/Users/sandyzhou/codex-project/agent-hub/googleAgent/google_agent.go:103)、[googleAgent/google_agent.go:256](/Users/sandyzhou/codex-project/agent-hub/googleAgent/google_agent.go:256)。Google/X 浏览器操作属于随 runtime 交付的脚本与技能，不是平台新增通用 API。
- **tgcustomer 客服应用**：多个 Bot 的创建、持久化恢复和移除 [bot_manager.go:53](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_manager.go:53)；管理员知识库维护和更新验证 [bot_session.go:960](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_session.go:960)；群消息批处理与回复决策 [bot_session.go:1232](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_session.go:1232)；自然语言配置升级规则 [bot_session.go:828](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_session.go:828)；群转私聊收集资料、升级工单、人工追问/解决 [bot_session.go:388](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_session.go:388)、[bot_session.go:1570](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_session.go:1570)；历史群回复定位与修订 [bot_session.go:902](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/bot_session.go:902)。这是有自定义状态机的业务应用，区别于 Hermes 自带聊天功能。

## 页面与数据架构

- accessServer 自带单页运维台：概览、平台 Key、资源申请、Telegram、Browser、Google/OAuth；[web/index.html:548](/Users/sandyzhou/codex-project/agent-hub/accessServer/web/index.html:548)。
- platformServer 自带“用户接口测试 / Admin 管理”单页：Google 登录、邀请码购买、用户实例及管理操作；[web/index.html:585](/Users/sandyzhou/codex-project/agent-hub/platformServer/web/index.html:585)。该文件没有 billing/checkout/portal 接入，Stripe 后端存在不代表现成支付前端。
- agentServer 自带测试聊天页，tgcustomer 自带管理页；对应嵌入入口 [agentServer/api.go:65](/Users/sandyzhou/codex-project/agent-hub/agentServer/api.go:65)、[tgcustomer/api.go:28](/Users/sandyzhou/codex-project/agent-hub/tgcustomer/api.go:28)。
- Go 单模块、多可执行服务，Gin + 嵌入 HTML；依赖见 [go.mod:1](/Users/sandyzhou/codex-project/agent-hub/go.mod:1)。主要状态为 JSON 文件与进程锁；资源、平台、Browser、Google 分文件初始化 [access_server.go:64](/Users/sandyzhou/codex-project/agent-hub/accessServer/access_server.go:64)，实例及账单分别存储 [agent_store.go:27](/Users/sandyzhou/codex-project/agent-hub/platformServer/agent_store.go:27)、[billing_store.go:23](/Users/sandyzhou/codex-project/agent-hub/platformServer/billing_store.go:23)。

合并建议：提取支付/订阅、邀请码、资源调用平台归属和交互式 OAuth 作为可迁移领域模块；tgcustomer 与通用运行时作为部署产品接入现有目录。统一 User/Agent/Resource/Billing ID 和持久化后再迁移状态机，不宜合并两个 JSON 控制面并行持有同一实例。保留 Google 产品部署、付款恢复运行、微信扫码路由及支付前端这些“仍需补齐”的明确边界。
