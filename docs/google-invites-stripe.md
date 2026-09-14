# Manager 用户登录、邀请码与 Stripe

实现范围来自本次用户要求：

1. 普通用户 Google 登录和 Web 操作入口，与管理员共用 Google 验证/会话逻辑；后台每次校验管理员白名单，普通登录不要求管理员身份。
2. 邀请码迁入 PostgreSQL，后台支持创建、查看和撤销，用户可以兑换开通助手。
3. Manager 兼容本地 website（vertr.ai）现有认证、邀请码、订阅购买、账单和门户 API。

## 入口

- `/app`：普通用户 Google 登录、兑换、订阅、我的助手与账单。
- `/admin/invite-codes`：管理员邀请码、账单和任务管理。
- 原 `/admin/login` 保留；身份验证与普通用户登录共用，后台仍逐次检查 `auth.google.allowedEmails`。
- 普通 API 使用 Bearer JWT。登录响应 `{accessToken, tokenType, expiresIn, user}`；user 包含 userId/email/name/picture/roles。
- User ID 使用 `google_<Google subject>`，与 agent-hub 一致；微信用户不按邮箱自动合并。

## vertr.ai API 兼容

| 接口 | 用途 |
|---|---|
| GET /v1/auth/info | Google clientId |
| POST /v1/auth/google | `{id_token}` 换取用户令牌 |
| GET /v1/me | 当前用户 |
| GET /v1/agents | 当前用户实例 |
| POST /v1/agents/telegram-customer | `{inviteCode}` 开通 telegram_customer_agent |
| POST /v1/agents/x | `{inviteCode}` 开通 x_agent |
| POST /v1/agents/google | `{inviteCode}` 开通 google_agent（须配置产品） |
| GET /v1/invite-codes/me | 已兑换记录 |
| POST /v1/billing/checkout-sessions | `{product,quantity:1}` 返回 sessionId/url |
| GET /v1/billing/checkout-sessions/:sessionId | 本用户 checkout 状态 |
| GET /v1/billing | 本用户账单 |
| POST /v1/billing/portal-sessions | 返回 Stripe Portal URL |
| POST /v1/stripe/webhook | Stripe 签名回调 |

产品标识由 `commerce.products` 映射到 Hub catalogId 和 Stripe Price ID。Module 在创建订单/兑换时保存快照，不接收浏览器提供的价格、模块或跳转 URL。Web 助手使用 Telegram，采用配置模块的 Hub Hermes `Start-Agent` 协议。agent-hub 原 TG 的 `Action=start`/X 私有协议并非该协议，不能直接填旧 Module ID。

## Stripe 运行约定

订阅为单席位，支持 Checkout、Billing Portal、订阅更新、支付成功/失败、取消和 checkout 过期。回调的事件 ID、账单变更、助手任务在同一数据库事务提交。收到订阅相关事件时取 Stripe 当前订阅状态，校验订单元数据、价格与数量；不按回调抵达次序覆盖权益。

首次支付确认后生成持久化开通任务。任务复用 Hub 的 Access Key、Telegram 和 LLM 资源，创建并启动 Pod。取消/欠费（可配置）停止 VM，补缴成功恢复 VM；节点需要支持 `/admin/vms/stop` 和 `/admin/vms/resume`。实例、资源绑定在暂停时保留。

部署外部操作结果不明或进程中断时标记 `needs_review`，不盲目重复 Spawn。管理员需先核实远端状态；安全失败的任务可以在后台重试。`running` 表示启动请求得到响应，不表示已经做过业务消息验收。

依据：[Stripe Webhooks](https://docs.stripe.com/webhooks)、[Subscription Webhooks](https://docs.stripe.com/billing/subscriptions/webhooks)。Webhook API version 必须与 stripe-go/v86.1.0 匹配，使用 SDK 常量 `stripe.APIVersion`；服务拒绝不匹配版本。

## 配置与切换

1. 运行数据库迁移（Manager 启动 AutoMigrate），配置 `auth.google`/`auth.jwt`。Google Client ID 必须匹配 vertr.ai 页面；Google Console 授权相应站点 origin。
2. 后台配置并上架助手目录；在 `commerce.products` 绑定 catalogId/Price ID。配置节点、签名私钥、Gateway URL、Hermes Gateway Token，并先用测试账号验证模块。
3. 配置 Stripe 测试密钥、Webhook secret、success/cancel/portal URL；Checkout 成功页 URL 需包含 `{CHECKOUT_SESSION_ID}`。本地 website 有 checkout-success.html，但正式站点路径需部署时核对。
4. Stripe 配置事件：checkout.session.completed/expired、invoice.paid/payment_failed、customer.subscription.updated/deleted。先用测试订阅验证。
5. 将 `window.VertrPlatformConfig.apiBase` 或 `window.VERTR_PLATFORM_API_BASE` 指向 Manager（或把原 platform-api 域名反代到 Manager）。客户端继续发送 Bearer，不需要跨域 Cookie。
6. 旧 JWT 若签名/issuer/audience 不一致，用户重新登录。旧用户/邀请码/账单/实例 JSON 数据不能仅通过切换域名自动进入 PostgreSQL；现有付费订阅上线前必须做数据映射导入，尤其核实旧运行模块协议。本次功能实现不操作生产数据、不切换域名。

部署机需保管真实 Stripe/Google/节点配置；仓库示例为空值，不包含生产凭据。
