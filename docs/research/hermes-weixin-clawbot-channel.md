# Hermes Agent 的 Weixin / ClawBot Channel 实现原理

调研日期：2026-08-17

## 结论摘要

Hermes Agent 当前支持的 `weixin` channel，是 NousResearch 官方仓库中 Gateway 的原生 `WeixinAdapter`，通过腾讯 **iLink Bot API** 接入个人微信；它不是企业微信（WeCom）回调，也不是模拟登录、客户端 Hook 或网页微信协议，更不是社区 `hermes-wechat` skill。官方发布说明称其为 “Native WeChat support via iLink Bot API”。[[官方发布](https://github.com/NousResearch/hermes-agent/releases/tag/v2026.4.13)] [[官方适配器源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py)]

腾讯另有官方 [`Tencent/openclaw-weixin`](https://github.com/Tencent/openclaw-weixin) 仓库：其中 `@tencent-weixin/openclaw-weixin` 是给 **OpenClaw** 用的 channel plugin，并公开了相同 iLink 后端协议。Hermes 并不运行这个 OpenClaw 插件，而是在自己的 `weixin.py` 中按该协议实现 Hermes 原生 adapter。换言之，**ClawBot/iLink 是微信提供的接入能力，OpenClaw plugin 与 Hermes adapter 是两个不同宿主的客户端实现**。[[腾讯官方说明与 Backend API Protocol](https://github.com/Tencent/openclaw-weixin/blob/main/README.md#backend-api-protocol)]

核心链路是：

```text
微信用户
  ↕
腾讯 iLink Bot 服务（API + 加密 CDN）
  ↕ HTTP 长轮询 / REST
Hermes Gateway: WeixinAdapter
  ↕ MessageEvent / SendResult
Gateway Runner ↔ Hermes Agent
```

入站采用 `getupdates` HTTP 长轮询，不需要公网 webhook 或 WebSocket；出站走 `sendmessage`。每个会话的最新 `context_token` 要随回复回显。媒体内容不直接嵌在消息里，而是经腾讯 CDN 传输，并以每文件随机/下发密钥进行 AES-128-ECB 加解密。[[官方使用文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/weixin.md#L7-L12)] [[源码设计说明](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1-L10)]

## 1. 项目与组件定位

- 官方项目：[`NousResearch/hermes-agent`](https://github.com/NousResearch/hermes-agent)。
- 平台标识：`Platform.WEIXIN`；配置和启用状态纳入 Hermes 统一 Gateway。[[配置源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/config.py)]
- 适配器：`gateway/platforms/weixin.py` 中的 `WeixinAdapter(BasePlatformAdapter)`。它把 iLink 原始消息转为 Gateway 标准 `MessageEvent`，并通过基类 `handle_message()` 交给 Agent；回复则返回标准 `SendResult`。[[适配器源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1102-L1117)] [[平台适配器架构](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/adding-platform-adapters.md)]
- 默认 API：`https://ilinkai.weixin.qq.com`；默认 CDN：`https://novac2c.cdn.weixin.qq.com/c2c`。[[源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L83-L96)]
- 工具集预设为 `hermes-weixin`。[[Gateway 文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/index.md)]

因此，“ClawBot channel”在 Hermes 中本质上是一个协议适配器：ClawBot/iLink 提供微信侧机器人身份和传输协议，Hermes Gateway 负责身份配置、消息游标、访问控制、媒体解密、事件标准化以及 Agent 回复投递。

## 2. 认证与绑定

运行 `hermes gateway setup` 并选择 Weixin 后：

1. Hermes 对 `ilink/bot/get_bot_qrcode?bot_type=3` 发起 GET；
2. 显示 iLink 返回的完整可扫码 URL/二维码；
3. Hermes 循环查询 `ilink/bot/get_qrcode_status?qrcode=...`，处理 `wait`、`scaned`、`scaned_but_redirect`、`expired`、`confirmed` 状态；
4. `confirmed` 响应提供 `ilink_bot_id`、`bot_token`、`baseurl` 和 `ilink_user_id`；
5. 凭据保存至 `~/.hermes/weixin/accounts/<account_id>.json`，文件尝试设为 `0600`；环境配置对应 `WEIXIN_ACCOUNT_ID`、`WEIXIN_TOKEN`、可选 `WEIXIN_BASE_URL`。[[QR 流程源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L967-L1081)] [[凭据存储源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L234-L273)]

已认证 API 请求使用 `AuthorizationType: ilink_bot_token` 与 `Authorization: Bearer <bot_token>`，同时携带 `iLink-App-Id: bot`、客户端版本和随机 `X-WECHAT-UIN`。[[请求头源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L218-L232)]

Gateway 启动要求 account ID 与 token 同时存在；同一本机同一 token 会加锁，避免两个 Hermes poller 同时消费导致重复或游标竞争。[[启动源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1212-L1244)]

## 3. 收发消息链路

### 入站

`connect()` 建立独立的 poll/send `aiohttp` session，并启动 `_poll_loop()`。poll loop 从磁盘恢复 `get_updates_buf`，向 `ilink/bot/getupdates` POST：

```json
{"get_updates_buf": "<上次游标>", "base_info": {"channel_version": "2.2.0"}}
```

默认长轮询超时 35 秒，服务端也可通过 `longpolling_timeout_ms` 调整。响应中的新 `get_updates_buf` 原子持久化到 `<account_id>.sync.json`，`msgs` 中每条消息以异步任务并发处理；超时被视为空结果并立即继续轮询。因此完全不需要 webhook 入站端口。[[getupdates 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L413-L431)] [[poll loop 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1281-L1327)]

单条消息处理会：按消息 ID 和文本指纹去重；推断 DM/group；应用访问策略；保存发送者最新 `context_token`；提取正文、引用消息和媒体；构造 `MessageEvent` 后调用 Gateway。纯文本还会等待默认 3 秒静默窗口，把微信连续发送的片段合并成一次 Agent 调用。[[消息处理源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1380-L1434)] [[文本合并源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1462-L1518)]

错误恢复：前两次失败等待 2 秒，连续第三次后退避 30 秒；`errcode=-14` 或判定为陈旧 session 时暂停 10 分钟；消息去重窗口为 300 秒。[[常量与错误码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L96-L116)]

### 出站与 `context_token`

文本回复 POST `ilink/bot/sendmessage`，核心消息体包含 `to_user_id`、Hermes 生成的唯一 `client_id`、完成态、文本 `item_list`，以及可用时的 `context_token`。[[sendmessage 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L433-L466)]

`context_token` 是 iLink 会话连续性的关键：每次入站按 `account_id + peer` 更新，存于 `~/.hermes/weixin/accounts/<account_id>.context-tokens.json`，重启后恢复，出站自动取该 peer 的最新值。源码还在 session 过期时尝试去掉 token 降级重发，使 cron/home-channel 推送仍可能成功；这属于 Hermes 的兼容逻辑，不应理解为 iLink 永远允许无上下文主动发送。[[token store](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L276-L318)] [[发送重试](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1668-L1712)]

另外，适配器通过 `getconfig` 获取并缓存约 10 分钟的 `typing_ticket`，再以 `sendtyping` 发开始/停止输入状态。WeChat 不支持 Hermes 编辑已发送消息，因此流式输出采用“最终结果一次发送”，避免残留流式光标。[[typing 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L468-L508)] [[编辑限制](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1102-L1104)]

## 4. 关键 iLink 端点

| 端点 | 用途 |
|---|---|
| `ilink/bot/get_bot_qrcode` | 获取绑定二维码 |
| `ilink/bot/get_qrcode_status` | 长查询扫码/确认状态并取得凭据 |
| `ilink/bot/getupdates` | 带游标的入站 HTTP 长轮询 |
| `ilink/bot/sendmessage` | 发送文本或媒体引用 |
| `ilink/bot/getconfig` | 获取 typing ticket 等会话配置 |
| `ilink/bot/sendtyping` | 开始/停止输入状态 |
| `ilink/bot/getuploadurl` | 获取加密媒体上传参数/URL |

端点名称和超时常量均直接定义在官方适配器中。[[源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L83-L105)]

上述 headers、`getUpdates` 长轮询及 sync cursor、`sendMessage/context_token`、`getUploadUrl`、`getConfig/sendTyping` 与消息 item 类型，也可在腾讯官方仓库公开的后端协议中交叉验证。[[腾讯 Backend API Protocol](https://github.com/Tencent/openclaw-weixin/blob/main/README.md#backend-api-protocol)]

## 5. 媒体的 AES/CDN 链路

入站支持图片、视频、文件和语音。适配器读取消息中的 `encrypted_query_param` 与每文件 `aes_key`，从 `/download?encrypted_query_param=...` 下载密文，以 AES-128-ECB 解密，随后缓存成 Agent 可消费的本地文件；引用消息里的媒体也会提取。语音若只有微信提供的转写则以文本进入；有原始媒体时下载 SILK，让 Gateway 的中央 STT 流程处理。[[官方媒体说明](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/weixin.md#L187-L205)] [[语音解析源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L891-L934)]

出站流程为：

1. 读取明文并生成随机 16-byte AES key、`filekey`、MD5；
2. AES-128-ECB + PKCS#7 填充加密；
3. 调 `getuploadurl`，提交媒体类型、明/密文长度、MD5 和 key；
4. 对 CDN upload URL POST 密文，读取返回的 `x-encrypted-param`；
5. 调 `sendmessage`，只发送加密引用、key、尺寸/文件名等元数据。[[上传 URL 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L510-L560)] [[完整发送链路](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L2030-L2121)]

图片、视频、普通文件可原生投递；当前源码明确说明原生 voice bubble 尚未证明稳定，`send_voice` 会退化为文件附件，这是比笼统的“支持语音”更准确的能力边界。[[语音发送源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1988-L2012)]

## 6. 状态持久化与安全限制

- 凭据：`<account_id>.json`，尝试权限 `0600`。
- 消费游标：`<account_id>.sync.json`，保证重启后从正确位置继续。
- 回复上下文：`<account_id>.context-tokens.json`，按 peer 保存。
- 去重：消息 ID + 文本指纹，TTL 5 分钟。
- 出站远程媒体：先调用统一 URL 安全检查阻断私网/内部地址，防 SSRF；下载超时 30 秒。[[SSRF 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L2013-L2029)]
- 访问控制：DM 支持 `pairing`、`allowlist`、`open`、`disabled`；group 支持 `allowlist`、`open`、`disabled`。当前 main 源码的构造器默认 DM 是 `pairing`，而文档配置表仍写 `open`，存在官方文档与代码漂移；生产中应显式设置策略，并以部署版本源码为准。`open` 还要求 `WEIXIN_ALLOW_ALL_USERS=true` 或全局 opt-in。[[策略源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1150-L1159)] [[策略执行](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1435-L1460)]
- 文本最长 4000 字符，超限按 Markdown 逻辑块切分并做发送节流。[[官方文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/weixin.md#L241-L259)]

## 7. 独立 Bot 身份与群聊边界

扫码并不是把用户的普通个人微信号变成“可脚本化账号”；它连接的是独立的 iLink bot identity，典型 ID 为 `...@im.bot`。官方 Hermes 文档明确警告：该身份通常不能像普通联系人一样加入微信群；iLink 对多数 bot-type 账号通常不投递普通微信群事件，包括对扫码个人号的 `@`；扫码个人号与 iLink bot 是不同身份。[[官方警告](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/weixin.md#L15-L23)]

因此：

- 最可靠的产品形态是用户与 iLink bot 的 DM；
- `WEIXIN_GROUP_POLICY` 只是 Hermes 在“iLink 实际送来群事件”之后的二次过滤，不能让平台凭空开放群事件；
- group allowlist 中填的是群 chat ID，不是群成员 user ID，默认 group policy 为 `disabled`；
- 不能把该 channel 等同于“监听扫码账号全部好友/群聊”，也不能把它视为企业微信自建应用或客服 API。

## 8. 对“原理”的一句话概括

Hermes Weixin/ClawBot channel 是一个运行在 Gateway 内的 iLink 协议客户端：二维码完成独立 bot 身份和 token 的配置，`getupdates + 持久游标` 实现无 webhook 的可靠入站，`context_token + sendmessage` 维持会话回复，媒体通过 `getuploadurl + AES-128-ECB 加密 CDN` 搬运，再由适配器把微信协议对象与 Hermes 的统一 `MessageEvent/SendResult` 双向转换。

## 一手来源

- [NousResearch/hermes-agent：Weixin 官方使用文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/weixin.md)
- [NousResearch/hermes-agent：WeixinAdapter 官方源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py)
- [NousResearch/hermes-agent：Gateway 配置源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/config.py)
- [NousResearch/hermes-agent：平台适配器架构文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/adding-platform-adapters.md)
- [NousResearch/hermes-agent v2026.4.13 发布说明](https://github.com/NousResearch/hermes-agent/releases/tag/v2026.4.13)
- [Tencent/openclaw-weixin：微信官方 OpenClaw channel 与 Backend API Protocol](https://github.com/Tencent/openclaw-weixin/blob/main/README.md#backend-api-protocol)

> 取证说明：本文只采用 NousResearch 官方仓库/文档与腾讯官方 `openclaw-weixin` 仓库公开协议，未用社区逆向文章来提升结论置信度。生产接入前应固定 Hermes release/commit，并对照部署版本重新核对默认策略和 API 行为。
