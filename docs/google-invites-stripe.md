# Manager 用户登录、邀请码与 Stripe

实现范围来自本次用户要求：

1. 普通用户 Google 登录和 Web 操作入口，与管理员共用 Google 验证/会话逻辑；后台每次校验管理员白名单，普通登录不要求管理员身份。
2. 六位邀请码（兼容 vertr.ai 输入限制）迁入 PostgreSQL，后台支持创建、查看和撤销，用户可以兑换开通助手。
3. Manager 兼容本地 website（vertr.ai）现有认证、邀请码、订阅购买、账单和门户 API。

## 入口

- `/app`：普通用户 Google 登录、兑换、订阅、我的助手与账单。
- `/admin/invite-codes`：邀请码生成、兑换用户和关联实例管理。
- `/admin/stripe`：Agent 订阅价格及订阅账单页面。
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

网站商品标识与 Stripe 价格在 `/admin/agents` 的“运行与上架设置”中填写，对应 `productId` 和 `stripePriceId`。商品标识首次保存后固定，价格可修改或清空；空价格表示仅支持邀请码。没有商品标识的目录项不作为网站商品。Module 在创建订单/兑换时保存快照，不接收浏览器提供的价格、模块或跳转 URL。Web 助手使用 Telegram，采用配置模块的 Hub Hermes `Start-Agent` 协议。agent-hub 原 TG 的 `Action=start`/X 私有协议并非该协议，不能直接填旧 Module ID。

## Stripe 运行约定

订阅为单席位，支持 Checkout、Billing Portal、订阅更新、支付成功/失败、取消和 checkout 过期。回调的事件 ID、账单变更、助手任务在同一数据库事务提交。收到订阅相关事件时取 Stripe 当前订阅状态，校验订单元数据、价格与数量；不按回调抵达次序覆盖权益。

首次支付确认后生成持久化开通任务。任务复用 Hub 的 Access Key、Telegram 和 LLM 资源，创建并启动 Pod。取消/欠费（可配置）停止 VM，补缴成功恢复 VM；节点需要支持 `/admin/vms/stop` 和 `/admin/vms/resume`。实例、资源绑定在暂停时保留。

部署外部操作结果不明或进程中断时标记 `needs_review`，不盲目重复 Spawn。管理员需先核实远端状态；安全失败的任务可以在后台重试。`running` 表示启动请求得到响应，不表示已经做过业务消息验收。

依据：[Stripe Webhooks](https://docs.stripe.com/webhooks)、[Subscription Webhooks](https://docs.stripe.com/billing/subscriptions/webhooks)。Webhook API version 必须与 stripe-go/v86.1.0 匹配，使用 SDK 常量 `stripe.APIVersion`；服务拒绝不匹配版本。

## 配置与切换

1. 先执行 `./manager --config config.yaml migrate` 完成数据库迁移（失败时不要启动新版本；普通启动不再运行 AutoMigrate），配置 `auth.google`/`auth.jwt`。Google Client ID 必须匹配 vertr.ai 页面；Google Console 授权相应站点 origin。
2. 在 `deployment` 填写网站与小程序共用的节点、签名私钥、Gateway URL、Hermes Gateway Token。后台“助手管理 → 运行与上架设置”配置模块、网站商品标识、Stripe 价格 ID 并上架；先用测试账号验证模块。
3. 在 config.yaml 中配置 Stripe 测试密钥、Webhook secret、success/cancel/portal URL；Checkout 成功页 URL 需包含 `{CHECKOUT_SESSION_ID}`。本地 website 有 checkout-success.html，但正式站点路径需部署时核对。
4. Stripe 配置事件：checkout.session.completed/expired、invoice.paid/payment_failed、customer.subscription.updated/deleted。先用测试订阅验证。
5. 将 `window.VertrPlatformConfig.apiBase` 或 `window.VERTR_PLATFORM_API_BASE` 指向 Manager（或把原 platform-api 域名反代到 Manager）。客户端继续发送 Bearer，不需要跨域 Cookie。
6. 旧 JWT 若签名/issuer/audience 不一致，用户重新登录。旧用户/邀请码/账单/实例 JSON 数据不能仅通过切换域名自动进入 PostgreSQL；现有付费订阅上线前必须做数据映射导入，尤其核实旧运行模块协议。本次功能实现不操作生产数据、不切换域名。

部署机需保管真实 Stripe/Google/节点配置；仓库示例为空值，不包含生产凭据。

## 验证

- `go test ./...`：现有功能及新增接口测试。
- `go test -race ./manager -run TestCommerce -count=1`：新增功能竞争检测。
- `HUB_TEST_COMMERCE_POSTGRES_DSN=postgresql://... go test ./manager -run TestCommerce -count=1`：独立 PostgreSQL schema，验证并发兑换和支付事件幂等；DSN 必须为测试数据库，账号需允许创建/删除 schema。
- 浏览器以模拟 API 验证后台创建/撤销、用户页面、移动端布局及脚本错误；不代替真实 Google OAuth 或支付验收。

开发验证没有使用生产 Stripe 密钥、没有创建真实订阅或部署线上 Agent。

## 从旧配置调整

- 删除 `commerce`，将所需节点配置放入唯一的 `deployment`。不要保留两套不同节点配置。
- 删除 `miniProgram.pod` / `miniProgram.agent`；已有的小程序旧配置仍可读取为共享部署配置，以便过渡。新旧值冲突时拒绝启动，错误只显示字段名。
- 旧 `commerce.products` 中每条映射：根据 catalogId 打开对应助手，将原 map 的 key 填到“网站商品标识”，把 priceId 填到“Stripe 价格 ID”。支持 `telegram_customer_agent`、`x_agent` 等原网站标识。
- 不会自动把已有助手都变成网站商品。已有账单和实例保存的模块、价格与产品快照不变。
- 无需修改 `stripe` 密钥、回调和网站 API 路径；之后新增商品或改价格不必编辑 YAML 或重启 Manager。

## 后台订阅价格与邀请码页面

Stripe 全局配置（支付开关、两项密钥、跳转地址、支付策略）始终读取 config.yaml，修改后重启生效。旧版后台保存的 manager_stripe_settings 记录不再读取，也不会覆盖配置文件；已有表保留，不删除历史数据。

`/admin/stripe` 按助手列出上架状态、网站商品标识和 Stripe Price ID。可首次绑定商品标识并修改价格，保存与助手目录使用同一条数据库记录。商品标识绑定后固定；Price ID 留空仅支持邀请码，调整价格不改写已有订单快照。实际金额、币种、周期在 Stripe 中创建并由 Price ID 对应。此前配置示例中的 prices.telegramCustomer / prices.xAgent 映射应分别填入对应助手，网站商品标识仍使用 telegram_customer_agent / x_agent。

邀请码默认只需填写数量（默认 10）即可生成通用码，产品限制、到期时间、备注置于高级选项。表格展示关联 Agent、兑换用户姓名/邮箱、兑换时间，可复制单码或本批次所有码。概览统计全部记录，列表显示最近 1,000 条；实例列表可进入已有 Pod 管理页面查看资源，保留失败重试操作。订阅账单移至 Stripe 页面。

邀请码复制即领取：`POST /v1/admin/invite-codes/claim` 接受 1–100 个 codes，在事务内标记 `claimed_at`，重复领取或领取已兑换/撤销/过期码返回 409，批量操作全部成功或全部回滚。领取记录不影响用户首次兑换。单条和批量复制按钮在领取后禁用，刷新或更换管理员仍有效。剪贴板失败时当前页面显示本次领取结果，供手动复制。部署前执行 `manager migrate` 新增字段；历史复制行为没有记录，无法自动判断旧码是否曾被分发。
