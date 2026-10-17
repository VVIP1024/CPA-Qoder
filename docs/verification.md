# Verification record

Date: 2026-10-17. Host: macOS, arm64. Go: 1.26.4.
CPA source: `5be2cda`, module `github.com/router-for-me/CLIProxyAPI/v8`, native ABI 1 / RPC schema 6. The host source is unmodified.
Both installed Qoder Global and QoderCN CLIs reported version 1.1.66.

## Automated checks

All of the following completed successfully:

```sh
# CPA-Qoder/
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
make build server
python3 scripts/check_host.py
python3 scripts/smoke.py --fixture

# CLIProxyAPI/
go test -race ./internal/pluginhost -count=1
git diff --check
```

The sandbox runs used temporary Go caches under `/tmp` and `GOPROXY=https://goproxy.cn,direct`, because the default module proxy timed out. These environment settings are not required by the plugin itself.

Regression coverage includes account and model namespace separation, missing credentials, complete text history, unsupported request rejection, explicit stdin, environment isolation, process cancellation during both discovery and silent generation, native tool handoff, empty argument deltas, truncated/error output, host wire method names, JSON stream chunks, terminal-chunk ordering, worker drain and cancellation while waiting for stream consumption. Proxy configuration is verified at the subprocess boundary for discovery and generation.

`python3 scripts/check_host.py` loads the native library into the original CPA loader using a temporary Go test overlay. It fills the real sixteen-slot stream queue, withholds the consumption receipt, and calls native shutdown. The original loader rejects all callbacks after closing the instance; the plugin cancels its local wait and returns without leaving callbacks behind. The overlay adds only a test in temporary storage; it does not edit CPA source.

The fixture smoke repeats both Global and CN paths on the unmodified host: model registration, ordinary and streamed completions, 64 reasoning deltas per response, native function-call handoff, tool-result followup and disconnect cleanup. It uses dummy auth files and a local CLI protocol fixture.

## Earlier real account integration

`python3 scripts/smoke.py` starts the built CPA with its native plugin on a temporary localhost port. It copies only the encrypted CLI login files into private temporary roots and removes them afterward, including on setup failure. Test-only wrappers record child PIDs without printing credentials or modifying either installed CLI.

These live-account results were recorded before the stream transport change. The current transport was rechecked with fixtures on the original host; live quota was not used for that recheck.

The account catalog advertised 17 Global models and one CN model during this run. Actual model permissions can change. Real generation tests selected `Qwen3.8-Flash` from each namespace:

| Check | `qoder/Qwen3.8-Flash` | `qoder-cn/Qwen3.8-Flash` |
|---|---|---|
| Non-stream completion returns `OK` | Passed | Passed |
| Text SSE reconstructs `OK`, with one DONE marker | Passed | Passed |
| Native `weather` call returns `finish_reason=tool_calls` | Passed | Passed |
| Streamed weather arguments reconstruct valid JSON | Passed | Passed |
| Zero-argument `ping` stream reconstructs `{}` | Passed | Passed |
| External tool result is used in the next answer | Passed | Passed |
| HTTP disconnect terminates and reaps the actual CLI child | Passed | Passed |

The helper never implemented either function. The external test client supplied the weather result. Both observed token usage reports were zero; the plugin preserves those upstream values.

## Review and limits

Independent design and code reviews were completed. Findings concerning worker drain, empty streamed arguments and proxy propagation were resolved, together with integration findings concerning SSE framing, interceptor names, terminal ordering and plugin-side stream consumption control.

Verified platform: macOS arm64. Linux/Windows process handling and other CPA entry-protocol translations were not exercised end to end. Only the two listed models received real generation tests; the catalog lists the remaining models without claiming they were all invoked. Real proxy connectivity, token expiry and long-running load were not simulated.

The transport reuses the official CLI login, refresh and signing behavior. It does not implement Qoder's private HTTP protocol. History and external tool results are role-labelled JSON text; their native upstream message semantics are not identical. Unsupported controls and modalities are explicitly rejected. Agent planning and tool execution remain the responsibility of the external CPA client.
