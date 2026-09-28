# 资源库存告警

后台「资源池 → 资源告警设置」（`/admin/resource-alerts`）支持 Xbox Child 和网易游戏账户资源池。可用库存为尚未绑定 Hub Access Key 的账户数量。

每个池单独设置启用状态、库存阈值、重复提醒间隔和 Telegram / Email 渠道，可以同时启用两种渠道。阈值由管理员填写，0 表示耗尽才通知；初始参考值为 3，间隔为 24 小时，规则默认关闭。间隔允许 1–720 小时。

## 配置通知

- Telegram：填写 Bot Token 和 Chat ID；先向 Bot 发起对话，或把 Bot 加入目标群组并授予发消息权限。
- Email：填写 SMTP 主机、端口、用户名、密码或授权码，以及单个发件邮箱和收件邮箱。支持 STARTTLS（常见端口 587）和直接 TLS（常见端口 465），要求有效 TLS 证书。用户名为空时不使用 SMTP AUTH；有用户名时使用 PLAIN over TLS。
- 保存后自动生效。Token 和 SMTP 密码保存于管理员数据库，不回传到页面；留空保留原值，填写新值可替换。关闭规则或取消渠道即可停用通知。

## 检查与重试

服务启动时及之后每分钟检查一次；可用库存小于等于阈值时发送。每个资源池、每个渠道独立记录通知状态，失败 5 分钟后重试，不影响已成功的其他渠道。检查到库存恢复后重置提醒周期；之后再次不足立即通知。页面可刷新库存、最近成功时间和发送失败状态。

数据库租约防止多个 Manager 实例同时发送同一通知。进程在渠道已接受通知、但尚未记录成功时中断，租约过期后可能重发；通知采用至少一次投递语义。配置修改不取消已经开始发送的通知；修改重复间隔不追溯调整已排定的下一次提醒。

## 部署

本功能新增两张表，按项目现有部署流程先执行迁移，再启动新版本：

```sh
./manager --config config.yaml migrate
./manager --config config.yaml start
```

新表为 `manager_resource_alert_config`、`manager_resource_alert_deliveries`。HTTP 页面和 API 使用现有管理员鉴权。配置接口为 `GET/PUT /v1/admin/resource-alerts`。发送过程设有超时，状态中不包含 Bot Token、SMTP 密码或服务器返回的敏感详情。
