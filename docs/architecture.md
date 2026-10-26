# 架构

## CPA 集成

插件面向 `github.com/router-for-me/CLIProxyAPI/v8 v8.0.0`、Go 1.26、原生 ABI 1 和 RPC schema 6，使用 CPA 的原生动态库宿主。公开发布的 Go module 在构建时提供 SDK；可执行插件文档没有描述该加载器。

| 源码 | 相关职责 |
|---|---|
| `sdk/cliproxy/service_executors.go` | 原生 provider 注册；未知 provider 执行器可由插件提供，而不走通用 OpenAI 兼容路径。 |
| `sdk/cliproxy/auth/conductor.go` | 执行器契约和宿主凭证调度。 |
| `sdk/pluginapi/types.go` | AuthData、AuthProvider、ModelProvider、ProviderExecutor、输入/输出格式及生命周期契约。 |
| `sdk/pluginabi/types.go` | 原生 ABI、JSON envelope 版本和方法名。 |
| `internal/pluginhost/loader_unix.go` | dlopen 和 `cliproxy_plugin_init`；函数指针及由插件持有的响应缓冲区。 |
| `internal/pluginhost/rpc_schema.go` | 生命周期和流字段使用小写；SDK 执行字段保留导出后的 JSON 名称。 |
| `internal/pluginhost/auth_provider.go` | 将描述文件解析为运行时认证记录和不透明的 provider 存储。 |
| `internal/pluginhost/adapters_executors.go` | 协议协商、CPA 请求/响应转换、执行和用量统计。 |
| `internal/pluginhost/rpc_client_stream.go` | 异步插件流 ID 和回调生命周期。 |
| `internal/pluginhost/stream_bridge.go` | 发送、关闭、取消和队列处理。 |
| `sdk/api/handlers/handlers_interceptors.go` | 请求 ID 关联和终止请求事件，包括取消。 |
| `internal/runtime/executor/codex_executor_execute.go` | 对照实现：Codex 直接认证并发送 Responses 请求；Qoder 的签名协议由 CLI 负责。 |

CPA-Qoder 在未修改的 CPA 上使用现有 provider 和响应转换接口。模型绑定 OAuth，请求使用 Chat Completions。执行器输出使用私有 `cpa-qoder-chat` 格式，其帧保留标准 Chat Completions 字段，供 CPA 统计用量。`ResponseTranslator` 移除内部流标记，并通过 CPA SDK 内置转换器转换为客户端请求的协议。

每条流只允许一个未确认数据块。CPA 从流队列取走数据块后才调用 `response.translate`；转换器确认消费后，插件才发送下一块。因此慢消费者只会让可取消的本地等待停下，不会塞满原生回调队列。关闭时即使宿主拒绝所有回调，也会取消并等待工作协程结束；`shutdown` 返回后不遗留 C 回调。

## 文件与流程

`cmd/cpa-qoder/main.go` 负责 C ABI、envelope 和宿主回调。`dispatch.go` 将 RPC 方法映射到 provider 并发送异步流。`stream_transport.go` 处理消费回执、输出转换和每条流的转换器状态。`auth.go` 只处理账号描述文件。`models.go` 发现并缓存每个账号的模型名。`request.go` 校验模型输入并格式化文本历史。`runner.go` 执行一次 CLI 推理，`events.go` 解析原生输出，`response.go` 生成 OpenAI 响应。`mcp.go` 是仅声明工具的服务，编译进 helper 可执行文件。各平台进程文件负责终止整个 CLI 进程树。

两套描述文件共用 provider ID `qoder`，模型 ID 分别使用 `qoder/<模型>` 和 `qodercn/<模型>`。请求生成前会按所选账号的模型目录校验，并拒绝与账号不匹配的模型前缀；Global 与 CN 账号之间不会自动回退。两套 CLI 返回同名模型时，前缀可区分并路由到对应账号。配置根目录必须显式指定；继承的 Qoder/Anthropic/OpenAI 认证和端点覆盖会被移除。

函数调用方面，helper 只声明调用方提供的 JSON schema。模型的原生 tool-use 块提供名称、ID 和参数增量。到达 `tool_use` 停止边界时，整个 CLI 进程组被终止；helper 故意不执行函数，也没有副作用。插件把原生调用返回给外部 Agent，由后者执行函数并在下一次请求中提供结果。

## 已知传输限制

公开 CLI 提供文本/stream-json 入口，不是任意 Chat Completions 请求 API。因此会话使用按角色标记的 JSON 文本重放。直接原生工具消息、强制工具选择、结构化输出和不支持的生成参数不在支持范围内。请求使用隔离的临时工作目录、显式 stdin、禁用的内置功能/hooks、空 settings 来源，并关闭会话持久化。

参考：上表中的 CPA 源码路径，以及官方 [CLI 参考](https://docs.qoder.com/cli/cli-reference)、[认证](https://docs.qoder.com/cli/authentication) 和 [脚本模式](https://docs.qoder.com/cli/run-in-scripts)。实现已对照 CLI help 和实际输出检查。插件不会打印或持久化解密后的 token。
