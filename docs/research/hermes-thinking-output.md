# Hermes 思考过程输出设置

调研日期：2026-08-31。区分隐藏展示与关闭模型思考；未修改应用代码或线上 Agent。

## 当前 Hub / 本地镜像

- `hymatrix-module/start-hermes/run.go` 的 `configureHermes` 设置模型和审批选项，没有显式写入 `display.show_reasoning` 或 `agent.reasoning_effort`。[本地代码](../../hymatrix-module/start-hermes/run.go)
- 主调查任务只读检查本地 `sandytest456/hermes-agent:linux-full` 镜像：`hermes_cli/config.py:1161` 默认 `show_reasoning: False`；`gateway/run.py:2930` 的显示回退也是 false。`gateway/run.py:8885-8910` 根据平台显示配置决定是否在回复前添加 `last_reasoning`。
- 该镜像 `gateway/run.py:2838` 的 reasoning effort 默认 medium，支持 none/minimal/low/medium/high/xhigh。因此“默认不展示”并不等于“禁用模型思考”。本地镜像不代表线上已有容器的最终配置；会话命令、平台覆盖、镜像版本和手动更改均可能影响结果。

## 隐藏思考过程

官方 Gateway 实现先解析平台覆盖，再根据 `show_reasoning` 决定是否把 `last_reasoning` 拼接进最终回复；因此应显式设为 false。[Gateway 源码](https://github.com/NousResearch/hermes-agent/blob/main/gateway/run.py)

```yaml
display:
  show_reasoning: false
  platforms:
    weixin:
      show_reasoning: false
```

容器内可执行以下配置命令；可作为现有 Eval 的脚本，无需编译 Module：

```bash
hermes config set display.show_reasoning false
hermes config set display.platforms.weixin.show_reasoning false
hermes gateway restart
```

配置文件通常为 `~/.hermes/config.yaml`。`/reasoning hide` 是交互入口；`agent.reasoning_effort` 是不同的开关。若还要隐藏工具进度与中途解释，可以另外设置 `display.tool_progress: 'off'` 和 `display.interim_assistant_messages: false`，它们不是模型思考内容。[官方配置说明](https://hermes-agent.nousresearch.com/docs/user-guide/configuration/)

## 关闭模型思考生成

```bash
hermes config set agent.reasoning_effort none
hermes gateway restart
```

这表达禁用思考的意图，最终是否生效取决于模型、供应商和 Hermes 适配版本；它不是隐藏展示设置的替代品。当前官方文档也有 per-model `agent.reasoning_overrides` 与会话优先级，检查时不能只看全局项。[官方配置说明](https://hermes-agent.nousresearch.com/docs/user-guide/configuration/)

DeepSeek 当前官方 Chat Completions 接口使用 `extra_body={"thinking":{"type":"disabled"}}`；当前文档的 V4 模型默认启用思考，不能由历史 `deepseek-chat` 名称推断当前响应一定无思考。[DeepSeek Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/)

当前 Hermes 的原生 DeepSeek profile 对支持的 V4+ 型号将禁用配置映射为 `thinking.type=disabled`；未知/V3 型号不做该映射。用户日志是 `provider=custom`，不能直接保证其旧镜像走此原生 profile；关闭生成要进一步核对该版本 custom provider 的实际请求构造。[DeepSeek profile 源码](https://github.com/NousResearch/hermes-agent/blob/main/plugins/model-providers/deepseek/__init__.py)

## 版本注意与验证

本次查到的上游 main 已将默认 `display.show_reasoning` 改为 true，而用户本地镜像为 false。务必显式设置，而不是依赖默认值。[上游默认配置](https://github.com/NousResearch/hermes-agent/blob/main/hermes_cli/config_defaults.py)

在目标容器中检查相关配置而非打印整个 `.env`（其中有密钥）：

```bash
hermes config get display.show_reasoning
hermes config get display.platforms.weixin.show_reasoning
hermes config get agent.reasoning_effort
```

如果安装版本没有 `config get`，仅查看 `config.yaml` 的相关字段。实际微信会话可用 `/reasoning` 检查状态。隐藏选项控制 Hermes 识别的 reasoning 字段；模型直接写在普通答案里的解释，不应一概视为被该开关保证过滤。
