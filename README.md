# CPA-Qoder

English | [简体中文](README_CN.md)

CPA-Qoder is a native model-provider plugin for CPA. It uses existing Qoder Global and QoderCN CLI logins, registers their models under `qoder/*` and `qoder-cn/*`, and exposes completions, streaming, and function-call handoff through CPA.

The external CPA client remains responsible for tool execution, planning, and the Agent loop.

## Compatibility and repository boundary

- Native plugin C ABI 1 and RPC schema 6, built against the published CPA v8.0.0 SDK.
- Linux release packages target Debian Bookworm/glibc and are available for amd64 and arm64.
- `qodercli` and `qoderclicn` are supported together, with separate credentials and model namespaces.
- This repository has no Git submodule, local module replacement, sibling source-tree reference, or host patch. Builds download the published Go SDK module. A deployed package needs neither Go nor either source tree.

## Build a release package

Only Docker is required on the build machine:

```sh
make release-linux-amd64  # common x86_64 servers
make release-linux-arm64  # ARM64 servers
```

The archive and checksum are written to `dist/`. Each archive contains:

```text
cpa-qoder-linux-<arch>/
├── plugins/cpa-qoder.so
├── bin/cpa-qoder-helper
├── auths/qoder-global.json
├── auths/qoder-cn.json
├── config.example.yaml
└── INSTALL.md
```

Plain `make` builds the amd64 package. Compilation runs in `golang:1.26-bookworm` with an explicit Linux target, so the build machine's operating system and CPU do not change the artifact format.

## Install in a Docker CPA deployment

The package contains the plugin, helper, account descriptors, and configuration fragment. It cannot redistribute the Qoder CLIs or your credentials. Install the Linux versions of `qodercli` and `qoderclicn` in the CPA container, and persist `/root/.qoder` and `/root/.qoder-cn` so the CLIs can refresh their login state.

Extract the package that matches the server CPU, then add these mounts to the CPA Compose service:

```yaml
services:
  cpa:
    volumes:
      - ./cpa-qoder-linux-amd64/plugins/cpa-qoder.so:/CLIProxyAPI/plugins/cpa-qoder.so:ro
      - ./cpa-qoder-linux-amd64/bin/cpa-qoder-helper:/CLIProxyAPI/plugin-bin/cpa-qoder-helper:ro
      - ./cpa-qoder-linux-amd64/auths/qoder-global.json:/root/.cli-proxy-api/qoder-global.json:ro
      - ./cpa-qoder-linux-amd64/auths/qoder-cn.json:/root/.cli-proxy-api/qoder-cn.json:ro
      - ./qoder-home:/root/.qoder
      - ./qodercn-home:/root/.qoder-cn
```

The descriptor mounts above use the official Docker default auth directory, `/root/.cli-proxy-api`. If your existing `oauth.auth-dir` differs, change both mount targets to that directory. Keep the current auth directory so existing CPA accounts remain visible.

The example expects the CLIs at `/usr/local/bin/qodercli` and `/usr/local/bin/qoderclicn`. Bake their full Linux installations into your CPA image, or mount them at those paths. Merge `config.example.yaml` into the existing CPA configuration; keep the server, access-key, auth-directory, and unrelated plugin settings already in that file.

With persistent login directories mounted, login from the container:

```sh
docker exec -it <cpa-container> qodercli login
docker exec -it <cpa-container> qoderclicn login
```

Restart CPA and query `/v1/models`. Global models appear as `qoder/<model>` and China models as `qoder-cn/<model>`. If only one account is used, remove the other descriptor and set that variant's `disabled` field to `true`.

See [INSTALL.md](INSTALL.md) for the same deployment steps included in the release archive.

## Features

| Capability | Behavior |
|---|---|
| `AuthProvider` | Parses both account descriptors and checks CLI login files. The official CLIs retain credential, signing, and refresh ownership. |
| `ModelProvider` | Discovers models for each account with a five-minute cache and keeps the two namespaces separate. |
| `ProviderExecutor` | Handles Chat Completions, normal and SSE output, reasoning deltas, and native function-call handoff. Each inference gets its own CLI process. |
| `RequestInterceptor` | Carries the CPA request ID for matching Qoder requests. |
| `RequestLifecyclePlugin` | Stops outstanding CLI work when CPA reports completion, failure, or cancellation. |
| `ResponseTranslator` | Converts the internal transport to CPA client protocols and applies stream backpressure. |

`proxy-url` sets the proxy used for discovery and generation. When empty, the CLI inherits `HTTP_PROXY`, `HTTPS_PROXY`, and `ALL_PROXY` from the container.

## CPA plugin lifecycle

1. CPA scans `plugins.dir` and selects `cpa-qoder.so` when `plugins.configs.cpa-qoder.enabled` is true.
2. CPA opens the library, calls `cliproxy_plugin_init`, checks the C ABI, and exchanges call, memory-release, and shutdown callbacks. No Qoder process starts yet.
3. `plugin.register` parses the YAML configuration, creates the Provider, and returns metadata, fields, and capabilities.
4. `auth.parse` creates Global and CN auth records from descriptors in `oauth.auth-dir`.
5. `model.for_auth` runs discovery through the matching CLI and binds its models to that auth record.
6. CPA translates the incoming request, selects credentials, and calls `executor.execute` or `executor.execute_stream`. Function calls are returned to the external Agent.
7. Completion and cancellation events remove subprocesses and temporary files. Reconfiguration drains the old Provider before replacement.
8. On disable or shutdown, CPA blocks new calls, waits for native calls, invokes the shutdown callback, and unloads the library.

## CPA extension points

| Extension | Possible addition |
|---|---|
| `ManagementAPI` / `ManagementHandler` | CLI versions, login health, discovery errors, and catalog refresh. |
| `QuotaProvider` | Per-region quota if a stable supported query becomes available. |
| `UsagePlugin` | Completed-request usage grouped by region and model. |
| `AuthProvider` + `Scheduler` | Multiple accounts in one region with availability or quota scheduling. |
| `ModelRouter` | Stable aliases and explicit routes while preserving account checks. |
| `RequestTranslator` / `ResponseNormalizer` | Images, structured output, and other mappings when the CLI supports those semantics. |
| `CommandLinePlugin` | Login checks, model refresh, and descriptor generation commands. |

These extensions are not implemented. Each one needs a capability declaration, RPC handlers, and host compatibility coverage.

## Development and verification

Local development builds require Go 1.26 and a C compiler:

```sh
make build
make test
```

The end-to-end fixture accepts an existing CPA executable and does not inspect a sibling source tree:

```sh
python3 scripts/smoke.py --fixture --cpa-binary /path/to/cli-proxy-api
```

See [architecture](docs/architecture.md) and [verification](docs/verification.md). The current public CLI is text-oriented; image input, forced tool choice, strict structured output, and some generation controls are not supported.
