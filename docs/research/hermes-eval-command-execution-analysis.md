# Hermes `Apply(Action=Eval)` 命令执行边界分析

日期：2026-08-31

## 结论

`vmdockerv2_agent/runtime/hermes` 的 `Eval` 是一个明确的**任意 shell 命令执行能力**：它将消息的 `Data` 字段未经白名单、拆词或转义，直接传给 `sh -lc <Data>`。因此，只要调用者能通过所有权检查，就能使用管道、重定向、命令替换、`;`、`&&` 等完整 shell 语法，执行容器中 `hymx` 用户能够执行的任意程序。

但“在运行的服务器上执行任何命令”需要准确区分：

- **可以**在该 process 对应的运行中 Hermes VM/Docker 容器内执行任意 shell 命令。
- **不能据现有代码直接等同于宿主机 RCE**。Docker 后端强制非 root 用户、只读 rootfs、丢弃全部 Linux capabilities、启用 `no-new-privileges`，且没有挂载 Docker socket。
- 容器仍有两个宿主机可写 bind mount（process workspace → `/home/hymx`，process tmp → `/tmp`）。因此命令可以修改这些宿主机目录中的持久化文件，也可以读取继承给 adapter 的环境变量、使用容器网络发起请求。若这些目录或网络中存在额外的逃逸条件/高权限凭证，影响才可能进一步扩大到宿主机或其他系统。

安全定级应视为：**已设计的容器内任意代码执行（高危管理能力），当前不是已证实的宿主机逃逸。**

## 执行链路

1. Hymx 节点接收签名 bundle，验证 bundle 签名并解码 signer、目标 PID、消息字段；随后才将消息交给目标 VM。来源：[node/handle.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/node/handle.go:16)。
2. 消息的 `Action` 和 item body 被分别复制到 VMM `Meta.Action` 与 `Meta.Data`。来源：[node/message.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/node/message.go:77)。
3. VMM 完成 nonce/sequence 检查，确定 `from` 后调用 VM 的 `Apply(from, meta)`。来源：[vmm/apply.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/vmm/apply.go:37)、[vmm/apply.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/vmm/apply.go:90)。
4. `vmdockerv2` 将 `from`、完整 `meta` 与 params JSON 编码后，请求该容器的 `/vmm/apply`。来源：[vmdocker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/vmdocker.go:248)、[vmdocker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/vmdocker.go:643)。
5. adapter 的 HTTP handler 仅做 JSON binding，然后调用 runtime；它自身没有 HTTP token/auth middleware。来源：[server/api.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/server/api.go:64)、[server/api.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/server/api.go:16)。
6. Hermes runtime 要求 `from == owner`，再根据 `Action == "Eval"` 调用 `eval(meta.Data)`。来源：[runtime/hermes/hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:95)。
7. `eval` 只拒绝空白命令，随后执行 `exec.Command("sh", "-lc", command).Start()`。来源：[runtime/hermes/hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:138)。

## 鉴权与可触发者

### 正常公网 Hymx 消息入口

- 公网提交接口本身不使用 bearer token，而是接收二进制 bundle；节点在业务处理前调用 `VerifyBundleItem` 校验签名。来源：[server/api.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/server/api.go:118)、[node/handle.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/node/handle.go:16)。
- Hermes 的 `owner` 来自 process spawn 环境中的 `env.Meta.AccId`。来源：[runtime/runtime.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/runtime.go:69)。
- 对普通直接消息，VMM 将 bundle signer 的 `AccId` 作为 `from`；Hermes 再严格比较 `from == owner`。来源：[vmm/apply.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/vmm/apply.go:100)、[runtime/hermes/hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:98)。因此正常链路下，持有 process owner 私钥并能构造 `Action=Eval` 消息的一方可以触发。
- `From-Process` 消息另有节点注册关系校验，且 VMM 会把 `from` 改为 `From-Process` 值；它仍必须最终等于 Hermes 保存的 owner 才能通过。来源：[node/handle.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/node/handle.go:84)、[vmm/apply.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/vmm/apply.go:101)。

### 容器 adapter 的直接 HTTP 入口

adapter 的 `/vmm/apply` **不校验签名或 token**，并信任 JSON 中的 `from`。它还允许任意 Origin 的 CORS。来源：[server/api.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/server/api.go:16)、[common/middleware.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/common/middleware.go:5)。所以任何能直接连到该 adapter 端口、且知道/猜到 owner 的主体都可以伪造 `from` 绕过 runtime 所有权比较。

Docker 后端将容器 8080 端口只发布到宿主机 `127.0.0.1`，降低了远程直接访问风险，但宿主机本地进程、SSRF、端口转发或错误的替代部署仍属于攻击面。来源：[env.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/runtimemanager/schema/env.go:5)、[docker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/runtimemanager/docker.go:285)。

## 命令能力、构造与限制

- **没有命令白名单或参数转义。** `meta.Data` 是 `sh -lc` 的单个命令字符串，shell 会正常解释所有元字符；这不是偶然的“命令注入”，而是接口本身提供的 shell eval。来源：[runtime/hermes/hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:138)。
- **异步且不返回输出。** 使用 `Start()` 而非 `Run()`/`Wait()`，stdout/stderr 均为 `nil`；HTTP 成功只表示 shell 进程成功启动，不表示命令执行成功。来源：[runtime/hermes/hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:142)。
- **没有执行超时、取消或子进程跟踪。** 创建独立 process group，但未保存 PID、未等待、也没有终止逻辑；长任务可继续运行，短任务还可能积累未 `Wait` 的子进程资源。
- **继承 adapter 环境与工作目录。** 代码没有覆盖 `Env` 或 `Dir`，因此 shell 继承 adapter 的环境（可能含 API/token 配置）并从容器默认工作目录 `/home/hymx` 启动。镜像工作目录来源：[dockerfile.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/modulebuild/dockerfile.go:50)。
- **命令受容器内可用工具约束。** “任意命令”指任意可由 `/bin/sh` 解析且容器内存在/可创建的程序，不代表能调用宿主机上任意二进制。

## 容器到宿主机的安全边界

Docker 容器配置提供了较强的基础隔离：

- 固定以非 root 的 `hymx` 用户运行。来源：[env.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/runtimemanager/schema/env.go:17)、[docker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/runtimemanager/docker.go:273)。
- root filesystem 只读、`no-new-privileges`、drop all capabilities、PID 上限 256、CPU/内存限额。来源：[docker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/runtimemanager/docker.go:285)。
- 构建镜像时移除 `hymx` 的 sudo/docker 组，并清除额外 sudoers。来源：[dockerfile.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/modulebuild/dockerfile.go:41)。
- 宿主 workspace 以读写方式挂载到 `/home/hymx`，宿主 tmp 目录以读写方式挂载到 `/tmp`；可选模型挂载是只读。来源：[docker.go](/Users/sandyzhou/GolandProjects/vmdockerv2/vmdocker/runtimemanager/docker.go:328)。

所以 Eval 可直接修改该 process 的持久化 workspace/tmp，并可影响后续 Hermes 启动、checkpoint 或恢复所使用的文件；但当前配置没有 `Privileged`、`CapAdd`、host PID/network、Docker socket 或宿主根目录挂载，源码不足以证明可以直接执行宿主机命令。

## 恢复/重放的额外风险

Hymx 恢复流程以 `ExecModeReplay` 重新应用历史消息，`HandleMode` 直接调用 `applyMessage`；Hermes `Apply` 没有按执行模式屏蔽 `Eval`。来源：[node/handle.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/node/handle.go:68)、[node/message.go](/Users/sandyzhou/go/pkg/mod/github.com/hymatrix/hymx@v0.5.0/node/message.go:50)、[runtime/hermes/hermes.go](/Users/sandyzhou/GolandProjects/vmdockerv2_agent/runtime/hermes/hermes.go:117)。

这意味着一个已持久化的 Eval 消息在 checkpoint 之后被重放时，**很可能再次启动同一命令**。非幂等命令可能重复修改文件、重复请求外部服务或重复启动后台进程。此结论来自代码路径推断，建议用无副作用的 mock/unit test 单独确认，不能在生产节点直接试验。

## 建议

1. 若产品不需要远程 shell，删除/禁用 `Eval`，这是唯一可靠的风险消除方式。
2. 若必须保留，把它定义为显式高权限 capability：节点侧验证 owner 后再加独立签名域/短期 nonce/审计策略，不只依赖通用消息 Action。
3. adapter `/vmm/apply` 增加节点到容器的强认证（每实例随机 token 或 mTLS），不要信任请求体中的 `from`；同时维持 loopback bind。
4. 禁止在 `Replay`/`DryRun` 模式执行外部副作用，或把 Eval 设计成带唯一执行 ID 的幂等操作。
5. 使用 `exec.CommandContext`、明确超时、捕获有限输出、调用 `Wait()`、记录 PID，并在 VM 停止时杀掉整个 process group。
6. 对容器网络实施最小出口策略，阻断云 metadata 等敏感地址；梳理传入 adapter 的环境变量，避免 shell 能读取长期高权限 secret。
7. 明确 workspace/tmp 的宿主权限和 checkpoint 内容；将不需要写入的路径改为只读，避免通过持久化脚本影响后续启动。

