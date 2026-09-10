# Xbox Child 账户

管理员在 /admin/xbox-child 输入子账户邮箱、密码和家长邮箱。数据独立保存在 Manager 数据库的 manager_xbox_children 表；Manager 启动时自动迁移。邮箱不区分大小写且不可重复，不进行 Xbox 注册或登录。

管理接口使用现有管理员会话：
- POST /v1/admin/xbox/children：提交 {"email":"child@example.com","password":"secret","parentEmail":"parent@example.com"}，成功返回 201，重复邮箱返回 409，字段无效返回 400。
- GET /v1/admin/xbox/children：返回 items，包含邮箱、家长邮箱、hubAccessKeyId、usedAt 和时间戳，以及供管理员查看的 password。hubAccessKeyId 非空表示已使用。

客户端调用 Manager：
```sh
curl -X POST https://MANAGER_HOST/v1/xbox-child \
  -H "Authorization: Bearer $HUB_API_KEY"
```

成功返回：
```json
{"email":"child@example.com","password":"secret"}
```

也支持 GET /v1/xbox-child，以及 X-Gateway-API-Key 请求头。每次请求均通过 Resources /v1/access-key 验证 key 有效且 active。无需额外资源 scope。

首次领取永久绑定该 Hub Access Key，记录 usedAt；相同 key 重复领取返回相同账户。其他 key 无法领取已绑定账户。分配通过带未使用条件的原子数据库更新和 key 唯一索引保护，支持多个 Manager 实例并发。没有可用账户时返回 409；无效 key 返回 401/403；验证或分配服务暂不可用返回 503，客户端可重试。领取响应禁止缓存。客户端不获得家长邮箱。

管理员可在列表查看密码、修改密码和删除账户：
- PATCH /v1/admin/xbox/children/:id：提交 {"password":"new-secret"}，仅更新 Manager 保存的密码，不修改 Xbox 网站密码。保留现有绑定，所属 key 下次请求返回新密码。
- DELETE /v1/admin/xbox/children/:id：永久删除账户记录及绑定。原 key 再次请求会尝试领取其他可用账户；无库存返回 409。
- 两个接口均需要管理员会话，成功返回 200，账户不存在返回 404；空白密码返回 400。

数据库保存原始密码以便后续领取；本资源不提供解绑或重置使用状态的接口。
