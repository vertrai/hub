# Hymatrix Module 生成教程

本文说明如何从 Hub 仓库中的源码生成可供 Hymatrix/VMDocker 使用的 Module。

## 快速构建

线上 Hymatrix 节点通常是 Linux AMD64。确认 Docker Desktop 已启动，并把
Linux AMD64 版本的 `vmdocker-agent` 放到约定位置后，在 Hub 根目录执行：

```sh
chmod 0755 ./hymatrix-module/tools/vmdocker-agent
./hymatrix-module/scripts/build-module.sh amd64
```

脚本会自动完成：

1. 生成仅用于本次构建的临时 Module 签名私钥；
2. 获取项目本地的 VMDocker v2 构建引擎；
3. 测试并编译 Linux AMD64 `start-hermes`；
4. 校验 `start-hermes` 和 `vmdocker-agent` 的平台与架构；
5. 构建 Module 镜像并验证最终镜像架构；
6. 创建本地测试镜像别名 `vmdocker-module:latest`；
7. 将可上传的 `mod-<MODULE_ID>.json` 保存到 `hymatrix-module/build/`。

成功后重点使用两个结果：

```text
hymatrix-module/build/mod-<MODULE_ID>.json  # 上传到 Hymatrix 节点
vmdocker-module:latest                     # 仅供本地 Docker 测试
```

不要把 Docker 镜像导出文件误当成 `.mod` 产物上传。Module JSON 内包含节点获取
和还原 Module 镜像所需的信息。

## 1. 目录与产物

相关目录如下：

```text
hymatrix-module/
├── start-hermes/          # Go 启动程序、四个 Skills、browser-harness
├── module/
│   ├── profile.toml       # Module 构建配置
│   └── bin/start-hermes   # build.sh 生成，不提交 Git
├── tools/
│   ├── vmdocker-agent     # 本地平台 Adapter，Git 忽略
│   └── vmdockerv2/        # hype 自动准备的构建引擎，Git 忽略
└── scripts/
    ├── build.sh           # 编译 start-hermes
    └── build-module.sh    # 生成 Module
```

生成过程分为两步：

1. 将 `start-hermes` 编译成与基础镜像架构一致的 Linux 二进制。
2. 使用 `hype vmdocker module build` 将基础镜像、二进制和 Profile 打包并发布为 Module。

## 2. 准备环境

需要安装：

- Docker，并确保 Docker daemon 已启动；
- Go 1.25 或兼容版本；
- `hype` CLI；
- 已编译的 `vmdocker-agent`；
- 用于签署 Module 交易的私钥。

本教程假设：

```text
Hub 仓库：         /path/to/hub
vmdocker-agent：   /path/to/vmdocker-agent
```

VMDocker v2 不需要指向其他项目。首次生成 Module 时，脚本会自动准备在：

```text
/path/to/hub/hymatrix-module/tools/vmdockerv2
```

## 3. 检查 Profile

构建配置位于 `hymatrix-module/module/profile.toml`：

```toml
[dockerfile]
FROM = "sandytest456/hermes-agent:linux-full"
bin = "bin"
CMD = ["start-hermes"]
```

- `FROM` 是 Module 使用的 Hermes 基础镜像；
- `bin` 表示把 `module/bin/` 放入 Module；
- `CMD` 让容器创建后自动运行 `start-hermes`。

如果更换基础镜像，需要确保镜像包含 Hermes、Python 和 `uv`。

## 4. 编译 start-hermes

进入 Hub 仓库根目录：

```sh
cd /path/to/hub
```

让脚本自动读取基础镜像架构并编译：

```sh
./hymatrix-module/scripts/build.sh
```

也可以显式指定架构：

```sh
./hymatrix-module/scripts/build.sh arm64
./hymatrix-module/scripts/build.sh amd64
```

脚本会依次执行：

- `go test ./...`；
- `go vet ./...`；
- Linux 静态交叉编译；
- `go version -m` 校验产物；
- 将最终文件写入 `hymatrix-module/module/bin/start-hermes`。

验证二进制：

```sh
file ./hymatrix-module/module/bin/start-hermes
```

输出应为 Linux ELF，并且架构与基础镜像一致。例如：

```text
ELF 64-bit LSB executable, ARM aarch64, statically linked
```

## 5. 准备 vmdocker-agent

`vmdocker-agent` 是平台 Adapter，VMDocker 构建时会把它注入镜像的
`/usr/local/bin/vmdocker-agent`。不要把它放进 `module/bin/`；该目录只存放
`start-hermes` 等业务程序。

本项目约定的本地位置是：

```text
hymatrix-module/tools/vmdocker-agent
```

当前开发机可执行：

```sh
cp /Users/sandyzhou/GolandProjects/vmdockerv2_agent/build/vmdocker-agent \
  ./hymatrix-module/tools/vmdocker-agent
chmod 0755 ./hymatrix-module/tools/vmdocker-agent
```

该文件已被 `.gitignore` 忽略，因为它体积较大、与目标架构相关，并且可从
`vmdockerv2_agent` 重新构建。

检查其架构：

```sh
file ./hymatrix-module/tools/vmdocker-agent
```

它必须是 Linux 可执行文件，并且与 `profile.toml` 基础镜像架构一致。

## 6. Module 签名私钥

通常不需要手动准备。`build-module.sh` 在没有设置
`VMDOCKER_PRIVATE_KEY` 时，会通过 `openssl rand` 从系统加密随机源生成一个
32 字节的一次性私钥。它仅保留在脚本进程内，不会打印或写入文件。

直接运行即可（参数必须与部署节点的 CPU 架构一致）：

```sh
./hymatrix-module/scripts/build-module.sh amd64
```

如果构建和后续管理必须使用固定签名身份，也可以显式传入私钥：

```sh
export VMDOCKER_PRIVATE_KEY="<your-private-key>"
```

显式提供的私钥优先于自动生成值。不要提交私钥、Gateway API Key、LLM API
Key 或 Telegram Bot Token。

## 7. 生成 Module

推荐使用包装脚本；必须指定目标架构，脚本默认会自动生成一次性签名私钥：

```sh
./hymatrix-module/scripts/build-module.sh amd64
```

可选值为 `amd64` 和 `arm64`。该参数会同时用于编译 `start-hermes`、校验
`vmdocker-agent`、选择 Docker 基础镜像平台以及验证最终 Module 镜像。目标
基础镜像必须提供对应平台，否则构建会明确失败。

脚本默认使用：

```text
VMDocker 工作区：hymatrix-module/tools/vmdockerv2
Agent 二进制：   hymatrix-module/tools/vmdocker-agent
Profile：        hymatrix-module/module/profile.toml
```

如果 VMDocker 工作区不存在，脚本会先执行：

```sh
hype vmdocker get --dir ./hymatrix-module/tools/vmdockerv2
```

`get` 只负责准备构建所需的 checkout。`hype vmdocker init` 会进一步启动本地
VMDocker 服务并运行 examples 初始化，单纯构建 Module 不需要执行它。

如果 Git 配置了当前不可用的代理，或者当前 `hype` 版本下载失败但错误退出码不
可靠，脚本会检查最终是否存在 `go.mod`。若 checkout 不完整，会将半成品移开，
再通过 `curl --noproxy '*'` 下载 GitHub 源码归档。该操作不会读取或修改全局
Git 代理配置。若归档直连也不可用，脚本会停止并提示检查网络。
若之前失败留下仅含 `.git` 的不完整 checkout，脚本会先备份该目录，重新获取
成功后再清理备份；失败时会恢复原目录。

如需复用已有 checkout，也可以覆盖路径：

```sh
VMDOCKER_WORKSPACE_DIR=/path/to/vmdockerv2 \
VMDOCKER_AGENT_BIN=/path/to/vmdocker-agent \
./hymatrix-module/scripts/build-module.sh amd64
```

等价的完整命令是：

执行：

```sh
hype vmdocker module build \
  --dir /path/to/hub/hymatrix-module/tools/vmdockerv2 \
  --profile /path/to/hub/hymatrix-module/module/profile.toml \
  --agent-bin /path/to/vmdocker-agent \
  --private-key "$VMDOCKER_PRIVATE_KEY"
```

参数含义：

- `--dir`：VMDocker v2 源码目录；
- `--profile`：本项目的 Module Profile；
- `--agent-bin`：与目标运行环境匹配的 `vmdocker-agent`；
- `--private-key`：签署和发布 Module 的私钥。

`--dir` 指向 `vmdockerv2` checkout，是因为当前 `hype vmdocker module build`
把它作为 Module 构建引擎：由其中的代码读取 Profile、准备 Docker 构建上下文、
把 `vmdocker-agent` 注入为镜像 ENTRYPOINT，并构建和发布 Module。它不会被打包
进最终 Module，也不是容器运行时依赖。

Profile 会在镜像构建阶段预装并校验 Telegram adapter 所需的
`python-telegram-bot`，以及 Hermes HTTP API Server 所需的 `aiohttp`。不要把这些
依赖推迟到容器首次启动时安装，否则 Hermes 可能在安装完成前就判定对应 adapter
不可用，必须重启 Gateway 才能恢复。

`build-module.sh` 成功后会将本次生成的 `mod-<MODULE_ID>.json` 从 Hype 内部的
临时输出位置归档到：

```text
hymatrix-module/build/
```

可以通过 `VMDOCKER_BUILD_DIR` 指定其他输出目录：

```sh
VMDOCKER_BUILD_DIR=/path/to/output ./hymatrix-module/scripts/build-module.sh amd64
```

命令同时会输出 Module ID。把该 ID 填入 Hub 管理后台 Hymatrix 页面中的
`Module` 字段。`build/` 中的产物体积较大，已被 Git 忽略。

VMDocker 内部仍使用 Dockerfile 内容哈希生成镜像 tag，便于追踪实际构建内容。
脚本会在构建成功后额外创建固定的本地测试别名：

```text
vmdocker-module:latest
```

因此日常本地测试无需查询每次变化的哈希 tag，可直接运行
`docker run ... vmdocker-module:latest`。`latest` 只是额外别名，不改变 Module
JSON 中记录的内容哈希镜像信息。

## 8. VMDocker 运行原理

Module 构建与 Pod 运行是两个阶段：

```text
构建阶段
profile.toml + 基础镜像 + vmdocker-agent + start-hermes
                         │
                         ▼
             Module 镜像 + mod-<MODULE_ID>.json

运行阶段
前端填写参数 → Hub Manager 组装并签名 Spawn 交易
                         │
                         ▼
Hymatrix 节点根据 Module ID 创建 Docker 容器并注入 Container-Env-* Tags
                         │
                         ▼
vmdocker-agent（容器入口和运行时适配器，监听容器内 8080）
                         │ 启动 Module CMD
                         ▼
start-hermes（安装 Skills、写配置、安装插件）
                         │ exec
                         ▼
hermes gateway run（Telegram + HTTP API Server，HTTP 默认监听 8642）
```

各组件职责：

- **Hymatrix 节点**：接收 Spawn 交易、根据 Scheduler 调度并创建容器；
- **VMDocker 构建引擎**：读取 `profile.toml`，构建镜像并生成 Module JSON；它不在
  Pod 中常驻运行；
- **`vmdocker-agent`**：被构建器注入镜像并成为容器入口，负责 VMDocker runtime
  协议、启动 Module CMD、暴露 `/vmm/health`；
- **`start-hermes`**：本项目编译的 Module CMD，准备 Hermes 所需的 Skills、环境、
  browser-harness 和 Telegram 自动 Home Channel 插件，最后以前台进程替换方式
  启动 Hermes Gateway；
- **Hermes Gateway**：真正处理 Telegram 消息、Skills 调用和 Hermes HTTP API。

节点访问的 runtime health URL 不是 Spawn 中配置的外部 URL。节点调用映射后的
`vmdocker-agent /vmm/health`；Hermes launcher 的 readiness 随后会继续请求容器内：

```text
http://127.0.0.1:8642/health
```

因此看到 `vmdocker-agent` 的 8080 端口已监听，并不代表 Pod 已完全就绪。必须同时
满足：Hermes Gateway 进程已启动、`API_SERVER_ENABLED=true`、API Server 依赖已
安装且 8642 端口开始监听。Module CMD 退出、架构不匹配或 Hermes API Server 未
启动，都会使节点持续得到 503/connection refused。

Spawn 成功只表示节点接受并开始创建 Pod。运行时就绪应以 `/vmm/health` 最终返回
成功为准。由于 `profile.toml` 已设置 `CMD = ["start-hermes"]`，不需要再发送 Start
交易。

## 9. Spawn 时注入运行配置

Module 本身不保存用户密钥。Hub Manager 会在 Spawn 交易中使用
`Container-Env-*` Tags 注入：

```text
RUNTIME_TYPE
HERMES_AGENT_LLM_PROVIDER
HERMES_AGENT_LLM_MODEL
HERMES_AGENT_LLM_BASE_URL
HERMES_AGENT_LLM_API_KEY
HUB_GATEWAY_URL
HUB_GATEWAY_API_KEY
HERMES_AGENT_TELEGRAM_BOT_TOKEN（可选）
API_SERVER_ENABLED=true
API_SERVER_KEY
HERMES_GATEWAY_TOKEN（与 API_SERVER_KEY 相同）
```

容器启动后，`start-hermes` 会：

1. 安装内嵌的四个 Gateway Skills；
2. 安装并验证内嵌的 `browser-harness`；
3. 写入 Hermes 环境和 LLM 配置；
4. 有 Telegram Token 时安装自动 Home Channel 插件，首条私聊自动成为 Home；
5. 前台执行 `hermes gateway run`。

无需再发送单独的 Start 交易。

> 安全提示：Manager 将 API Key、Bot Token 等敏感值通过 Hymatrix SDK encrypted tags 发送；VMM 解密后注入容器。不要把敏感值放入普通 `Container-Env-*` Tags，也不要在日志或交易预览中显示明文。

## 10. 修改 Skills 或 start-hermes 后重新生成

Skills 的唯一源码目录是：

```text
hymatrix-module/start-hermes/skills/
```

修改 Skill 后必须重新执行：

```sh
./hymatrix-module/scripts/build-module.sh amd64
```

Skills 通过 Go `embed` 编译进 `start-hermes`，只修改源码但不重新编译不会更新已有 Module。

## 11. 常见问题

### Docker 镜像架构无法识别

先拉取基础镜像：

```sh
docker pull sandytest456/hermes-agent:linux-full
```

或者显式向构建脚本传入 `arm64` 或 `amd64`。

### 下载 VMDocker 时连接本机代理失败

如果错误中出现类似：

```text
Failed to connect to 127.0.0.1 port 7890
```

说明 Git 配置了本机代理，但代理进程没有运行。构建脚本会检测 `hype get` 是否
真正生成了 checkout，并自动尝试一次绕过代理的 GitHub 源码归档下载。也可以
启动代理，或者自行检查：

```sh
git config --global --get http.proxy
git config --global --get https.proxy
```

### 无法连接 Docker API

如果错误包含：

```text
failed to connect to the docker API
```

说明 Docker CLI 已安装，但 Docker daemon 尚未运行或当前 context 无法访问它。
在 macOS 上启动 Docker Desktop，等待状态显示为 Running，然后确认：

```sh
docker info
docker context show
```

`docker info` 能同时显示 Client 和 Server 信息后，再重新执行构建脚本。脚本会在
正式构建前检查 daemon，避免进入 VMDocker 构建后才输出较长的错误堆栈。

首次构建时出现 `public entry ... matched no files` 只是提示对应持久化目录尚未在
容器中生成，不是本次构建失败的原因。

### Module 启动后提示找不到 Hermes

确认 `profile.toml` 的基础镜像包含 `hermes` 命令，且命令位于 `PATH`、
`~/.local/bin/hermes`、`~/.hermes/hermes-agent/venv/bin/hermes` 或
`/usr/local/bin/hermes`。

### start-hermes 提示缺少 Gateway 配置

确认 Spawn Tags 中同时存在：

```text
Container-Env-HUB_GATEWAY_URL
Container-Env-HUB_GATEWAY_API_KEY
```

这两个变量必须成对提供，不能只设置其中一个。

### 修改代码后 Module 行为没有变化

确认已重新运行 `build.sh`，并使用新的构建产物生成了新 Module。旧 Module ID
仍指向旧版本，不会自动更新。

### vmdocker-agent health 正常启动但一直返回 503

先查看 Pod 容器日志，并检查两个监听端口：

```sh
docker logs <pod-container>
docker exec <pod-container> sh -lc 'ss -lntp'
```

容器内 8080 是 `vmdocker-agent`；8642 是 Hermes HTTP API Server。如果只有 8080，
检查 Spawn 是否注入 `API_SERVER_ENABLED=true`、`API_SERVER_KEY` 和相同值的
`HERMES_GATEWAY_TOKEN`，并确认构建日志中成功安装和导入 `aiohttp`。
