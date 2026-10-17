# CPA-Qoder

English | [简体中文](README_CN.md)

A standalone native CLIProxyAPI plugin providing model inference through existing Qoder Global and QoderCN CLI logins. External CPA clients own Agent planning, function execution and subsequent turns.

See [中文使用说明](README_CN.md) for setup, configuration, examples and limitations.

## Install into an existing CPA

This installs `cpa-qoder` into CPA's local plugin list. Use your existing CPA server, endpoint and client keys. `make run` starts a separate development server and is not required for installation.

### 1. Prepare the runtime

The plugin was verified against CPA commit `5be2cda`, C ABI 1 / RPC schema 6. Build with Go 1.26+, a C compiler and the matching CPA source at `../CLIProxyAPI` (the local module replacement). Build for the same OS and CPU as the CPA process.

Install and log into `qodercli` and `qoderclicn` on the CPA machine as the service's OS user. For containers, install the CLIs inside the container and make login directories accessible there. Host CLI paths and libraries for another OS cannot be reused directly.

If the CPA source is not already available, prepare it from this repository root:

```sh
git clone https://github.com/router-for-me/CLIProxyAPI.git ../CLIProxyAPI
git -C ../CLIProxyAPI checkout 5be2cda
```

Use an existing compatible checkout instead when available. The Go module imports CPA's plugin SDK through the `../CLIProxyAPI` replacement; the source directory is a build dependency, not a runtime requirement.

Check whether the CPA checkout already includes [the stream cleanup fix](patches/cpa-native-stream-cleanup.patch). If missing, apply it to the matching source version, then rebuild and replace CPA.

```sh
git -C /path/to/CLIProxyAPI apply --check /absolute/path/to/CPA-Qoder/patches/cpa-native-stream-cleanup.patch
git -C /path/to/CLIProxyAPI apply /absolute/path/to/CPA-Qoder/patches/cpa-native-stream-cleanup.patch
```

### 2. Install the files

Stop CPA. From the `CPA-Qoder` source directory, replace the paths below with the existing `plugins.dir` and `oauth.auth-dir`:

```sh
make install PLUGIN_DIR=/path/to/cpa/plugins AUTH_DIR=/path/to/cpa/auths
mkdir -p /path/to/cpa/plugin-bin
cp build/cpa-qoder-helper /path/to/cpa/plugin-bin/cpa-qoder-helper
chmod 755 /path/to/cpa/plugin-bin/cpa-qoder-helper
```

This installs `plugins/cpa-qoder.dylib` (Linux: `.so`), `auths/qoder-global.json` and `auths/qoder-cn.json`. Existing descriptors are preserved. Copy the helper separately as shown; the source `build` directory is then unnecessary at runtime. Generated C headers do not need installation.

Only keep descriptors for accounts you use and disable unused variants. Descriptors contain `type` and `variant`, never tokens. Do not replace them with Qoder's encrypted `user` file.

### 3. Merge CPA configuration

Merge these fields into the existing configuration, keeping other plugins, server settings and client keys. Keep the existing auth directory. Replace all example paths; use absolute CLI paths from `command -v qodercli` and `command -v qoderclicn` under the service user.

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

`/home/cpa` stands for the service user's home (usually `/Users/<user>` on macOS). That user must be able to read login files and execute both CLIs and the helper.

### 4. Check the CPA plugin list

Restart your existing CPA service and refresh its plugin list. Look for ID `cpa-qoder`, name `CPA-Qoder`. With management access already configured, check the same state through:

```sh
curl http://127.0.0.1:8317/v8/management/plugins \
  -H 'Authorization: Bearer YOUR_MANAGEMENT_KEY'
curl http://127.0.0.1:8317/v1/models \
  -H 'Authorization: Bearer YOUR_CLIENT_API_KEY'
```

Expect `plugins_enabled: true` and the plugin's `registered`, `enabled` and `effective_enabled` all true. A discovered file alone does not confirm registration. The model list should contain the configured accounts' `qoder/` and `qoder-cn/` models. Management and client keys serve different purposes. If registration succeeds but models are missing, check the actual auth directory, service user's CLI login and connectivity.

This repository currently documents installation from source. CPA's store requires downloadable native libraries and registry metadata; it cannot import this source directory or the helper as a plugin package.

Use an exact model name returned by `/v1/models` in a Chat Completions request:

```sh
curl http://127.0.0.1:8317/v1/chat/completions \
  -H 'Authorization: Bearer YOUR_CLIENT_API_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"qoder/MODEL_FROM_LIST","messages":[{"role":"user","content":"Hello"}],"stream":true}'
```

### Update or disable

Stop CPA before replacing the library and helper, then restart. To disable, set `plugins.configs.cpa-qoder.enabled: false`, or use the current management API:

```sh
curl -X PUT http://127.0.0.1:8317/v8/management/config/plugins/configs/cpa-qoder/enabled \
  -H 'Authorization: Bearer YOUR_MANAGEMENT_KEY' \
  -H 'Content-Type: application/json' -d 'false'
```

Use `true` to re-enable and keep `plugins.enabled: true`. To remove completely, stop CPA and remove this library, helper, descriptors and configuration entry. Keep the original CLI login directories.

For a development server, use `make run`. Its example client key must be replaced for normal use.

Both accounts are isolated: `qoder/<CLI model name>` uses `qodercli` with `~/.qoder`; `qoder-cn/<CLI model name>` uses `qoderclicn` with `~/.qoder-cn`. Models are discovered per account. Token-free descriptors in `auths/` activate the accounts through CPA's AuthProvider interface. The included host patch lets stream cleanup callbacks release blocked output during unload.

Function calling uses native model tool-use events through a schema-only MCP helper. The plugin stops the CLI before function execution and returns OpenAI tool calls; the helper never implements or executes functions. Textual history and tool results are replayed as role-labelled JSON text, with system instructions provided separately. This is a CLI transport, not direct access to Qoder's private model protocol, and native message semantics are not identical. Unsupported controls and modalities are rejected explicitly.

## Supported requests and limits

Text input, streamed text/reasoning, external function declarations and tool-result history are supported. Generation controls include `max_tokens` / `max_completion_tokens`, the CLI's `reasoning_effort`, and `tool_choice: auto|none`, subject to model support.

Images/audio, forced or required tool choice, strict tool schemas, `temperature`, `top_p`, `stop` and JSON Schema output are rejected. History is replayed as role-labelled text; native provider message semantics, signed reasoning and cache state are not preserved between requests. Usage reflects CLI-reported values, including zero counts from some models, without estimates.

## Implemented CPA interfaces

The plugin ID is `cpa-qoder`; its provider ID is `qoder`. Global and CN accounts share one library and use separate descriptor variants.

| Interface | Current behavior |
|---|---|
| `AuthProvider` | Parses token-free descriptors, imports existing CLI logins and schedules CPA credential checks. Qoder CLI owns upstream authentication, signing and token refresh. |
| `ModelProvider` | Runs `--list-models` per account with a five-minute cache. Registers separate `qoder/` and `qoder-cn/` namespaces without cross-region fallback. |
| `ProviderExecutor` | Accepts and emits Chat Completions: text, streaming content, reasoning deltas and native function-call handoff. Each inference starts a separate CLI process. |
| `RequestInterceptor` | Adds a request correlation ID before and after credential selection for matching model names. |
| `RequestLifecyclePlugin` | Cancels remaining work when CPA reports completion, failure or cancellation. |

The declared `executor_model_scope: oauth` binds models to CPA auth records; it does not start a browser OAuth flow. Exact token counting and arbitrary authenticated HTTP requests return 501.

## CPA load and request lifecycle

1. **Select the library.** CPA reads `plugins.enabled`, `plugins.dir` and `plugins.configs.cpa-qoder`, then selects an enabled library/version (`cpa-qoder.dylib`, `.so` or `.dll`). Installation copies files; loading happens when CPA applies plugin configuration.
2. **Initialize the native interface.** CPA opens the library and calls `cliproxy_plugin_init`, checks C ABI 1, and exchanges call, buffer-free and shutdown callbacks. No inference CLI starts at this stage.
3. **Register capabilities.** CPA sends the plugin YAML through `plugin.register`. The plugin creates a `Provider` and returns metadata, configuration fields and the five capabilities above. CPA validates RPC schema 6 and publishes the capabilities in its runtime snapshot.
4. **Load accounts and models.** `auth.parse` reads `qoder-global.json` and `qoder-cn.json` from `oauth.auth-dir`. `model.for_auth` discovers models and binds them to each auth record. `model.static` contributes no static models. After the catalog cache expires, the next lookup runs discovery again.
5. **Execute a request.** CPA runs interception, selects an auth record and calls `executor.execute` or `executor.execute_stream`. The plugin validates the account/model pair and starts the matching CLI in an empty temporary directory. Non-streaming output becomes a JSON response. Streaming JSON deltas go through `host.stream.emit`; CPA supplies SSE framing and `[DONE]`, and the plugin calls `host.stream.close`. Native function calls stop the CLI before tool execution and return to the external Agent.
6. **Finish work and check credentials.** Asynchronous `request.complete` events cancel remaining tasks by request ID; processes and temporary files are cleaned up. `auth.refresh` checks CLI account files and renews CPA's check schedule. It does not exchange upstream tokens. `auth.login.start/poll` only imports an existing CLI login.
7. **Reapply configuration.** `plugin.reconfigure` creates and swaps in a new `Provider`, cancels old tasks and waits for them to exit. In-flight requests can be interrupted. When replacing a library path, CPA first sends `plugin.quiesce` to the old instance and registers the new one; the old library remains retired until final unload.
8. **Disable, unload or stop CPA.** CPA removes the plugin from its runtime snapshot, stops new calls, waits for active native calls and invokes the C ABI shutdown callback. `Close()` cancels tasks and joins workers before CPA closes the library handle. Cleanup may continue in the background after the host's wait context expires. The dispatcher also accepts `plugin.shutdown`, but this native loader uses the C callback for final shutdown.

Source: [native entrypoint](native.go), [RPC dispatch](dispatch.go), [worker lifecycle](config.go), [CLI runner](runner.go); CPA's `internal/pluginhost/host.go`, `internal/pluginhost/loader_unix.go` and `internal/pluginhost/client_guard.go`.

## Further CPA extension points

These additions are not implemented. Interfaces are defined in `sdk/pluginapi/types.go` in a compatible [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) checkout. Each addition needs a capability declaration, RPC handlers and verification.

| Extension | Possible addition and requirements |
|---|---|
| `ManagementAPI` / `ManagementHandler` | CLI version, per-region login checks, discovery errors and catalog refresh. Return status without exposing credentials. |
| `QuotaProvider` | Per-region balance or quota, once a supported CLI or official query interface is available. Token usage cannot establish remaining balance. |
| `UsagePlugin` | Aggregate completed CPA usage records by region/model; preserve zero token counts reported by the CLI. |
| `AuthProvider` + `Scheduler` | Multiple accounts within one region, followed by availability/quota-based selection. Requires extending account configuration and descriptors; today's fixed Global/CN accounts have disjoint model namespaces. |
| `ModelRouter` | Stable aliases and explicit routing to discovered models. Cross-region routing requires its own configuration and must retain account/model checks. |
| `RequestTranslator` / `RequestNormalizer` / `ResponseTranslator` / `ResponseNormalizer` | Additional field mappings beyond CPA's existing translation. The executor currently declares Chat Completions only; translation alone cannot add CLI support for images or strict schemas. |
| `CommandLinePlugin` | CPA flags for login checks, model discovery or descriptor generation, using the existing import and discovery code. |

## Verification

Run `make test` for unit and race checks. See [architecture](docs/architecture.md) and [verification](docs/verification.md).

Set `plugins.configs.cpa-qoder.proxy-url` for the CLI transport proxy, including discovery and generation. Empty inherits CLI proxy environment variables. CPA’s general HTTP proxy setting does not automatically reach CLI subprocesses.
