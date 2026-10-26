# 验证记录

日期：2026-10-19。本机：macOS arm64。插件 SDK：CPA v8.0.0，原生 ABI 1，RPC schema 6。

## 独立仓库检查

仓库被复制到新的临时目录，且没有相邻的 CPA checkout。在该目录中，使用干净的模块缓存完成了：

```sh
go test -race ./...
go vet ./...
go build -buildmode=c-shared ./cmd/cpa-qoder
go build ./cmd/qoder-main
```

构建从模块代理解析 `github.com/router-for-me/CLIProxyAPI/v8 v8.0.0`。`go.mod` 不含本地 `replace`，校验和已提交到 `go.sum`。

同时扫描了父目录 CPA 引用、本地模块替换、旧宿主构建命令和其他插件仓库引用，均未残留。

## 宿主兼容 fixture

当前 CPA 可执行文件被构建到 `/tmp`，其源码树未修改。随后在本地构建插件并执行：

```sh
python3 scripts/smoke.py --fixture --cpa-binary /tmp/cli-proxy-api-qoder-test
```

fixture 使用 `qoder/Auto` 和 `qodercn/Qwen3.8-Flash` 通过验证：

- 使用带账号前缀的模型 ID 进行模型发现和按账号执行；
- 非流式和 SSE 响应；
- reasoning delta；
- 带参数及空参数对象的原生函数调用；
- 外部工具结果回传；
- HTTP 断开后的取消和子进程清理。

单元测试和竞态测试还覆盖缺少凭证、不支持的输入、环境隔离、发现与生成取消、CLI 输出截断、流顺序、背压、工作协程排空，以及流等待消费时的关闭。

## 发布包

`make package-macos` 在本机生成 macOS 包。`make release-linux-amd64` 和 `make release-linux-arm64` 在 `golang:1.26-bookworm` 中运行，强制目标 Linux 平台，并生成固定名称的 `.so`。打包步骤会检查动态库非空、helper 可执行，然后创建归档和 SHA-256 文件。

GitHub Actions 在 Ubuntu 上通过 QEMU 构建两种 Linux 架构。匹配 `v*` 的 tag 会将两个归档和校验和发布到对应 GitHub Release；手动运行则保留为 workflow artifacts。

本地沙箱无法启动 Apple 虚拟化，因此本次审查未生成 Linux 归档。正式发布资产前，workflow 会执行真实 Linux 编译。

## 限制

fixture 在不消耗 Qoder 账号额度的情况下验证 CPA/插件边界。真实部署仍要求在 CPA 容器内安装两套 Linux Qoder CLI，并提供有效登录目录。

公开 CLI 以文本为主。多轮消息按角色标记文本重放。图片输入、强制工具选择、严格结构化输出及不支持的生成参数会明确拒绝，而不是静默改写。
