# CPA-Qoder

[English](README.md) | 简体中文

CLIProxyAPI 的独立模型提供方插件，同时使用 Qoder 国际版和 QoderCN 的现有 CLI 登录。插件提供模型推理及原生函数调用的交接；规划、工具执行和 Agent 循环由接入 CPA 的客户端负责。

## 安装到现有 CPA

安装后，`cpa-qoder` 会出现在 CPA 的本地插件列表中，由现有 CPA 对外提供模型接口。下面的步骤用于接入你正在使用的 CPA；`make run` 仅供开发时单独试运行。

### 1. 确认运行环境

CPA 需要支持原生插件 C ABI 1 / RPC schema 6，本插件按 CPA 提交 `5be2cda` 验证。动态库必须与 CPA 的操作系统、CPU 架构一致，构建需要 Go 1.26+ 和 C 编译器。`go.mod` 使用本地模块替换，源码旁需保留匹配版本的 `../CLIProxyAPI` 目录。

在运行 CPA 的机器上安装两套 CLI，并以 CPA 服务使用的系统账号完成登录：

```sh
qodercli login
qoderclicn login
```

CPA 作为后台服务时，建议在插件配置中填写 CLI 可执行文件及登录目录的绝对路径。Docker 部署需要在容器内准备 CLI 和可访问的登录目录，不能直接使用宿主机上的 CLI 路径或 macOS 动态库。

若尚未准备 CPA 源码，在本仓库根目录执行：

```sh
git clone https://github.com/router-for-me/CLIProxyAPI.git ../CLIProxyAPI
git -C ../CLIProxyAPI checkout 5be2cda
```

已有兼容版本的源码时可直接使用。`../CLIProxyAPI` 是 Go 模块的构建依赖，插件安装完成后不要求保留源码。

检查 CPA 源码是否已包含 [流关闭修复](patches/cpa-native-stream-cleanup.patch)。尚未包含时，将补丁应用到匹配版本的源码，再重新编译并替换 CPA 程序。

```sh
git -C /path/to/CLIProxyAPI apply --check /absolute/path/to/CPA-Qoder/patches/cpa-native-stream-cleanup.patch
git -C /path/to/CLIProxyAPI apply /absolute/path/to/CPA-Qoder/patches/cpa-native-stream-cleanup.patch
```

### 2. 安装插件文件

先停止现有 CPA，再在 `CPA-Qoder` 源码目录执行。将 `/path/to/cpa/plugins` 替换为 CPA 实际的 `plugins.dir`，`/path/to/cpa/auths` 替换为现有的 `oauth.auth-dir`。

```sh
make install PLUGIN_DIR=/path/to/cpa/plugins AUTH_DIR=/path/to/cpa/auths
mkdir -p /path/to/cpa/plugin-bin
cp build/cpa-qoder-helper /path/to/cpa/plugin-bin/cpa-qoder-helper
chmod 755 /path/to/cpa/plugin-bin/cpa-qoder-helper
```

`make install` 会编译插件，复制当前平台的动态库和两份描述文件；已有的同名描述文件保留。辅助程序按上面的命令单独复制到固定目录，之后无需保留源码中的 `build` 目录。

| 安装文件 | 用途 |
|---|---|
| `plugins/cpa-qoder.dylib`（Linux 为 `.so`） | CPA 加载的原生插件。生成的 `.h` 文件无需安装。 |
| `plugin-bin/cpa-qoder-helper` | 函数声明辅助程序，需有执行权限。 |
| `auths/qoder-global.json` | 国际版账号描述：`{"type":"qoder","variant":"global"}`。 |
| `auths/qoder-cn.json` | 国内版账号描述：`{"type":"qoder","variant":"cn"}`。 |

只有一套账号时，只保留对应描述文件，并将另一套账号设为 `disabled: true`。描述文件不含 token；不要上传 Qoder 的加密 `user` 文件来代替它。

### 3. 修改现有 CPA 配置

将下面字段合并到正在使用的 `config.yaml`，保留现有的其他插件和服务配置。已有 `oauth.auth-dir` 时使用原目录，不要为本插件另换目录。示例中的路径均需替换；CLI 路径可在登录账号下用 `command -v qodercli`、`command -v qoderclicn` 查询。

```yaml
oauth:
  auth-dir: /path/to/cpa/auths
plugins:
  enabled: true
  dir: /path/to/cpa/plugins
  configs:
    cpa-qoder:
      enabled: true
      priority: 1
      helper: /path/to/cpa/plugin-bin/cpa-qoder-helper
      proxy-url: ""
      global:
        cli: /absolute/path/to/qodercli
        config-dir: /home/cpa/.qoder
        disabled: false
      cn:
        cli: /absolute/path/to/qoderclicn
        config-dir: /home/cpa/.qoder-cn
        disabled: false
```

`/home/cpa` 表示 CPA 运行账号的用户目录，macOS 通常为 `/Users/用户名`。CPA 需能读取登录文件、执行 CLI 和 helper。`proxy-url` 作用于模型发现和推理；留空时继承 CLI 代理环境，CPA 的通用 HTTP 代理不会自动用于 CLI。

### 4. 在 CPA 中确认并使用

按原来的服务方式重新启动 CPA，刷新管理界面的插件列表，查找 ID `cpa-qoder`、名称 `CPA-Qoder`。也可使用现有的管理密钥查询：

```sh
curl http://127.0.0.1:8317/v8/management/plugins \
  -H 'Authorization: Bearer YOUR_MANAGEMENT_KEY'
```

应看到 `plugins_enabled: true`，以及该插件的 `registered: true`、`enabled: true`、`effective_enabled: true`。仅列出了文件路径并不代表注册成功。管理接口需要 CPA 已启用管理访问，管理密钥与模型请求的客户端密钥用途不同。

用现有 CPA 的客户端密钥查询模型：

```sh
curl http://127.0.0.1:8317/v1/models \
  -H 'Authorization: Bearer YOUR_CLIENT_API_KEY'
```

模型列表应包含对应账号的 `qoder/`、`qoder-cn/` 模型。客户端继续使用原 CPA 地址和密钥，选择返回的完整模型名即可。插件已注册但没有模型时，先检查描述文件是否放在实际认证目录，再检查服务账号的 CLI 登录和网络。

本仓库提供从源码安装的步骤。CPA 的商店安装需要可下载的动态库和插件源元数据，不能把源码目录或 helper 当作商店安装包导入。

### 更新与停用

更新前停止 CPA，重新安装动态库和 helper，再启动服务；不要覆盖运行中已加载的库。停用时将 `plugins.configs.cpa-qoder.enabled` 设为 `false`，也可通过当前管理接口修改：

```sh
curl -X PUT http://127.0.0.1:8317/v8/management/config/plugins/configs/cpa-qoder/enabled \
  -H 'Authorization: Bearer YOUR_MANAGEMENT_KEY' \
  -H 'Content-Type: application/json' \
  -d 'false'
```

重新启用将请求体改为 `true`，同时确保总开关 `plugins.enabled` 为 `true`。彻底移除时，停止 CPA 后删除本插件的库、helper、两份描述文件及配置项，保留原 CLI 登录目录。

## 插件功能

CPA 加载的插件 ID 是 `cpa-qoder`，模型提供方 ID 是 `qoder`。国际版和国内版共用一个动态库，通过描述文件中的 `variant` 区分账号。

| CPA 接口 | 当前实现 |
|---|---|
| `AuthProvider` | 解析不含 token 的账号描述文件，导入已有 CLI 登录，并维护 CPA 的凭证检查时间。上游登录、签名和 token 刷新由 Qoder CLI 处理。 |
| `ModelProvider` | 按账号调用 CLI 的 `--list-models`，缓存五分钟，分别注册 `qoder/` 和 `qoder-cn/` 模型。不会在两个区域之间切换账号。 |
| `ProviderExecutor` | 接收和返回 Chat Completions，提供文本、流式内容、思考增量及原生函数调用交接；每次推理启动独立 CLI 进程。 |
| `RequestInterceptor` | 在凭证选择前后为本插件的模型请求附加关联 ID，用于找到该请求启动的任务。 |
| `RequestLifecyclePlugin` | 收到请求结束通知后，取消关联任务；客户端断开、请求失败时也走这条清理路径。 |

注册时的 `executor_model_scope: oauth` 表示模型绑定 CPA 认证记录，不代表插件会发起浏览器 OAuth 登录。插件不承担 Agent 规划或工具执行。支持的参数与限制见下文“能力边界”。

## CPA 加载插件的生命周期

1. **选择动态库。** CPA 读取 `plugins.enabled`、`plugins.dir` 和 `plugins.configs.cpa-qoder`，按启用状态及版本选择 `cpa-qoder.dylib`、`.so` 或 `.dll`。`make install` 只是复制文件，实际加载发生在 CPA 应用插件配置时。
2. **初始化原生接口。** CPA 打开动态库，调用 `cliproxy_plugin_init`，校验 C ABI 1，并交换调用、释放缓冲区及关闭回调。此时尚未启动推理 CLI。
3. **注册插件。** CPA 调用 `plugin.register`，传入该插件的 YAML 配置。插件创建 `Provider`，返回元数据、配置字段和上述五项能力；CPA 校验 RPC schema 6 后，将这些能力放入运行时快照。
4. **载入账号和模型。** CPA 通过 `auth.parse` 解析 `oauth.auth-dir` 下的 `qoder-global.json`、`qoder-cn.json`。随后通过 `model.for_auth` 查询各账号可用模型，绑定到对应认证记录；`model.static` 不提供静态模型。目录缓存失效后，下次查询会重新启动发现进程。
5. **处理模型请求。** CPA 运行请求拦截、选择认证记录，调用 `executor.execute` 或 `executor.execute_stream`。插件检查模型与账号是否匹配，在空临时目录启动对应 CLI。普通响应聚合为 JSON；流式响应通过 `host.stream.emit` 返回 JSON 增量，CPA 负责封装 SSE 和 `[DONE]`，插件最后调用 `host.stream.close`。有函数调用时，插件在 CLI 执行工具前停止进程，将调用交给外部 Agent。
6. **结束请求和检查凭证。** CPA 通过异步 `request.complete` 通知正常结束、失败或取消，插件按关联 ID 取消剩余任务，清理进程和临时目录。CPA 调用 `auth.refresh` 时，插件检查 CLI 账号文件并更新下次检查时间，不自行兑换上游 token。`auth.login.start/poll` 只导入已有登录。
7. **重新应用配置。** 已注册实例收到 `plugin.reconfigure` 后，创建并切换到新的 `Provider`，取消旧实例中的任务并等待退出；进行中的请求可能因此中断。换用另一条动态库路径时，CPA 先向旧实例发送 `plugin.quiesce`，再注册新实例；旧库进入保留列表，最终卸载时关闭。
8. **禁用、卸载或停止 CPA。** CPA 先从运行时快照移除插件能力，停止接收新调用，等待已进入的原生调用退出，再调用 C ABI 的 `shutdown` 回调。插件的 `Close()` 取消任务并等待工作协程结束，CPA 随后关闭库句柄。宿主等待超时后，清理可在后台继续。插件也处理 `plugin.shutdown` RPC，但当前原生加载器的最终关闭入口是 C 回调。

入口和 RPC 分发见 [native.go](native.go)、[dispatch.go](dispatch.go)，任务退出见 [config.go](config.go) 和 [runner.go](runner.go)。CPA 侧对应 `internal/pluginhost/host.go`、`internal/pluginhost/loader_unix.go` 和 `internal/pluginhost/client_guard.go`。

## 可增加的 CPA 扩展点

以下是后续可接入的位置，当前没有实现。接口定义以兼容版本的 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 中 `sdk/pluginapi/types.go` 为准；增加能力时，需要同时声明 capability、实现对应 RPC，并补充行为验证。

| CPA 扩展点 | 可增加的功能及前提 |
|---|---|
| `ManagementAPI` / `ManagementHandler` | 提供 CLI 版本、两套登录状态、最近一次模型发现错误、缓存刷新等诊断接口。状态接口应只返回检查结果，不返回凭证内容。 |
| `QuotaProvider` | 展示国际版、国内版各自的余额或额度。需先找到 CLI 或官方支持的查询接口，不能从 token usage 推算余额。 |
| `UsagePlugin` | 消费 CPA 已完成的用量记录，按区域和模型汇总。CLI 报告为 0 的 token 仍须保留为 0。 |
| `AuthProvider` + `Scheduler` | 增加同一区域的多个账号，再按可用性或配额选择认证记录。需先扩展描述文件和账号配置；当前只有 Global/CN 两套固定账号，且两者模型命名空间隔离。 |
| `ModelRouter` | 为已发现模型增加稳定别名或显式路由规则。跨区域切换需单独配置，不能绕过现有模型与账号校验。 |
| `RequestTranslator` / `RequestNormalizer` / `ResponseTranslator` / `ResponseNormalizer` | 在 CPA 现有协议转换之外补充必要的字段映射。当前执行器只声明 Chat Completions 输入输出；增加转换不会让 CLI 自动支持图片或严格 Schema。 |
| `CommandLinePlugin` | 增加登录检查、列出模型或生成描述文件的 CPA 命令行入口，复用现有发现和导入逻辑。 |

## 从源码单独试运行

需要 Go 1.26+、C 编译器和已经登录的 Qoder CLI 1.1.66 或兼容版本。目录布局：

```text
CPA/
├── CLIProxyAPI/
└── CPA-Qoder/
```

确认两套 CLI 已分别登录：

```sh
qodercli login
qoderclicn login
```

然后在 `CPA-Qoder` 目录执行：

```sh
make run
```

此命令编译动态库、工具声明辅助程序和相邻源码中的 CPA 服务，使用 `config.example.yaml` 启动。默认监听 `127.0.0.1:8317`，示例客户端密钥为 `replace-with-your-client-key`。正式使用时复制为 `config.local.yaml` 并更换客户端密钥：

```sh
./build/cli-proxy-api --config config.local.yaml --local-model
```

模型名称按当前登录账号动态发现，不预设账号没有权限的模型：

```sh
curl http://127.0.0.1:8317/v1/models \
  -H 'Authorization: Bearer replace-with-your-client-key'

curl http://127.0.0.1:8317/v1/chat/completions \
  -H 'Authorization: Bearer replace-with-your-client-key' \
  -H 'Content-Type: application/json' \
  -d '{"model":"qoder-cn/Qwen3.8-Flash","messages":[{"role":"user","content":"你好"}],"stream":true}'
```

国际版模型为 `qoder/<CLI模型名>`，国内版为 `qoder-cn/<CLI模型名>`，保留模型名原有大小写。实际名称以 `/v1/models` 为准。只有一套账号时，移除另一套描述文件，或将对应区域的 `disabled` 设为 `true`。

## 外部 Agent 的工具调用

客户端按 OpenAI Chat Completions 格式传入 `tools`。插件通过 MCP 仅向模型声明这些函数的名称、描述和 JSON Schema，截获模型生成的原生工具调用，在 CLI 执行工具前终止该次推理，返回标准的 `tool_calls` 和 `finish_reason: "tool_calls"`。

辅助程序没有工具实现，不执行函数、不访问业务文件，也不伪造工具结果。客户端执行工具后，将 assistant 的 `tool_calls` 和 role=tool 的结果加入下一次请求。支持流式函数参数增量和多个原生函数调用。此机制不依赖让模型在文本中模仿工具 JSON。

## 能力边界

- 支持文本、多轮历史、流式内容和思考增量、函数工具及工具结果历史。
- 支持 `max_tokens` / `max_completion_tokens`、CLI 提供的 `reasoning_effort`、`tool_choice: auto/none`。具体推理设置仍受所选模型限制。
- 不支持图片/音频、强制指定函数、`tool_choice: required`、严格工具 Schema、temperature/top_p、stop、JSON Schema 输出等 CLI 未公开的控制；显式返回错误，避免悄悄忽略。
- CLI 接收的是带角色标签的完整历史文本，系统指令单独传入。这与直接使用上游原生消息协议不完全等价。每次请求启动独立 CLI，不保留上游会话和隐藏推理状态。
- Token usage 采用 CLI 报告的值；部分 Qoder 模型报告为 0，插件不会估算。独立精确 token 计数接口返回 501。
- CLI 负责上游 HTTP，因此 CPA 的逐条上游 HTTP 请求日志不可见；CPA 仍记录执行和用量。插件不提供任意 HTTP 凭证代理。
- 每次请求禁用 CLI 自身的工具、hooks、项目设置和持久化会话，使用空临时工作目录。函数 Schema 辅助程序随推理进程一并清理。取消请求或卸载插件会终止相关进程。

## 验证

```sh
make test
make build
```

真实账号端到端测试可运行 `python3 scripts/smoke.py`（macOS/Linux，需要两套 CLI 均已登录，会消耗少量真实模型额度）。脚本使用临时凭证副本并自动清理。

详细接入依据见 [架构说明](docs/architecture.md)，本机验证结果见 [验证记录](docs/verification.md)。


