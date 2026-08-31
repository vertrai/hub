# Hermes 运行中通过 Eval 重置微信绑定：机制与安全设计

## 结论

可以用 Eval **触发**运行中的 Hermes 切换微信绑定，但不应把新的 `bot_token` 放在 Eval 的消息正文、命令行或 Hymx encrypted tag 中。

推荐方案是：管理员在 Hub 完成新的二维码授权并把新 `WeixinBot` 关联到目标 Pod；容器内的 Eval 只执行一个固定、不含秘密的更新程序。该程序从本机 `~/.hermes/.env` 读取现有 `HUB_GATEWAY_API_KEY`，经 HTTPS 向 Hub 的 Pod 专属、一次性领取接口取回新凭据，在容器内原子更新 `~/.hermes/.env`，最后重启 Hermes Gateway。这样 Hymx 交易和 Node debug 日志都不出现微信 token。

不要直接下发如下命令：

```sh
printf 'WEIXIN_TOKEN=明文...' >> ~/.hermes/.env
```

也不要认为把整条命令改成 encrypted param 就足够安全：Hymx 会在 VM 执行前解密参数，当前 vmdockerv2 的 debug 日志又会记录传给 `/vmm/apply` 的完整 JSON。

## 现有实现链路

### 1. Hub 已经拥有二维码授权和凭据存储能力

Manager 调用 iLink QR 接口并轮询；确认后读取 `ilink_bot_id`、`bot_token`、`baseurl`、`ilink_user_id`，校验 base URL 后写入 `manager_weixin_bots`。[manager/weixin.go](/Users/sandyzhou/codex-project/hub/manager/weixin.go:191) [manager/weixin.go](/Users/sandyzhou/codex-project/hub/manager/weixin.go:212)

`WeixinBot.Token` 不会出现在 JSON 响应中，但数据库列本身是普通 text；它不是应用层加密字段。[manager/schema/db.go](/Users/sandyzhou/codex-project/hub/manager/schema/db.go:81)

首次 Start-Agent 时，Hub 把微信 account ID、token、base URL 和 allowlist 都作为 SDK encrypted params 发送。[manager/hymatrix.go](/Users/sandyzhou/codex-project/hub/manager/hymatrix.go:133)

### 2. start-hermes 如何落盘和启动

`start-hermes` 将 `HERMES_AGENT_WEIXIN_*` 映射为 Hermes 原生的 `WEIXIN_*`，要求 account ID、token、base URL、allowed users 四项完整，并把 `.env` 通过临时文件 + rename 原子替换为 `0600`。[env.go](/Users/sandyzhou/codex-project/hub/hymatrix-module/start-hermes/env.go:29) [env.go](/Users/sandyzhou/codex-project/hub/hymatrix-module/start-hermes/env.go:73)

随后 `start-hermes` 用 `syscall.Exec` 运行 `hermes gateway run -q --replace --accept-hooks`；因此 start-hermes 本身不再保留，实际 Gateway 是它的替代进程。[run.go](/Users/sandyzhou/codex-project/hub/hymatrix-module/start-hermes/run.go:17)

### 3. Hermes 如何选取微信凭据

官方 `WeixinAdapter` 在构造时一次性读取 account ID、token、base URL 和 allowlist；它不会热更新这些字段。因而仅修改 `.env` 不会改变正在运行的 adapter，必须重启 Gateway。[官方 WeixinAdapter 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L1184-L1234)

二维码登录得到的凭据也可保存到 `~/.hermes/weixin/accounts/<account_id>.json`，文件权限尝试设为 `0600`。[官方凭据存储源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/weixin.py#L254-L295)

本项目首次启动明确写入 `WEIXIN_TOKEN`，它是最清晰的更新源。切换 account ID 后，旧账号的 `.json`、`.sync.json` 和 `.context-tokens.json` 不会被新 account ID 使用；可在成功切换后清理，但不应在切换前删除，以便失败回滚。

Hermes 官方建议 session 失效时重新运行 `hermes gateway setup` 扫码，并说明凭据会保存到上述 accounts 目录；但该 wizard 是交互式终端流程，不适合当前异步、无 stdout/stderr 的 Eval。[官方 Weixin 文档](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/messaging/weixin.md)

### 4. 当前 Eval 的能力和保密边界

Hub 当前 Eval 把整条命令作为 Hymx message data 发送，只有 `Action=Eval` tag；没有 encrypted params。[manager/hymatrix.go](/Users/sandyzhou/codex-project/hub/manager/hymatrix.go:103)

Runtime 取 `params["Data"]` 或解码 `meta.Data`，执行 `sh -lc`，异步返回 `status=started`，不等待退出，也不采集 stdout/stderr。[hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:118) [hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:132) [hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:154)

Hymx v0.5.0 的 encrypted tag 会在 `Vm.Apply` 前解密成去掉 `Encrypted-` 前缀的明文参数；原密文仍保留。[Hymx crypto-tags 文档](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/docs/crypto-tags.md:135) [Hymx apply 实现](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/vmm/apply.go:54)

vmdockerv2 随后把 `meta.Params` 原样作为 Apply request，并在 debug 日志打印整个 JSON。[vmdocker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/vmdocker.go:248) [vmdocker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/vmdocker.go:658)

因此 encrypted param 能避免 token 出现在公开交易正文中，但在当前日志实现下不能避免 token 出现在 Hymx Node 日志中。

## 推荐的后台交互与后端协议

### 管理页面

在目标运行中 Hermes Pod 的操作中增加“重置微信连接”：

1. 显示当前关联的微信 account ID（脱敏），不返回 token。
2. 点击后复用现有二维码 onboarding 页面。
3. 扫码确认后显示新 account ID / allowed user，要求二次确认“替换并重启”。
4. 提交后显示阶段：`authorized → assigned → delivered → restarting → healthy`；失败时保留旧绑定并允许重试。

不复用当前 `/credentials` 响应把 dotenv 返回浏览器；重置流程中浏览器不需要看到 token。

### Hub 到容器的安全领取

增加一个专用 helper（例如 Module 内置 `reset-hermes-weixin`）及 Hub endpoint：

```text
POST /v1/agent/hymatrix/weixin/reset/claim
Authorization: Bearer <容器 ~/.hermes/.env 中现有 HUB_GATEWAY_API_KEY>
Content-Type: application/json

{"pid":"<目标 pid>","generation":"<非秘密版本号>"}
```

Hub 必须验证：

- Gateway access key 有效，并且确实属于目标 Pod / user；
- PID 与 key 的绑定一致，不能由请求者任意指定另一个 PID；
- 存在管理员刚确认、尚未领取的 reset generation；
- 新 bot 属于同一 user，状态可用，且未分配给其他 Pod；
- claim 只能成功一次，短 TTL，并在数据库事务中完成旧/new bot assignment 切换；
- 响应 `Cache-Control: no-store`，请求和响应日志必须彻底脱敏。

Eval 交易只需要发送固定命令，例如：

```sh
reset-hermes-weixin --pid "$HYMATRIX_PROCESS_ID" --generation '<非秘密版本号>' \
  > /home/hymx/weixin-reset-status.json 2>&1
```

如果容器目前没有可靠的 PID 环境变量，可将 PID 和 generation 作为非秘密参数传入。helper 从 `.env` 读取 Hub URL/Key，不把 key 放入 argv；HTTPS response 直接在进程内解析，不通过 shell 变量、stdout 或临时明文文件。

### 容器内更新顺序

helper 应执行：

1. 对 `~/.hermes/.env` 加独占锁；
2. 从 Hub 领取凭据并严格校验无换行/NUL、HTTPS base URL allowlist；
3. 备份当前 `.env` 到权限 `0600` 的单个回滚文件；
4. 以 `0600` 临时文件写入四项并 `fsync + rename`：
   `WEIXIN_ACCOUNT_ID`、`WEIXIN_TOKEN`、`WEIXIN_BASE_URL`、`WEIXIN_DM_POLICY=allowlist`、`WEIXIN_ALLOWED_USERS`；
5. 不立即删除旧 account 的 credential/sync/context 文件；
6. 调用 `hermes gateway restart`；
7. 轮询 `hermes gateway status` 或 runtime status，在新 Weixin adapter connected 后通知 Hub commit；超时则恢复旧 `.env` 并再重启一次；
8. 状态文件只写阶段、account ID 摘要、时间和错误类别，不写 token。

Hermes Gateway 的 `restart` 在无 systemd/launchd 时会停止当前 profile Gateway 后前台启动新 Gateway。[官方 gateway CLI 源码](https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/gateway.py#L8578-L8740) 当前 vmdockerv2 容器中 `vmdocker-agent` 是 runtime 控制进程，Gateway 是 Start-Agent 启动的子进程；因此该前台 replacement 可继续存活，但必须先在实际 Module 上验证进程树和重启后的 `/vmm/apply` 可用性。

## 测试阶段可接受的简化版

若当前目标只是受控环境验证，可先让 Manager 发送 `Data` 作为 encrypted param，而不是 message body，并调用一个固定 helper，避免 token 进入公开 message data。但上线前必须同时修改 vmdockerv2 的 apply debug logging：对 `Data`、`Encrypted-*` 以及所有已解密敏感参数做不可逆脱敏。否则 token 仍会落入 Node 日志。

即便简化，也不要把 token：

- 放在 URL query、shell argv 或 `printf` 命令中；
- 写进 `test-status.txt` 或返回到 Admin 页面；
- 记录在审计表的 request body / error 文本；
- 通过现有 Eval 页面让管理员手工粘贴。

## 必测场景

- 新绑定成功：旧微信不再消费，新微信收发正常；
- 新 token 无效 / iLink 限流：自动回滚旧 `.env` 并恢复旧连接；
- Gateway 正有一轮消息处理：重启 drain 行为不会重复回复或损坏 session；
- 重复点击：同一 generation 只领取/执行一次；
- Eval/Hymx recovery 重放：已消费 generation 返回幂等成功，不能再次切换或泄漏凭据；
- Pod、bot、user 不匹配：claim 拒绝；
- Node、Manager、Admin access log、数据库审计、status 文件均检索不到 token；
- 重启后 vmdocker runtime endpoint 仍在线，并可继续执行 Eval；
- 容器/节点重启后使用新绑定，且旧 binding 状态已正确释放。

## 最终建议

后台按钮可以使用 Eval 作为“远程触发器”，但真正的秘密分发应走容器到 Hub 的认证领取通道。若短期只做验证，至少采用 encrypted `Data` + 固定 helper + Node 日志脱敏；直接拼接 token 的任意 shell Eval 不应进入管理页面。
