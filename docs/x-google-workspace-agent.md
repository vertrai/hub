# X Google Workspace Agent 的 Hub 接口

`agents/x-google-workspace-agent` 已迁移到 Hub，不再连接废弃的 agent-hub/accessServer。Agent 与其他 Hermes module 一样使用 `HUB_GATEWAY_URL`、`HUB_GATEWAY_API_KEY`，该 Access Key 需要同时启用 Google 和 Browser 权限。

| 用途 | 现有 Hub 接口 | 响应字段 |
| --- | --- | --- |
| 分配/获取托管 Google 账号 | GET /v1/google-user | googleUser.email、password |
| 获取 Google DWD 短期 token | GET /v1/google-user/access-token | accessToken、tokenType、expiresAt、email |
| 获取浏览器 | GET /v1/browser | browser.id、cdpUrl、liveUrl |
| 重置浏览器 | POST /v1/browser/reset | browser.id、cdpUrl、liveUrl |

资源归属仍按 Access Key，不增加旧的独立 Browser key、Google User key 或第二套账号池。Agent 将 browser.id 用作本地 harness session 名称，不要求旧的 browser.session 字段。

Google Workspace API 授权全部使用现有 DWD issuer。没有新增交互式 OAuth start/callback/token 路由，不需要浏览器 consent、OAuth Web Client 或客户端 refresh token。每次 Google API 操作由 Agent 向 Hub 取当前 access token；Hub 已按账号缓存并在到期前五分钟续期，Agent 不持久化 token。

Google 网页登录只服务于后续 X 的 Google SSO 或网页操作，与 API 授权无关。`/google_auth` 可直接检查 DWD，不需要先登录浏览器。

## 配置

Hub `cmd/resouces/config.example.yaml` 的 `google.authorization.scopes` 示例包含：

- https://mail.google.com/
- https://www.googleapis.com/auth/drive
- https://www.googleapis.com/auth/spreadsheets
- https://www.googleapis.com/auth/calendar
- https://www.googleapis.com/auth/documents

管理员须在 Google Workspace 管理后台对 `google.authorization.credentialsFile` 对应服务账号的 DWD Client ID 授予同样的范围，并为项目启用相关 Google API。示例配置不会自动更改已有部署或后台权限；修改服务端 scopes 后需按部署流程重启 Hub，使新 issuer 使用新配置。

继续使用现有 Google 创建和授权配置；创建账号的服务账号与 DWD 授权服务账号可以不同，授权域名必须匹配托管账号。权限不足时应修正 Hub/Workspace 配置，不切换到旧浏览器 OAuth。

## 迁移边界

此变更替换的是 Agent 的调用协议，不会自动导入旧 accessServer 的资源记录。已有实例若要求继续使用原 Google 账号或浏览器 profile，需要另行建立资源归属映射；直接换成新 Access Key 会获得 Hub 分配的资源。
