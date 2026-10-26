# CPA-Qoder

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) v8 plugin that exposes Qoder and QoderCN as OpenAI-compatible models. If you already use the Qoder CLI, this lets any Chat Completions client talk to the same account.

The plugin runs `qoder` or `qodercn` once per request. The international account is published as `qoder/<model>`, the China account as `qodercn/<model>`. The CLI answers the prompt; the plugin only moves messages and results. The CLI's own agent loop stays off: tool calls are handed back to the client, and the client decides what to execute.

[中文说明](README.zh-CN.md)

## Requirements

- CLIProxyAPI v8 with native plugin support (C ABI 1, RPC schema 6). Built against v8.0.0.
- The Qoder CLI installed and logged in (`qoder login`, `qodercn login`).
- Linux `amd64`/`arm64` or macOS. Building from source needs Go 1.26 and a C compiler; the Linux package is built in Docker.

You need your own Qoder account. The plugin does not create accounts, bundle credentials, or bypass plan limits.

## Install

Download the archive for your architecture from Releases and unpack it:

```sh
tar -xzf cpa-qoder-linux-amd64.tar.gz
```

The archive contains the plugin, `cpa-qoder-helper`, two auth descriptors, and a config fragment. Copy the files where CPA expects them, for example:

```sh
cp cpa-qoder-linux-amd64/plugins/cpa-qoder.so /path/to/cpa/plugins/
cp cpa-qoder-linux-amd64/bin/cpa-qoder-helper /path/to/cpa/plugin-bin/
chmod 755 /path/to/cpa/plugin-bin/cpa-qoder-helper
cp cpa-qoder-linux-amd64/auths/*.json /path/to/cpa/auths/
```

Keep `/root/.qoder` and `/root/.qoder-cn` on persistent storage if CPA runs in Docker. The CLI login lives there.

Merge the plugin section into `config.yaml`. Do not replace the rest of the file:

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

If you only have one account, delete the other descriptor and set the unused section to `disabled: true`.

Log in inside the container if you have not already:

```sh
docker exec -it <cpa-container> qoder login
docker exec -it <cpa-container> qodercn login
```

Restart CPA and check the model list:

```sh
curl http://127.0.0.1:8317/v1/models \
  -H 'Authorization: Bearer <client-key>'
```

## Configuration

| Key | Default | Meaning |
| --- | --- | --- |
| `global.cli` | `qoder` | International CLI executable |
| `global.config-dir` | `~/.qoder` | Login and settings for the international CLI |
| `global.disabled` | `false` | Turn the international account off |
| `cn.cli` | `qodercn` | China CLI executable |
| `cn.config-dir` | `~/.qoder-cn` | Login and settings for the China CLI |
| `cn.disabled` | `false` | Turn the China account off |
| `accounts` | empty | Additional isolated accounts, see below |
| `helper` | looked up on `PATH` | Schema helper used for function calling |
| `proxy-url` | empty | Proxy for discovery and generation; empty inherits `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` |
| `queue-wait` | `60s` | How long a request may wait in the upstream model queue; `0` waits forever |

Extra accounts need their own CLI config directory and an `account_id` in the auth descriptor:

```yaml
      accounts:
        - id: work
          variant: global
          cli: "/usr/local/bin/qoder"
          config-dir: "/root/.qoder-work"
```

```json
{"type": "qoder", "variant": "global", "account_id": "work"}
```

Each account is a separate CPA auth record, so upstream cooling and retries stay per account.

## Request support

- Chat Completions, including SSE streaming and usage reporting.
- `reasoning_effort` maps to the CLI's reasoning level. `thinking.type` accepts `auto`, `adaptive`, `enabled`, `disabled`; `thinking.budget_tokens` maps to the thinking budget. `reasoning_effort: none` disables thinking. Conflicting combinations return 400.
- Function calls are returned as OpenAI `tool_calls`. The helper only declares the caller's JSON schema; it never executes anything. With `parallel_tool_calls: false`, only the first call is returned.
- `stop`, `max_tokens`, `max_completion_tokens`, `n: 1`, and `response_format: {"type":"json_object"}` are handled. JSON Schema output is not supported.
- Unknown OpenAI parameters are dropped instead of rejected, so standard client libraries work without tuning.

Text only. Images, audio, and provider-signed reasoning blocks are not preserved. Every request starts a fresh CLI process and replays history as role-tagged text; there is no shared session or prompt cache between requests.

## Errors worth knowing

The plugin translates the failures that are easy to misread:

| Upstream condition | Response |
| --- | --- |
| Model queued because capacity is exhausted | `429 model_queued`, after `queue-wait` |
| Input above the model's context limit | `400` with `context length exceeded` in the message |
| Expired or missing login | `401 login_required` |
| Quota or plan limit | `403 insufficient_quota` |

`queue-wait` exists because the CLI keeps polling a queued model for minutes. A 429 lets CPA retry or move to another account instead of holding the client connection open. If you put LiteLLM in front of CPA, the context error is worded so `context_window_fallbacks` can select a larger model.

Model discovery is cached for five minutes. Requests for a model that belongs to the other region are rejected rather than silently routed there.

## Build from source

```sh
make build
make test

make package-linux TARGET_ARCH=amd64
make package-linux TARGET_ARCH=arm64
make package-macos
```

`make package-linux` uses the `golang:1.26-bookworm` image, so the target ABI does not depend on the build host. Artifacts land in `dist/`.

An end-to-end smoke test can run against an external CPA binary:

```sh
python3 scripts/smoke.py --fixture --cpa-binary /path/to/cli-proxy-api
```

Drop `--fixture` to use the logged-in CLIs and a real model request. See [docs/architecture.md](docs/architecture.md) and [docs/verification.md](docs/verification.md) for internals and test notes.

## License

MIT. See [LICENSE](LICENSE).

Other plugins in this family: [CPA-OpenCode](https://github.com/VVIP1024/CPA-OpenCode), [CPA-MiniMax](https://github.com/VVIP1024/CPA-MiniMax), [CPA-Copilot](https://github.com/VVIP1024/CPA-Copilot), [CPA-Antigravity](https://github.com/VVIP1024/CPA-Antigravity).
