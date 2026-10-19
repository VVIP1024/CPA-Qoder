# Architecture

## CPA integration

The plugin targets `github.com/router-for-me/CLIProxyAPI/v8 v8.0.0`, Go 1.26, native ABI 1 and RPC schema 6. It uses CPA's native dynamic-library host. The published Go module provides the SDK at build time; executable-plugin documentation does not describe this loader.

| Source | Relevant responsibility |
|---|---|
| `sdk/cliproxy/service_executors.go` | Native provider registration; unknown provider executors can be supplied by plugins instead of generic OpenAI compatibility. |
| `sdk/cliproxy/auth/conductor.go` | Executor contract and host credential scheduling. |
| `sdk/pluginapi/types.go` | AuthData, AuthProvider, ModelProvider, ProviderExecutor, input/output formats and lifecycle contracts. |
| `sdk/pluginabi/types.go` | Native ABI and JSON envelope versions and method names. |
| `internal/pluginhost/loader_unix.go` | dlopen and `cliproxy_plugin_init`; function pointers and owned response buffers. |
| `internal/pluginhost/rpc_schema.go` | Lowercase lifecycle and stream fields; SDK execution fields retain their exported JSON names. |
| `internal/pluginhost/auth_provider.go` | Descriptor parsing into runtime auth records and opaque provider storage. |
| `internal/pluginhost/adapters_executors.go` | Protocol negotiation, CPA request/response translation, execution and usage accounting. |
| `internal/pluginhost/rpc_client_stream.go` | Async plugin stream ID and callback lifetime. |
| `internal/pluginhost/stream_bridge.go` | Emission, close, cancellation and queue handling. |
| `sdk/api/handlers/handlers_interceptors.go` | Request ID correlation and terminal request events, including cancellation. |
| `internal/runtime/executor/codex_executor_execute.go` | Contrast: Codex directly authenticates and sends Responses requests; Qoder's CLI owns its signing protocol. |

CPA-Qoder uses the existing provider and response translator interfaces on unmodified CPA. Models are OAuth-bound and requests use Chat Completions. Executor output uses the private `cpa-qoder-chat` format, whose frames retain standard Chat Completions fields for CPA usage accounting. `ResponseTranslator` removes the internal stream marker and translates to the requested client protocol with CPA's built-in SDK translators.

Each stream allows one unacknowledged chunk. CPA calls `response.translate` only after taking that chunk from its stream queue; the translator acknowledges consumption before the plugin sends another. Slow consumers therefore stop a cancelable local wait instead of saturating the native callback queue. Shutdown cancels and joins the workers even after the host rejects all callbacks. No C callback is left running after shutdown returns.

## Files and flow

`native.go` owns the C ABI, envelopes and host callbacks. `dispatch.go` maps RPC methods to the provider and emits async streams. `stream_transport.go` handles consumption receipts, output conversion and per-stream translator state. `auth.go` deals only with account descriptors. `models.go` discovers and caches per-account model names. `request.go` validates model inputs and formats text history. `runner.go` runs one CLI inference, while `events.go` parses native output and `response.go` formats OpenAI responses. `mcp.go` is a declaration-only tool server, compiled into the helper executable. Platform process files terminate the whole CLI process tree.

The two descriptor variants share provider ID `qoder` but advertise disjoint model IDs. Requests validate namespace and account catalog before generation. There is no fallback between Global and CN accounts. Config roots are explicit, and inherited Qoder/Anthropic/OpenAI authentication and endpoint overrides are removed.

For functions, the helper advertises exactly the caller's JSON schemas. The model's native tool-use blocks supply names, IDs and argument deltas. At the `tool_use` stop boundary, the entire CLI process group is stopped; the helper deliberately leaves function calls pending and has no side effects. The plugin returns the native calls to the external Agent, which executes functions and supplies results in the next request.

## Known transport limits

The public CLI exposes text/stream-json entrypoints, not a direct arbitrary Chat Completions request API. Conversation replay therefore uses role-labelled JSON text. Direct native tool messages, forcing tool choice, structured outputs and unsupported generation controls are not claimed. Requests use isolated temporary working directories, explicit stdin, disabled built-ins/hooks, empty settings sources and no session persistence.

References: the CPA source paths above and the official [CLI reference](https://docs.qoder.com/cli/cli-reference), [authentication](https://docs.qoder.com/cli/authentication), and [script mode](https://docs.qoder.com/cli/run-in-scripts). The implementation was checked against CLI help and observed output. The plugin does not print or persist decrypted tokens.
