# 网易游戏账号资源池

管理员页面：`/admin/netease-accounts`。手动录入已存在的网易登录账号（邮箱等）和密码，
不自动注册或登录网易。页面提供列表、未使用/已使用状态、绑定 Hub Key ID、领取时间、改密和删除。

账号保存在 Manager 数据库 `manager_netease_accounts`，与 Xbox Child 表独立。
部署时显式执行 `go run ./cmd/manager --config ./cmd/manager/config.yaml migrate`；启动不会自动迁移。
存储方案沿用 Xbox Child：密码作为私有数据库字段保存，不使用单向哈希，因为客户端登录需要原密码。
请沿用现有数据库访问控制和备份保护。账号密码写入禁止输出 SQL 参数；管理员列表返回密码供后台直接显示，创建响应不包含密码。

## 管理员接口

使用现有 Hub 管理员认证：

- `POST /v1/admin/netease/accounts`：`{"username":"bot@example.com","password":"your-password"}`。
  账号去除首尾空白并转小写，密码保留原始内容；空账号/空白密码返回 400，重复账号 409，成功 201。
- `GET /v1/admin/netease/accounts`：返回 `items`，含 id、username、password、hubAccessKeyId、usedAt 和时间戳。
- `PATCH /v1/admin/netease/accounts/:id`：`{"password":"new-password"}`。仅更新 Hub 保存的密码，
  不改变网易网站密码，不释放绑定。账号所属 key 下次请求取得新密码。
- `DELETE /v1/admin/netease/accounts/:id`：删除账号和绑定；原 key 下次请求可领取另一账号。

所有接口返回 `Cache-Control: no-store`。缺失记录的 PATCH/DELETE 返回 404。

## Agent 领取接口

`GET /v1/netease-account`（也支持 POST），使用 `Authorization: Bearer <Hub API Key>`
或 `X-Gateway-API-Key`。每次请求都验证 key 的有效性和 active 状态，无需额外 scope。

成功响应：`{"id":"nea_...","username":"bot@example.com","password":"..."}`。
只向已认证的所属 key 返回其账号，不返回整个池或其他账号。

同一 key 永久绑定一个账号，重复请求幂等；不同 key 独占不同账号。
使用数据库条件 UPDATE 和唯一索引保护并发分配。无库存返回 409，暂时不可用 503，失效 key 401/403。

MicAI Agent 不应打印此响应。它通过私有临时凭证文件调用
`micai-netease-native login --credentials <path>`，登录结束删除凭证文件。
账号池增删改不会推送终止已建立的游戏连接；运行中的 Agent 需 stop 后重新 login 领取当前绑定。

资源预检查接口及统一耗尽错误码见 [资源可用状态](resource-availability.md)。
