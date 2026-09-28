# 资源可用状态与领取失败提示

## 用户端创建前检查

微信小程序、vertr.ai 网页等调用方自行决定要检查哪些资源，Hub 不推断 Agent 的资源依赖。

```http
GET /v1/resource-availability?resources=xbox-child,netease
```

公开只读接口，无需 Hub API Key 或管理员登录；只返回可用状态，不返回库存数量、账号、密码或绑定信息。

`resources` 为必填参数，目前支持：

| 参数值 | 资源池 |
| --- | --- |
| `xbox-child` | Xbox Child 游戏账户 |
| `netease` | 网易游戏账户 |

多个资源用逗号分隔，也可重复参数，如 `?resources=xbox-child&resources=netease`。重复资源去重，结果按首次出现顺序返回。缺失、空项或不支持的类型返回 HTTP 400、`code: INVALID_RESOURCES`，验证失败不会查询数据库。应用不依赖这些资源时无需调用。

正常查询返回 HTTP 200，即使其中有资源已耗尽：

```json
{
  "allAvailable": false,
  "checkedAt": "2026-09-28T08:00:00Z",
  "resources": [
    {"resource": "xbox-child", "status": "available"},
    {
      "resource": "netease",
      "status": "exhausted",
      "code": "RESOURCE_POOL_EXHAUSTED",
      "message": "网易游戏账户暂时用完，请稍后再试；如持续不可用，请联系官方补充资源。"
    }
  ]
}
```

仅直接查询指定资源表中的未分配记录（`hub_access_key_id IS NULL`），每种资源最多读取一个 ID，找到即停止。不做 COUNT、联表、外部服务调用或缓存维护。响应设置 `Cache-Control: no-store`。查询共享 2 秒上下文超时。

数据库异常或查询超时返回 HTTP 503：

```json
{
  "code": "RESOURCE_STATUS_UNAVAILABLE",
  "status": "unknown",
  "error": "暂时无法确认资源状态，请稍后重试。"
}
```

503 不是资源耗尽，不返回可能误导的 `allAvailable` 或部分结果。用户端可提示重试；200 时使用 `allAvailable` 决定是否阻断创建，并展示耗尽资源的 `message`。这次 Hub 改动不自动修改用户端应用或创建接口的行为。

## 运行中的 Agent 领取资源

`GET/POST /v1/xbox-child` 和 `GET/POST /v1/netease-account` 保留原有认证、分配机制与 HTTP 409 状态码，耗尽时统一返回：

```json
{
  "code": "RESOURCE_POOL_EXHAUSTED",
  "resource": "xbox-child",
  "error": "Xbox Child 游戏账户暂时用完，请稍后再试；如持续不可用，请联系官方补充资源。",
  "action": "wait_for_restock"
}
```

调用方根据 `code` 分支处理，保留 `error` 字符串供旧调用方和用户阅读。遇到 `wait_for_restock` 应告知用户等待补充或联系官方，不要立即循环重试。鉴权失败、领取服务异常仍与库存不足分开处理。

预检查不锁定或预留资源：并发创建时，检查通过后仍可能领不到账户，必须处理领取接口的业务错误。已经绑定资源的 Hub Key 继续返回原账户，不受公共池耗尽影响；运行中的 Agent 不应以公共池状态替代自己的领取接口。

接口复用现有资源表，无需为此新增数据库迁移。其他类型资源尚未接入本接口。
