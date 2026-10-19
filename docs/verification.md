# Verification record

Date: 2026-10-19. Local host: macOS arm64. Plugin SDK: CPA v8.0.0, native ABI 1, RPC schema 6.

## Independent repository check

The repository was copied to a new temporary directory with no adjacent CPA checkout. From that directory, a clean module cache completed:

```sh
go test -race ./...
go vet ./...
go build -buildmode=c-shared .
go build .
```

The build resolved `github.com/router-for-me/CLIProxyAPI/v8 v8.0.0` from the module proxy. `go.mod` contains no local `replace`, and the checksum entries are committed in `go.sum`.

The repository was also scanned for parent-directory CPA references, local module replacements, old host-build commands, and references to other plugin repositories. None remain.

## Host compatibility fixture

A current CPA executable was built into `/tmp`; its source tree was not edited. The plugin was then built locally and tested with:

```sh
python3 scripts/smoke.py --fixture --cpa-binary /tmp/cli-proxy-api-qoder-test
```

The fixture passed for both `qoder/Qwen3.8-Flash` and `qoder-cn/Qwen3.8-Flash`:

- model discovery and namespace separation;
- non-streaming and SSE responses;
- reasoning deltas;
- native function calls with arguments and an empty argument object;
- external tool-result follow-up;
- cancellation and child-process cleanup after an HTTP disconnect.

Unit and race coverage also exercises missing credentials, unsupported inputs, environment isolation, discovery and generation cancellation, truncated CLI output, stream ordering, backpressure, worker drain, and shutdown while a stream is waiting for consumption.

## Linux release packages

`make release-linux-amd64` and `make release-linux-arm64` run inside `golang:1.26-bookworm`, force the requested Linux platform, and write a fixed `.so` name. The packaging step checks that the library is non-empty and the helper is executable before creating the archive and SHA-256 file.

The GitHub Actions workflow builds both architectures on Ubuntu with QEMU enabled. A tag matching `v*` publishes both archives and checksums to the matching GitHub Release; a manual run keeps them as workflow artifacts.

The local sandbox could not start Apple virtualization, so no local Linux archive was produced during this review. The workflow performs the real Linux compilation before any release asset is published.

## Limits

The fixture validates the CPA/plugin boundary without consuming a Qoder account quota. Live deployment still requires Linux installations of both Qoder CLIs and valid login directories inside the CPA container.

The public CLI is text-oriented. Multi-turn messages are replayed as role-labelled text. Image input, forced tool choice, strict structured output, and unsupported generation controls are rejected rather than silently changed.
