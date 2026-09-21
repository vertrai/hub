# 网页助手目录

网站通过 `/v1/catalog?channel=web&locale=en` 和 `/v1/catalog/:id?channel=web&locale=zh` 读取目录。无需登录；不存在或未在网页上架的详情返回 404，非法 channel/locale 返回 400。

小程序继续使用原来的 `/v1/wechat/catalog`，不需要升级客户端。其目录响应和发布、详情访问、创建规则不变。

## 管理

后台“助手管理”新增网页发布、排序、邀请码开关和可选中英文覆盖文案。共用图标、助手 ID 和模块。`web` 是可空 JSON 配置，现有 AutoMigrate 自动增加列；不批量修改已有记录。

- `web == nil`：不进入新网页目录；原有网站商品继续沿用 Published 的交易规则。
- 配置了 `web`：网页展示和新交易遵循 web.published；独立于微信 Published。
- 旧管理请求省略 web 时保留已有设置，防止旧客户端清空网页配置。
- 删除需要微信、网页均下架，且没有已有实例/订单等依赖。

## 返回结构

列表为 `{agents: [...]}`，详情直接返回单个对象。公开字段：id、name、logoUrl、intro、summary、capabilities、compatibilityNote、consentText、published、acquisition、connection。

`acquisition` 包含 productId、invite.enabled、subscription.enabled。订阅可用性检查部署和 Stripe 配置；金额在 Stripe 结账页确认。`connection.type` 当前为 telegram。名称等按请求语言、另一个已配置语言、共用文案逐字段回退。

兑换使用 `/v1/invite-codes/redeem`，请求 `{product, inviteCode, consentAccepted}`；订阅仍使用 `/v1/billing/checkout-sessions`。对于已配置网页的商品，后端校验发布状态、邀请码可用性及说明同意；X 产品自动保留既有说明。

`GET /v1/agents?locale=zh` 保留原有字段，增加 catalogId、agent 目录摘要和 connection。实例 agentId 与目录 ID 不混用。下架不会隐藏用户自己的实例；connection.url 仅在实例运行时提供。

## 验证

`go test ./...` 包含网页/微信目录兼容及新商品兑换测试。PostgreSQL 专属测试需设置指向隔离数据库的 `HUB_TEST_POSTGRES_DSN` / `HUB_TEST_COMMERCE_POSTGRES_DSN`；默认跳过。

部署顺序：先 Hub → 后台明确勾选网页上架 → 网站。新网站没有本地硬编码目录作为回退。

## 独立的管理页面与字段归属

- `/admin/agents` 助手库：唯一 ID、默认名称/图标/介绍/能力、运行模块；新助手默认不上架。
- `/admin/agents/wechat` 微信小程序展示：原有可视化编辑器，小程序专属文案、图标、能力、创建过程、排序与上架。
- `/admin/agents/web` 网页版展示：中英文展示文案、预览、网页排序/上架、邀请码及网站订阅配置。

三个页面引用同一个 `AgentCatalogEntry.ID`，不复制助手或运行实例。共享默认信息保留在原字段，新增可空 JSON `Wechat` 保存小程序展示覆盖值；原 `Web` 保存网站展示配置。微信既有 published/sort_order 字段和所有公开响应格式保持不变。数据库启动迁移只新增可空列，旧数据无需批量重写。首次编辑基础信息会先保存原微信展示快照，以免改变线上小程序文案；网站未填写的文案继续使用基础信息。

后台新增 `/v1/admin/catalog-management/{core|wechat|web}` 查询入口；core 支持 POST 创建；`PUT /v1/admin/catalog-management/{scope}/{id}` 在事务行锁内读取最新记录，只合并该页面拥有的字段。即使页面提交了其他端的旧字段，也不会覆盖它们。原管理接口保留兼容；新页面均使用独立保存入口。

发布需重新构建、部署 Hub 后端（管理页面由 Go embed 打包）。小程序客户端无需更新。新增字段的真实 PostgreSQL 迁移与事务检查需在隔离测试数据库中执行（`HUB_TEST_POSTGRES_DSN`）。

### 注册信息收敛

助手库仅维护名称、默认图标、一句话介绍和运行模块，不再填写或更新详细介绍、能力。新注册无需提供展示能力。详细介绍和能力由各端独立编辑；微信旧字段作为兼容数据保留，网站不再回退到这些旧微信展示字段。网页未配置详细介绍时仅用一句话介绍兜底，能力可为空。注册保存接口忽略提交的 summary/capabilities，避免旧页面误改展示数据。

### 网站商品标识自动生成

网页版管理不再要求手填 productId。首次保存网页配置时，后台根据稳定的助手 ID 生成符合格式的内部商品标识并持久化；后续保存及上下架保持不变。已有商品标识原样保留，旧客户端显式修改已有标识仍被拒绝。Stripe 价格 ID 仍是单独的外部配置。此规则替代前文的“网页上架手填商品标识”说明。

### 创建方式与统一阅读确认

`web.inviteEnabled` 和 `web.subscriptionEnabled` 独立选择：仅邀请码、仅订阅、两者均可、两者皆不选时免费。旧数据缺少 subscriptionEnabled 时根据原 StripePriceID 推导，防止付费助手意外变为免费；后台保存后写入明确布尔值。选择订阅但支付服务不可用时不会降级为免费。

所有网页助手统一要求 consentAccepted，不再对 X 做特殊处理。网站需打开通用说明弹窗并点击“我已阅读并同意”后才能创建或订阅。新 `/v1/agents/free` 在认证及事务锁内检查上架、创建方式与运行模块，复用同一用户同一助手的免费实例，避免重复分配。现有邀请码、Stripe 创建入口均执行各自模式校验。

### 网页实例两阶段启动

网页 worker 只提交并等待 Spawn 成功，再将实例置为 `awaiting_setup`；此时不领取 Telegram Bot、不发送 Start-Agent。网页进度页展示独立渠道配置，可选择 Telegram、微信或同时选择。Telegram 可提供自己的 Bot Token（服务端 getMe 验证，不在响应中返回），或留空由 Hub 分配；微信使用现有 iLink 扫码授权，并按用户、实例隔离授权轮询。

`POST /v1/agents/:id/start` 验证实例所有权、Spawn 完成、运行授权及微信 Bot 归属，在事务中预留渠道并将实例重新入队。worker 自动取得 Hub LLM / Gateway 参数后提交 Start-Agent。重复提交不能再次入队，交易结果不确定时仍进入 needs_review，不自动重复交易。停止订阅可清理等待配置的 Pod。已运行实例保持原有运行与恢复行为；旧失败/不确定实例仍需先在管理后台核实交易结果。
