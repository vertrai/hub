# agent-hub 与 Hub 功能差异及合并评估

评估日期：2026-09-14。对比本机工作区，不代表远端最新版本或线上运行状况。

- Hub：`/Users/sandyzhou/codex-project/hub`，HEAD `81c38ba`（2026-09-11）；开始检查时只有未跟踪的 `.DS_Store` 文件。
- agent-hub：`/Users/sandyzhou/codex-project/agent-hub`，HEAD `71dcc03`（2026-07-15）；`go.mod` 有本地修改，`tools/` 未跟踪。结论依据当前工作区。
- 方法：核对注册路由、处理函数、持久化模型、后台任务和页面；未启动外部服务、未执行支付或部署，未验证生产可用性。下文“没有”指在本次检查的 Hub 自有控制面与接口中未找到对应实现，不排除外部 Hermes/Hymatrix 提供相近能力。

## 结论

建议以 **当前 Hub 的 Manager + Resources + PostgreSQL 为合并底座**，迁入 agent-hub 的用户商业流程及独立 Agent 应用。不要整体覆盖，也不要长期维护两套资源分配系统。

agent-hub 最值得补入的功能是：普通用户 Web 门户、邀请码、Stripe 订阅与付费开通、可保留实例的停用/重启、停服通知、多平台资源分发，以及 Telegram 客服业务应用。Google 交互式 OAuth 是另一种授权模式，可按产品需求加入。

Hub 已有动态助手目录、小程序用户登录与创建、微信扫码和换绑、LLM 中转、托管 Google 账号、浏览器和 Telegram 资源池，不能把这些重复计入新增功能。

## agent-hub 有而 Hub 缺失或仅部分覆盖的功能

| 功能 | agent-hub 实现 | 当前 Hub 差异 | 合并建议 |
|---|---|---|---|
| 普通用户 Google 登录与 Web 入口 | Google ID Token 登录、用户记录、用户 JWT、我的 Agent、邀请码开通测试/管理页面；附带页面未接支付 | 已有管理员 Google 登录和普通用户微信小程序登录；缺面向普通用户的 Google 登录与 Web 自助门户 | 如果要同时服务网页用户，优先补；复用现有目录 API 的业务逻辑，页面仍需产品化 |
| 邀请码开通 | 管理员批量生成、用户兑换、校验使用资格、记录兑换与产品/实例关系 | Access Key 是资源凭据，不是邀请码；目录的“使用条件”也不是资格校验系统 | 抽出可持久化的使用权益，支持邀请码和付费两种来源 |
| Stripe 订阅与账单 | Checkout、Customer Portal、订阅记录、发票链接/PDF、Webhook 验签与事件处理标记 | 没有购买/订阅/账单模型与路由；LLM 中转也没有计费配额 | 若目标包含收费，属于核心迁入项；将 JSON 存储改为数据库事务 |
| 支付结果驱动开通与停服 | invoice.paid 首次创建实例；支付失败可配置停服；订阅删除停服 | 小程序按创建请求分配资源，没有付费权益检查 | 在资源创建前检查权益，并建立支付事件与实例任务的幂等关系 |
| 独立停止、重启和后台启动推进 | 管理员 stop/restart；后台扫描 starting 状态执行启动 | 有 Spawn/Start/Eval/Delete；Delete 会停止 VM 并删除记录、释放部分绑定，缺保留实例的 suspend/resume 业务流程 | 先定义暂停/恢复语义，再迁入支付停服；不能用 Delete 代替订阅暂停 |
| 停服后 Telegram 提示 | 后台接管 stopped Agent 的 Telegram 消息，回复服务已停提示 | 无控制面对应实现 | 与暂停流程一并迁入，确保与运行中的 Bot 不同时消费消息 |
| 多平台资源供应 | Platform 注册、API Key 轮换；按平台和外部标识/idempotency key 分配及查询资源 | 有 Gateway User → Access Key → 资源，没有独立 Platform 调用方与平台隔离层 | 只有需要供多个产品/合作方使用时再加入；保留 Access Key 作为实际资源归属边界 |
| Google 交互式 OAuth | 授权 URL、回调换 Token、记录同意范围和邮箱、返回 Token JSON | 当前使用托管 Workspace 账号 + DWD 短期 Token，未提供 Google 用户 consent 回调 | 作为第二种连接模式新增；不能用它替换当前托管账号流程，也不能称现有代码已完整管理 OAuth 长期刷新 |
| Telegram 专用客服 Agent | 知识库、群聊批处理、升级规则、私聊收集信息、人工工单与回复修订 | 有 Telegram Bot 供应及 Hermes 对话入口，没有这套客服业务流程 | 作为独立助手模块合入；控制面只负责目录、配置、实例与权益 |
| X / Google 专用 Agent 与自有运行时 | 包含专用产品 provision、Agent 运行时及渠道实现 | Hub 偏向通用 Hermes/Hymatrix 模块；已有 X 相关资源不等于 X 产品完整工作流 | 按实际产品分别迁；先证明运行时可部署、协议兼容，再上架目录 |

以上差异的代码依据：

- 用户门户/邀请码/账单/停止重启路由：[agent-hub platform API](/Users/sandyzhou/codex-project/agent-hub/platformServer/api.go:17)；用户登录：[auth API](/Users/sandyzhou/codex-project/agent-hub/platformServer/auth/api.go:31)；邀请码持久化：[invite_code_store.go](/Users/sandyzhou/codex-project/agent-hub/platformServer/invite_code_store.go)。
- 支付行为：[Stripe 事件处理](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:231)、[首次付费开通](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:297)、[失败与取消停服](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:332)、[账单存储](/Users/sandyzhou/codex-project/agent-hub/platformServer/billing_store.go:23)。
- 后台启动和停服提示：[jobs.go](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:25)。
- 多平台与资源 API：[access API](/Users/sandyzhou/codex-project/agent-hub/accessServer/api.go:17)、[资源归属与幂等查询](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_store.go:199)。
- OAuth：[StartOAuth / callback / Token](/Users/sandyzhou/codex-project/agent-hub/accessServer/googleuser/service.go:294)。
- Hub 路由核对：[Manager API](/Users/sandyzhou/codex-project/hub/manager/api.go:21)、[Resources API](/Users/sandyzhou/codex-project/hub/resouces/api.go:32)、[网页入口](/Users/sandyzhou/codex-project/hub/web/web.go:82)。
- Hub 登录模式：[管理员 Google 登录](/Users/sandyzhou/codex-project/hub/manager/admin_auth.go:175)、[小程序登录](/Users/sandyzhou/codex-project/hub/manager/miniprogram.go:29)；Google 模式：[DWD issuer](/Users/sandyzhou/codex-project/hub/resouces/google/provider.go:72)。
- Hub 生命周期：[状态模型](/Users/sandyzhou/codex-project/hub/manager/schema/db.go:8)、[删除 Pod](/Users/sandyzhou/codex-project/hub/manager/api.go:608)、[空后台 jobs](/Users/sandyzhou/codex-project/hub/manager/jobs.go:4)。
- 专用 Agent 及运行时细项见配套 [agent-hub 功能盘点](/Users/sandyzhou/codex-project/hub/docs/research/agent-hub-feature-inventory.md)。

## 已有能力与不应重复迁入的部分

1. **助手目录与创建**：Hub 已支持动态目录、图片、上下架、模块快照、“我的助手”、防重复创建。agent-hub 的 TG/X/Google 产品代码可以提供应用，但不宜把目录退回固定产品分支。[目录实现](/Users/sandyzhou/codex-project/hub/manager/agent_catalog.go:69)、[创建流程](/Users/sandyzhou/codex-project/hub/manager/miniprogram.go:61)。
2. **Google、Browser、Telegram**：两边都有相关供应能力。Hub 已按 Access Key 分配资源，Google 账号池不足时可自动创建，Telegram 支持导入、生产账号授权与 BotFather 补池。不应再建立第二套池。[Resources 路由](/Users/sandyzhou/codex-project/hub/resouces/api.go:32)、[Google 分配](/Users/sandyzhou/codex-project/hub/resouces/google/service.go:140)、[Telegram 服务](/Users/sandyzhou/codex-project/hub/resouces/telegram/service.go)。
3. **微信**：Hub 已有扫码开通、二维码刷新、已有实例换绑和管理员重置。agent-hub 的 accessServer/weixin 有实现文件，但未在 accessServer 的 runApi 挂载，不计为已开放能力。[Hub 路由](/Users/sandyzhou/codex-project/hub/manager/api.go:24)、[agent-hub 注册入口](/Users/sandyzhou/codex-project/agent-hub/accessServer/api.go:17)。
4. **LLM 控制面**：Hub 有 Provider/OAuth、公开模型路由、每 Key 模型策略、自动领取 LLM Key 和 hub-chat 动态默认模型。应让迁入的运行时统一使用它；usage 转发不等于计费账本。[LLM 说明](/Users/sandyzhou/codex-project/hub/docs/llm-gateway.md:1)、[路由](/Users/sandyzhou/codex-project/hub/manager/api.go:53)。
5. **通用 Agent 能力**：agent-hub 自有运行时中的工具、会话、定时任务、浏览器操作不能一律算成 Hub 产品缺口。Hub 集成 Hermes，部分能力由外部运行时提供；只有需要统一控制面管理时才算需要新建接口。[启动 Hermes](/Users/sandyzhou/codex-project/hub/hymatrix-module/start-hermes/run.go:17)。

## 合并时必须解决的模型差异

### 用户与凭据

建议新增 Identity 映射，将 Google subject、微信 appid/openid 派生身份映射到稳定 User ID。现存管理员 JWT、普通用户 JWT 和 Gateway Access Key 各有用途，不能因 issuer 相同或共用签名文件就互相替代。

Hub 当前微信 User ID 由 appid/openid 派生，管理员白名单另行校验；agent-hub 普通用户由自己的用户 Store 管理。[微信身份](/Users/sandyzhou/codex-project/hub/manager/miniprogram.go:568)、[管理员验签与白名单](/Users/sandyzhou/codex-project/hub/manager/admin_auth.go:151)、[agent-hub 普通用户登录](/Users/sandyzhou/codex-project/agent-hub/platformServer/auth/api.go:73)。

### 资源归属与老凭据兼容

Hub 的核心约束是 Gateway User 关联 Access Key，由 Access Key 拥有 Browser/Google/LLM 等资源。agent-hub 引入 Platform、Resource、ExternalID 和独立 Browser/Google Key。建议做显式映射表：旧 Platform/User/Resource/Agent → 新 User/AccessKey/Resource/Pod，不直接按邮箱或名称合并。

迁移已有实例时保留浏览器 Profile、Google 账号和 Telegram Bot 的对应关系；可以先通过兼容适配器继续验证旧资源 Key，再逐步换发 Hub Key。重建资源会丢失会话和产品身份，不能只迁数据库记录而忽略供应商侧 ID。[Hub 领域约束](/Users/sandyzhou/codex-project/hub/CONTEXT.md:1)、[agent-hub ResourceRecord](/Users/sandyzhou/codex-project/agent-hub/accessServer/resource_store.go:27)。

### 产品与实例

建议区分 CatalogEntry（助手是什么）、Entitlement（为何有权使用）、AgentInstance（用户实例）、RuntimeDeployment（运行状态）、ResourceBinding（资源关系）。这是合并建议，不是现有模型。

当前 Hub 小程序每用户每助手保留一个当前实例，agent-hub 账单绑定自己的 AgentID。必须先决定一个订阅能创建几个实例、兑换权益是否同样受限制，再映射旧数据；否则可能重复创建或错误覆盖。[Hub 防重](/Users/sandyzhou/codex-project/hub/manager/miniprogram.go:113)、[账单字段](/Users/sandyzhou/codex-project/agent-hub/platformServer/billing_store.go:23)。

### 生命周期与支付一致性

Hub Delete 是停止 VM 后删除 Pod 和相关任务；付费暂停需要保留实例、资源和恢复路径，应新增 suspend/resume 操作。Webhook 处理需要数据库唯一事件 ID、实例开通幂等键、失败重试和补偿；原 JSON Store 和多步外部调用不能原封不动变成生产支付事务。

现有 agent-hub invoice.paid 对已有 Agent 只更新账单，不调用重启。因此不能假设“补缴后自动恢复”已经实现；合并时要明确加入这一转换。[invoice.paid](/Users/sandyzhou/codex-project/agent-hub/platformServer/stripe_billing.go:297)、[Hub Delete](/Users/sandyzhou/codex-project/hub/manager/api.go:608)。

### 存储与运行时

agent-hub 的用户、邀请码、账单、平台和资源等存在 JSON Store 实现；Hub 控制面是 PostgreSQL/GORM。迁入业务逻辑时改用 Hub 的持久化与约束，不建议把文件 Store 一起作为长期主存储。[账单文件写入](/Users/sandyzhou/codex-project/agent-hub/platformServer/billing_store.go:243)、[平台文件写入](/Users/sandyzhou/codex-project/agent-hub/accessServer/platform_store.go:162)、[Hub 数据库初始化](/Users/sandyzhou/codex-project/hub/manager/wdb.go)。

Hub 的小程序路径当前要求 Hermes；动态目录的 Module 字段不代表任意运行时协议已经兼容。TG/X/Google 产品需分别适配部署协议和 readiness，成功发送启动请求与实例真正可服务要分开记录。[Hub runtime 校验](/Users/sandyzhou/codex-project/hub/manager/miniprogram.go:503)、[agent-hub 启动分支](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:50)。

## 推荐落地顺序

1. **统一身份、实例和资源映射**：明确未来用户入口，保留现有微信流程；迁移前导出旧数据与供应商资源 ID 对照，完成 dry-run。
2. **补 Web 门户与邀请码**：用户登录、目录、我的实例、兑换资格先跑通；沿用 Hub 的资源供应和目录配置。
3. **补暂停/恢复和订阅**：先实现可验证的 suspend/resume，再连接 Stripe Checkout/Webhook/Portal；覆盖事件重放、付款失败、取消、补缴恢复和部署失败重试。
4. **迁客服应用**：独立接入运行时适配，上架目录，验证知识库、人工接管与消息消费；X/Google 按业务需求逐一跟进。
5. **按需补多平台和 OAuth**：这两项会扩大身份与授权模型，只有存在明确接入需求时推进。

如果当前目标仅是微信助手运营，第 2 步的 Web 登录和第 3 步的 Stripe 可延后，优先客服应用和邀请码权益。如果目标是收费的多产品平台，则身份、权益、暂停恢复与订阅应优先。

## 尚未证明的能力

- agent-hub 附带 Web 页面属于接口测试/管理入口，没有接入 billing/checkout/portal；可复用后端，不是现成付费用户门户。[页面](/Users/sandyzhou/codex-project/agent-hub/platformServer/web/index.html:585)。
- agent-hub Google Agent 的后台启动仅推进状态，依赖外部 runtime；TG/X 未配置 Hymx 时也直接标 running，不能据此认定端到端部署完成。[jobs.go](/Users/sandyzhou/codex-project/agent-hub/platformServer/jobs.go:50)。
- agent-hub OAuth 返回存储的 Token JSON；该函数本身没有证明持续自动刷新。[Token](/Users/sandyzhou/codex-project/agent-hub/accessServer/googleuser/service.go:362)。
- Stripe 流程是 Agent 产品订阅，不能等同于按 LLM Token 用量结算；Hub 同样明确未提供计费配额。[Hub LLM 文档](/Users/sandyzhou/codex-project/hub/docs/llm-gateway.md:3)。
- 本报告是代码级合并评估，没有运行跨服务验收测试；后续实施应以真实沙箱部署与支付测试事件验收。
