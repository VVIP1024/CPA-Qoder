# CPA-Qoder

CLIProxyAPI v8 原生插件，把 Qoder 国际版和 QoderCN 变成 OpenAI 兼容模型。已经在用 `qoder` / `qodercn` CLI 的账号，可以直接通过 Chat Completions 调用。

插件每个请求启动一次 CLI。国际版模型 ID 为 `qoder/<模型>`，国内版为 `qodercn/<模型>`。回答由 CLI 产生，插件只负责消息转换、流式输出和函数调用交接。CLI 自带的 Agent 能力不参与，工具调用交回给客户端执行。

[English](README.md)

## 前置条件

- 带原生插件支持的 CLIProxyAPI v8（C ABI 1、RPC schema 6），按 v8.0.0 构建
- 已安装并登录的 Qoder CLI（`qoder login`、`qodercn login`）
- Linux `amd64`/`arm64` 或 macOS；从源码构建需要 Go 1.26 和 C 编译器

插件不创建账号、不附带凭证，也不绕过套餐额度。

## 安装

从 Releases 下载对应架构的压缩包，解压后把插件、helper 和认证描述放到 CPA 目录：

```sh
tar -xzf cpa-qoder-linux-amd64.tar.gz
cp cpa-qoder-linux-amd64/plugins/cpa-qoder.so /path/to/cpa/plugins/
cp cpa-qoder-linux-amd64/bin/cpa-qoder-helper /path/to/cpa/plugin-bin/
chmod 755 /path/to/cpa/plugin-bin/cpa-qoder-helper
cp cpa-qoder-linux-amd64/auths/*.json /path/to/cpa/auths/
```

Docker 部署时，把 `/root/.qoder` 和 `/root/.qoder-cn` 放在持久化目录里，CLI 的登录状态保存在这两个目录。

把下面的配置合并进现有的 `config.yaml`，不要覆盖 CPA 的端口、密钥和其他插件：

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
  configs:
    cpa-qoder:
      enabled: true
      priority: 1
      helper: "/CLIProxyAPI/plugin-bin/cpa-qoder-helper"
      proxy-url: ""
      queue-wait: "60s"
      global:
        cli: "/usr/local/bin/qoder"
        config-dir: "/root/.qoder"
        disabled: false
      cn:
        cli: "/usr/local/bin/qodercn"
        config-dir: "/root/.qoder-cn"
        disabled: false
```

只有一套账号时，删除另一份认证描述，并把对应段落设为 `disabled: true`。

首次登录在容器内执行：

```sh
docker exec -it <cpa-container> qoder login
docker exec -it <cpa-container> qodercn login
```

重启 CPA 后查询 `/v1/models` 确认模型已经注册。

## 配置项

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `global.cli` | `qoder` | 国际版 CLI |
| `global.config-dir` | `~/.qoder` | 国际版登录与配置目录 |
| `global.disabled` | `false` | 停用国际版账号 |
| `cn.cli` | `qodercn` | 国内版 CLI |
| `cn.config-dir` | `~/.qoder-cn` | 国内版登录与配置目录 |
| `cn.disabled` | `false` | 停用国内版账号 |
| `accounts` | 空 | 额外账号，需各自独立的配置目录 |
| `helper` | 从 `PATH` 查找 | 函数调用所需的 schema helper |
| `proxy-url` | 空 | 模型发现和推理的代理；留空继承 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY` |
| `queue-wait` | `60s` | 上游排队时最多等待多久；`0` 表示一直等 |

额外账号示例：

```yaml
      accounts:
        - id: work
          variant: global
          cli: "/usr/local/bin/qoder"
          config-dir: "/root/.qoder-work"
```

对应的认证描述需要写入 `account_id`：

```json
{"type": "qoder", "variant": "global", "account_id": "work"}
```

每个账号在 CPA 中是一条独立的认证记录，冷却和重试互不影响。

## 请求支持

- Chat Completions、SSE 流式输出和 usage 统计。
- `reasoning_effort` 映射到 CLI 的推理档位。`thinking.type` 支持 `auto`、`adaptive`、`enabled`、`disabled`，`thinking.budget_tokens` 映射到思考预算，`reasoning_effort: none` 关闭思考。冲突参数返回 400。
- 函数调用以 OpenAI `tool_calls` 返回。helper 只声明调用方提供的 JSON schema，不执行函数；`parallel_tool_calls: false` 时只返回第一个调用。
- 支持 `stop`、`max_tokens`、`max_completion_tokens`、`n: 1` 和 `response_format: {"type":"json_object"}`。JSON Schema 输出不支持。
- 无法识别的 OpenAI 参数会被丢弃，不会因此返回 400。

仅支持文本。图片、音频和供应商签名的推理块不会保留。每个请求启动独立 CLI 进程，历史消息按角色标记的文本重放，请求之间不共享会话或缓存。

## 常见错误

| 上游情况 | 返回 |
| --- | --- |
| 容量不足，模型进入队列 | `429 model_queued`，等待超过 `queue-wait` 后返回 |
| 输入超过模型上下文 | `400`，消息包含 `context length exceeded` |
| 登录失效 | `401 login_required` |
| 额度或套餐限制 | `403 insufficient_quota` |

CLI 遇到排队会持续轮询数分钟，`queue-wait` 让它尽早结束，CPA 可以重试或换账号。如果前面有 LiteLLM，上下文错误的消息特意保留了 `context length` 字样，`context_window_fallbacks` 可以据此换用更大的模型。

模型列表缓存五分钟。请求另一个区域的模型会直接报错，不会自动跨区路由。

## 构建

```sh
make build
make test

make package-linux TARGET_ARCH=amd64
make package-linux TARGET_ARCH=arm64
make package-macos
```

Linux 包在 `golang:1.26-bookworm` 中编译，目标 ABI 不受构建机影响，产物在 `dist/`。

端到端测试：

```sh
python3 scripts/smoke.py --fixture --cpa-binary /path/to/cli-proxy-api
```

去掉 `--fixture` 会使用当前登录的 CLI 并产生真实模型请求。实现细节见 [docs/architecture.md](docs/architecture.md)。

## 许可

MIT，见 [LICENSE](LICENSE)。

同系列插件：[CPA-OpenCode](https://github.com/VVIP1024/CPA-OpenCode)、[CPA-MiniMax](https://github.com/VVIP1024/CPA-MiniMax)、[CPA-Copilot](https://github.com/VVIP1024/CPA-Copilot)、[CPA-Antigravity](https://github.com/VVIP1024/CPA-Antigravity)。
