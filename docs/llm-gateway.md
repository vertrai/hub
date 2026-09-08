# Hub LLM 中转站

Manager 内置 LLM 中转功能，管理入口 `/admin/llm`。Codex 设备码 OAuth、Chat/Responses 转换和 Token 刷新流程参考 `llm-token-router`。支持多个 Codex 账号及多个 API Key Provider；不包含计费、配额和自动负载均衡。

## 在后台添加 Codex 账号

1. 点击 **添加 Codex 账号**，填写可选的账号备注名称，系统自动生成唯一 Provider ID。
2. 点击 **生成授权链接与 Code**。
3. 点击 **打开 OpenAI 授权页面**，在 OpenAI 官方页面登录目标账号，输入 Code 并完成授权。
4. 返回 Hub，点击 **完成连接**。尚未授权时会显示等待状态，并按上游要求限制查询间隔。
5. 成功后，Hub 保存所选预设模型与凭据并启用账号，不请求模型查询接口。重复上述操作、即可导入多个账号，系统为每个账号生成不同 ID。

账号列表显示备注名称、账号标识、邮箱（上游提供时）、凭据到期时间及配置模型。支持编辑模型、启停、删除和 **重新授权**。重新授权要求登录原来的 OpenAI 账号，并保留授权期间对名称、模型和启用状态的修改。添加其他账号请使用新的 Provider ID。

浏览器只接收临时会话标识与设备授权码，不接收 access token、refresh token 或 ID token。凭据由 Manager 从 OpenAI 兑换并保存，临近到期自动刷新。授权会话与发起管理员绑定，可取消；会话过期、页面刷新或 Manager 重启后需要重新生成授权码。数据库临时保存失败时可点击完成连接重试，已兑换的凭据会在该会话有效期内保留，不重复兑换一次性授权码。

后台添加账号直接使用 OAuth。

## 添加普通 API Provider

点击 **添加 API Provider**，选择服务商，填写备注、API Key 和模型列表，Provider ID 由系统自动生成。Base URL 自动填入服务商预设，也可修改。修改已有 Provider 时 API Key 留空表示保留原密钥。

| 服务商预设 | API 根地址 |
|---|---|
| OpenAI | `https://api.openai.com/v1` |
| DeepSeek | `https://api.deepseek.com` |
| 阿里云 Token Plan（个人版） | `https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` |
| 阿里云 Coding Plan | `https://coding.dashscope.aliyuncs.com/v1` |
| 阿里云百炼按量（北京） | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| 其他 OpenAI 兼容服务 | 管理员填写 |

Token Plan、Coding Plan 和百炼按量的密钥及地址需要配套使用。预设地址依据 [DeepSeek 文档](https://api-docs.deepseek.com/)、[阿里云套餐接入文档](https://help.aliyun.com/zh/model-studio/other-tools-token-plan)和[阿里云 Base URL 总览](https://help.aliyun.com/zh/model-studio/base-url)核对；模型权限由管理员根据实际账号配置，不硬编码模型目录。

普通 Provider 按 OpenAI 兼容协议转发：将公开模型 ID 解析为上游模型名，并替换 Authorization，保留 messages、tools、stream、reasoning/厂商扩展参数、usage、上游错误状态和 Retry-After。对于 `/responses`，需要上游本身支持 Responses API；DeepSeek 等 Chat Completions 服务应使用 `/chat/completions`，Hub 不将普通上游的 Chat 响应转换成 Responses。

## 模型路由与 Key 资源

LLM Key 是 Manager 管理的资源，每个 Hub Access Key 自动分配一个独立 LLM Key。重复申请返回同一个 Key，撤销后不会自动重新分配。手动创建的 Key 不绑定 Hub Access Key。

Provider ID 仅供内部关联，由系统自动生成。客户端使用公开模型 ID（例如 `deepseek-v4-pro`），不需要 Provider 前缀。保存 Provider 或完成 Codex OAuth 不会发布模型。管理员必须在“模型路由”明确创建对外模型，并选择实际 Provider 和上游模型；Key 只能选择这些有效对外模型。

每个 Key 必须明确保存 `allowedModels` 和 `defaultModel`。默认模型必须属于允许列表；空列表拒绝所有模型，没有默认全部放行或旧数据迁移逻辑。`GET /llm/v1/models` 只返回该 Key 可用的模型。

`hub-chat` 是保留的动态别名：每次推理根据当前 Key 的 `defaultModel` 解析。修改 Key 的默认模型后，下一次请求立即使用新模型，不需要修改或重启 Hermes；已经开始的请求不受影响。显式指定模型仍受该 Key 允许列表限制。

## 自动申请与 Hermes 配置

1. 在后台添加 Provider，并确认公开模型路由。
2. 在“自动申请设置”填写客户端可访问的 Base URL（以 `/llm/v1` 结尾）、允许模型及默认模型。
3. 使用 Hub API Key 向 **Manager** 请求资源：

```http
POST /v1/llm
Authorization: Bearer <Hub API Key>
```

返回包含 `apiKey`、`keyId`、`baseUrl`、`provider: "custom"`、`model: "hub-chat"`、`defaultModel` 和 `models`（模型对象列表）的 JSON。Manager 每次向 Resources 验证 Hub Key 身份，确保仅返回该 Key 的资源。

Hermes 配置直接使用返回值：

```text
llm_provider = custom
llm_base_url = <返回的 baseUrl>
llm_api_key = <返回的 apiKey>
llm_model = hub-chat
```

也可将 `llm_model` 设置为返回列表中的显式模型 ID。Hymatrix 后台启动页默认使用 Hub LLM 资源，可点击领取查看配置，启动时后端重新验证并获取。小程序创建 Agent 自动使用其 Hub Key 领取资源；无需配置静态 `miniProgram.agent.llm`。

自动申请模型设置只作为新 Key 的初始策略；修改它不会覆盖已分配 Key 的独立策略。升级已分配 Key，在后台编辑其允许模型及默认模型，或调用管理员接口：

```http
PATCH /v1/admin/llm/keys/<keyId>/policy
Content-Type: application/json

{"allowedModels":["deepseek-v4-pro","gpt-5.6-sol"],"defaultModel":"gpt-5.6-sol"}
```

示例中的模型须先配置有效路由。策略修改使用管理员认证，普通 Hub Key 不能自行提升权限。

## 接口

管理接口使用现有管理员会话认证：

| 方法 | 地址 | 用途 |
|---|---|---|
| GET | `/v1/admin/llm/presets` | 服务商及模型预设 |
| GET / POST | `/v1/admin/llm/providers` | 查看/新建 Provider，自动生成 ID |
| PUT / DELETE | `/v1/admin/llm/providers/:id` | 更新/删除 Provider |
| POST | `/v1/admin/llm/oauth/device/start` | 开始 Codex OAuth |
| POST | `/v1/admin/llm/oauth/device/complete` | 完成 OAuth |
| DELETE | `/v1/admin/llm/oauth/device/:state` | 取消 OAuth |
| GET / PUT | `/v1/admin/llm/routes` | 查看/设置公开模型路由 |
| DELETE | `/v1/admin/llm/routes/:id` | 删除对外路由；后续请求无法调用该模型 |
| GET / PUT | `/v1/admin/llm/resource-settings` | URL、自动申请初始模型策略 |
| GET / POST | `/v1/admin/llm/keys` | 列表/创建；创建需传 name、allowedModels、defaultModel |
| PATCH | `/v1/admin/llm/keys/:id/policy` | 修改允许模型和默认模型 |
| DELETE | `/v1/admin/llm/keys/:id` | 撤销 Key |
| GET / POST | `/v1/admin/access-keys/:id/llm` | 领取指定 Hub Key 的 LLM 资源 |

资源接口：Manager 的 `GET / POST /v1/llm` 使用 Hub API Key；Resources 的 `GET /v1/access-key` 返回当前 Hub Key 的身份，不返回密钥。

推理接口使用 `Authorization: Bearer <LLM API Key>`：

- `GET /llm/v1/models`
- `POST /llm/v1/chat/completions`
- `POST /llm/v1/responses`

两个推理接口支持流式和非流式。普通上游需自行支持所选接口；Codex 通过上游 Responses SSE，Hub 负责 Chat 转换。

## 本地测试与部署

纯手动创建 Key 和推理中转可运行 Manager（连接数据库）。测试 Hub Key 自动领取、小程序或 Hymatrix 领取流程，需要同时运行 **Manager 和 Resources**，并配置 Manager 的 Resources 地址。两者均需更新至包含资源身份接口的版本。

数据库自动创建 Provider、Key、公开模型路由和自动申请设置表。没有旧数据回填或无限制 Key 兼容逻辑。手动 Key 只持久化摘要，完整值仅创建时返回；自动分配 Key 为支持重复领取，还保存密钥。上游凭据和资源密钥均属于数据库中的敏感数据，管理列表不返回完整密钥。

当前不包含计费、额度、自动负载均衡。OAuth 会话及刷新锁在单个 Manager 进程内，部署多副本需要另行实现共享协调。撤销 LLM Key 后新推理请求失效。自动申请没有开关，配置有效默认策略后即可申请。

## Token Plan 模型预填

目前仅提供 Token Plan 个人版预设，沿用 `aliyun-tokenplan` 标识。2026-09-08 根据[官方个人版概述](https://help.aliyun.com/zh/model-studio/token-plan-personal-overview)核对，预填 9 个对话模型。图片、音频、视频生成模型不纳入当前对话接口预设。

新建时切换服务商自动填入对应模型；编辑时保留已保存的模型。可直接逐行追加新模型，或点击“补充预设模型（保留已有）”合并去重。预设是文档快照，不会自动修改已有 Provider，也不会限制手动新增的模型名。

## 自动生成 Provider ID

新建普通 Provider 使用 `POST /v1/admin/llm/providers`，后端按服务商前缀与随机标识生成 ID；Codex 新建 OAuth 无需提交 `providerId`。ID 用于内部关联路由，备注名称可修改，ID 保持稳定；重新授权由页面自动携带原账号 ID。

删除路由不会自动更换 Key 的默认模型；管理员应调整引用该路由的 Key 及自动申请策略。早期版本自动生成的同名路由无法与管理员创建的路由可靠区分，系统不会自动删除，可在后台逐项删除不需要公开的路由。

## 后台为用户创建 LLM Key

选择用户 → 选择该用户的 Hub Key → 点击“创建 LLM API Key” → 弹窗勾选允许的对外模型，并选择 `hub-chat（默认模型）→ 对外模型 ID` → 创建。已有资源直接显示完整 LLM Key、Base URL 和模型配置，也可修改模型策略。查看不会创建资源，重复创建返回 409。

管理接口 `GET /v1/admin/access-keys/:id/llm-resource?userId=...` 查看；`POST` 同一路径传 `userId`、`allowedModels`、`defaultModel` 创建。这里的 id 为 Resources Hub Key ID。管理员手动选择的策略独立于自动申请默认策略，自动领取会返回同一个已绑定的 LLM Key。

## LLM 测试页面

进入 `/admin/llm/test`，选择用户及 Hub Key 读取已创建的 LLM Key，自动加载授权模型后发送测试消息。请求使用当前 Hub 的 `/llm/v1/chat/completions`，显示状态、耗时、回复、原始响应及用量。选择 Key 不触发自动创建；密钥不写入浏览器存储。页面测试不验证对外 Base URL 的远程可达性。

Hermes 专用配置中的 `llm_provider=custom` 对应启动代码写入的 `model.provider`；上游 Provider 由 Hub 的模型路由决定。

## DeepSeek 模型预填

根据 [DeepSeek 官方接入文档](https://api-docs.deepseek.com/)（2026-09-08 核对），选择 DeepSeek 自动预填 `deepseek-v4-flash`、`deepseek-v4-pro`、`deepseek-v4-flash-vision-exp`。最后一个是支持图片输入的实验模型。编辑已有 Provider 保留原列表，可点击补充预设模型合并去重，也可自行追加。预填来源模型不会自动发布对外路由。

Codex 新账号预填官方主要模型：`gpt-6-astra`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-5.3-codex-spark`、`gpt-5.5`。依据 [官方模型文档](https://learn.chatgpt.com/docs/models)，2026-09-08 核对。可手动增删或合并预设；这是文档快照而非账号权限探测，实际可用性取决于套餐与开放范围。OAuth 完成不再依赖模型查询接口。
