# Hub

为 Codex、Claude、Hermes、pi coding agent 等 Agent 提供可直接使用的 Google 账号、Gmail、Google Drive、远程浏览器和 Telegram Bot。

安装 Skills 后，你可以直接对 Agent 说：

- “申请一个 Google 账号”
- “查看邮箱里的未读邮件”
- “发送一封邮件给 user@example.com”
- “把这个文件上传到 Google Drive 并返回分享链接”
- “使用远程浏览器打开 example.com”
- “通过 Telegram Bot 和 Hermes 对话”

## 安装 Skills

安装前，请向 Hub 管理员获取：

- Gateway URL
- Gateway API Key（以 `gw_sk_` 开头）

然后把下面这段话完整发送给你的 Agent：

```text
请按照这个安装文档安装 Hub Skills：
https://raw.githubusercontent.com/vertrai/hub/main/agent-skills/INSTALL.md

需要配置时向我询问 Gateway URL 和 API Key。不要在输出中显示 API Key。
```

Agent 会自动完成下载、安装和配置。它询问时，再分别提供 Gateway URL 和 API Key。

### Hermes 用户

安装完成后，在当前 Hermes 对话中发送：

```text
/reload-skills
```

不需要重启 Hermes 服务。热加载完成后即可直接使用。

### pi coding agent 用户

Skills 默认安装到 `~/.pi/agent/skills/`（也支持 `PI_CODING_AGENT_DIR`）。安装完成后，在当前 pi 对话中发送：

```text
/reload
```

重新加载完成后即可直接使用，不需要重启 pi。

## 可以使用的能力

### Google 账号

```text
申请一个 Google 账号
获取我的 Google user
告诉我 Google 邮箱地址
使用 Google 账号登录网页
```

同一个 Gateway API Key 会一直使用同一个 Google 账号。

### Gmail

```text
查看 Google 邮箱
列出最近 7 天的未读邮件
读取这封邮件
发送一封测试邮件
创建一封邮件草稿
```

### Google Drive

```text
列出 Drive 中的文件
创建一个文件夹
创建一个文本文档
上传这个文件并返回分享链接
下载这个 Drive 文件
```

新创建或上传的 Drive 内容默认设置为“知道链接的人可以查看”，不会默认授予编辑权限。

### 远程浏览器

```text
使用远程浏览器打开 example.com
在网页中点击登录按钮
填写这个表单
返回浏览器实时查看链接
重置远程浏览器
关闭浏览器会话
```

同一个 Gateway API Key 会复用同一个浏览器 Profile，以保留网站登录状态。

### Telegram

Telegram Bot 是 Hermes 的对话入口。管理员为 Gateway API Key 开通 Telegram
权限并分配 Bot 后，用户可以直接打开对应的 `https://t.me/<bot_username>` 与
Hermes 私聊。

Hymatrix Pod 的使用流程：

1. 在“用户与密钥”中签发或编辑 Gateway API Key，勾选 `Telegram` 资源权限。
2. 在“Telegram 资源池”中导入已有 Bot，或连接 Telegram 生产账号后通过
   BotFather 自动生产 Bot。
3. 在“Hymatrix Pods”中选择用户和 API Key，点击“从 Resources 领取”。系统会
   为该 Key 固定分配一个 Bot，并自动填写 Bot Token 和 `t.me` 链接。
4. 创建 Pod。启动参数会把 Bot Token 注入 Hermes，用户点击页面显示的 Bot
   链接即可开始对话。

同一个 Gateway API Key 会一直使用同一个 Telegram Bot。Hymatrix Module 中的
`start-hermes` 会把第一条 Telegram 私聊自动设为 Hermes Home Channel，供定时
任务结果和跨平台消息投递使用；通常不需要再手动发送 `/sethome`。默认只会自动
绑定私聊，不会把群聊设为 Home Channel。

如果不希望由 Resources 分配，也可以在创建 Hymatrix Pod 时手动填写已有 Bot
Token。Bot Token 属于敏感凭据，不要写入日志、聊天消息或提交到 Git。

## 常见问题

### Agent 没有调用 Gateway Skills

Hermes 用户先发送：

```text
/reload-skills
```

然后重新描述任务，例如“查看 Google 邮箱”或“使用远程浏览器打开网站”。

### 获取 Google 账号失败

账号池为空时，Gateway 会自动创建一个 Workspace 账号并立即分配。首次请求可能因此耗时更长。如果自动创建失败，检查服务端返回的 Google Workspace 错误和创建凭据；不要自行注册或切换其他 Google 账号方案。

### Telegram Bot 领取失败

- 返回 `403`：当前 Gateway API Key 没有开通 Telegram 资源权限。前往“用户与
  密钥”调整该 Key 的权限，或者在 Hymatrix Pods 页面手动填写 Bot Token。
- 返回 `503`：Telegram 资源池中没有可分配的 Bot。管理员需要导入或生产新的
  Bot。
- Bot 能收到消息但没有 Home Channel：新 Module 会在第一条私聊时自动设置；
  旧版本可以在 Bot 私聊中发送 `/sethome`。

### Gateway URL 无法连接

确认 Agent 所在环境能够访问 Gateway URL。在 Docker 中访问宿主机服务时，通常使用：

```text
http://host.docker.internal:8085
```

### 如何更新 Skills

再次把上面的安装文档链接发送给 Agent，让它重新执行安装流程即可。

## 服务端开发

以下内容仅供部署 Hub 服务的管理员和开发者使用；普通 Skills 用户不需要执行。

分别复制两个服务的示例配置。Resources 填写资源供应商配置；Manager 填写自己的 PostgreSQL、管理员密钥、Resources 内网地址和 Hymatrix 配置：

```bash
cp ./cmd/resouces/config.example.yaml ./cmd/resouces/config.yaml
cp ./cmd/manager/config.example.yaml ./cmd/manager/config.yaml
go run ./cmd/resouces --config ./cmd/resouces/config.yaml
# 另一个终端
go run ./cmd/manager --config ./cmd/manager/config.yaml
```

后台管理页面：

```text
http://<manager-host>:8086/admin
```

API 测试页面：

```text
http://<manager-host>:8086/admin/test
```

请勿提交 `config.yaml`、Google Service Account JSON、Gateway API Key 或其他 credentials。Google 账号创建和 Access Token 授权必须使用两个独立的 Service Account。

### 服务目录

- `cmd/resouces` + `resouces`：内网资源服务。负责 Gateway API Key 鉴权与 Browser、Google、Telegram 资源生命周期，不挂载后台页面。
- `cmd/manager` + `manager`：管理控制面。负责用户、API Key 创建、Resources 内网代理，以及分别构建和发送 Spawn、Start-Agent 交易。
- `web`：只挂载到 Manager。页面按 Manager 管理功能与 Resources 资源池功能分区，浏览器不会接触 Resources 内部密钥和内网地址。
- `hymatrix-module/start-hermes/skills`：四个 Gateway Skills 的唯一源码目录，同时供 Module 内嵌和外部安装器使用。
- `agent-skills`：外部 Agent 的安装、配置脚本和可执行安装文档，不再维护 Skills 副本。

Manager 的 Spawn 与 Start-Agent 是两个独立操作。创建 Pod 的表单只通过 Spawn Tags 传递 `RUNTIME_TYPE`；Spawn 成功并取得 PID 后，管理员需要在 Pod 列表点击 Start，打开独立表单填写 LLM、Gateway 和消息渠道配置，再向该 PID 发送 `Action=Start-Agent`。敏感的 `Container-Env-*` 参数通过 SDK encrypted tags 发送，Node 必须提供有效的 `Encryption-Public-Key`。

两个服务可以使用同一个 PostgreSQL 数据库。Manager 固定使用 `manager_users`、`manager_hymatrix_pods`；Resources 使用各自的资源表，服务之间不直接查询对方的数据表。

Hymatrix 的 Node URL、签名私钥、Module、Scheduler 与 LLM 参数由管理员在 `/admin/hymatrix` 创建 Pod 时填写，并按 Pod 保存到 `manager_hymatrix_pods`，不再放在 Manager 的 YAML 配置中。私钥、LLM Key、Gateway Key 和 Bot Token 不会通过列表接口返回。

Manager 会把新创建 Gateway API Key 的明文与 Resources Key ID 保存到 `manager_access_keys`，用于在创建 Pod 时选择。一个 Key 绑定一个 Pod 后即标记为已使用，不能再次选择。旧版本只在 Resources 保存哈希的 Key 无法恢复明文，需要重新创建后才能用于 Pod。Hymatrix 页面内置的 ETH 私钥是公开测试私钥，只能用于测试环境。

### Telegram Bot 资源

Telegram Bot 支持两种入池方式：管理员手动导入已有 Bot，或者授权 Telegram 用户账号后让服务通过 BotFather 自动创建。

推荐从 Manager 后台的 `/admin/telegram` 完成生产账号授权、自动生产、手动导入
和资源池查看。创建或调整 Gateway API Key 时必须启用 `Telegram` 权限，否则该
Key 调用领取接口会返回 `403`。

手动导入已有 Bot：

```http
POST /v1/admin/telegram/bots
Authorization: Bearer <manager-admin-key>
Content-Type: application/json

{"botToken":"<telegram-bot-token>","username":"example_bot"}
```

Agent 使用 Gateway API Key 获取固定分配给该 Key 的 Bot；账号池为空时返回 `503`：

```http
GET /v1/telegram-bot
Authorization: Bearer <gateway-api-key>
```

同一个 Gateway API Key 最多绑定一个 Telegram Bot，重复获取会返回同一个 Token。Manager 的管理员列表接口 `GET /v1/admin/telegram/bots` 会代理到 Resources，并只返回脱敏 Token。

自动创建流程需要管理员依次调用：

```text
POST /v1/admin/telegram/auth/init       # phone、apiId、apiHash，发送验证码
POST /v1/admin/telegram/auth/verify     # accountId、code
POST /v1/admin/telegram/auth/2fa        # 仅账号启用 2FA 时调用
GET  /v1/admin/telegram/auth/status
GET  /v1/admin/telegram/auth/accounts
POST /v1/admin/telegram/bots/create     # body: {"count": 1}
```

授权完成后，服务每分钟检查一次可用 Bot 数量，并通过 BotFather 自动补充到 `telegram.minAvailableBots`。遇到 Telegram `FLOOD_WAIT` 会进入冷却期，期间自动创建接口返回 `429`，而手动导入仍然可用。Telegram MTProto Session 保存在 `telegram.dataDir`，必须使用持久化私有目录，不能提交到 Git。
