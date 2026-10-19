# CPA-Qoder

[English](README.md) | 简体中文

CPA-Qoder 是 CPA 的原生模型插件。它调用已经登录的 Qoder 国际版和 QoderCN CLI，把可用模型注册为 `qoder/*` 和 `qoder-cn/*`，并通过 CPA 暴露对话、流式输出和函数调用。

插件只负责模型能力。工具执行、规划和 Agent 循环由接入 CPA 的客户端完成。

## 兼容性和仓库边界

- CPA 原生插件 C ABI 1、RPC schema 6；已按 CPA v8.0.0 的公开 SDK 构建。
- 发布包面向官方 Docker 运行环境所用的 Debian Bookworm/glibc，提供 `linux/amd64` 和 `linux/arm64` 两种架构。
- 同时支持 `qodercli` 和 `qoderclicn`，两套账号和模型命名空间互不混用。
- 仓库没有 Git 子模块、本地 `replace`、相邻源码目录引用或宿主源码补丁。构建时从 Go 模块仓库下载发布版 SDK；安装后的容器不需要 Go、插件源码或 CPA 源码。

## 构建发布包

构建机只需 Docker，不需要安装 Go：

```sh
# 常见的 x86_64 服务器
make release-linux-amd64

# ARM64 服务器
make release-linux-arm64
```

输出位于 `dist/`：

```text
dist/
├── cpa-qoder-linux-amd64.tar.gz
├── cpa-qoder-linux-amd64.sha256
└── cpa-qoder-linux-amd64/
    ├── plugins/cpa-qoder.so
    ├── bin/cpa-qoder-helper
    ├── auths/qoder-global.json
    ├── auths/qoder-cn.json
    ├── config.example.yaml
    └── INSTALL.md
```

默认 `make` 等同于构建 amd64 发布包。动态库和 helper 在 `golang:1.26-bookworm` 容器中编译，目标平台由 Makefile 明确指定，不受构建机架构影响。

## 安装到 Docker 版 CPA

发布包包含插件、helper、账号描述和配置片段，不包含 Qoder CLI 与登录凭证。CPA 容器内还需要 Linux 版 `qodercli`、`qoderclicn`，并为两套登录目录提供持久化存储。推荐在现有 CPA 镜像之上安装两套 CLI，登录后保留 `/root/.qoder` 和 `/root/.qoder-cn`。

在服务器解压与其 CPU 架构一致的发布包：

```sh
tar -xzf cpa-qoder-linux-amd64.tar.gz
```

将以下挂载加入 CPA 的 Compose 配置。示例假定 CPA 工作目录为 `/CLIProxyAPI`：

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

如果 CLI 没有装进镜像，还要把它们的完整 Linux 安装目录挂入容器，并保证下面两个路径可执行：

```text
/usr/local/bin/qodercli
/usr/local/bin/qoderclicn
```

把发布包里的 `config.example.yaml` 合并到 CPA 现有配置。不要覆盖 CPA 的服务端口、访问密钥或其他插件配置。关键部分如下：

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
  configs:
    cpa-qoder:
      enabled: true
      helper: "/CLIProxyAPI/plugin-bin/cpa-qoder-helper"
      global:
        cli: "/usr/local/bin/qodercli"
        config-dir: "/root/.qoder"
        disabled: false
      cn:
        cli: "/usr/local/bin/qoderclicn"
        config-dir: "/root/.qoder-cn"
        disabled: false
```

两份账号描述必须放入 CPA 已有的 `oauth.auth-dir`。上面的 Compose 示例按官方 Docker 默认目录 `/root/.cli-proxy-api` 挂载；如果现有配置使用其他目录，请同时修改挂载目标。不要为了插件新建或替换 `oauth.auth-dir`，否则 CPA 读取不到原有账号。

首次登录可在带持久化目录的 CPA 容器中执行：

```sh
docker exec -it <cpa-container> qodercli login
docker exec -it <cpa-container> qoderclicn login
```

重启 CPA 后查询 `/v1/models`。国际版模型使用 `qoder/<模型名>`，国内版使用 `qoder-cn/<模型名>`。只有一套账号时，删除另一份账号描述文件，并把对应配置的 `disabled` 设为 `true`。

## 功能

| 能力 | 行为 |
|---|---|
| `AuthProvider` | 读取两份账号描述，检查 CLI 登录文件；凭证、签名和刷新仍由官方 CLI 处理。 |
| `ModelProvider` | 分别调用两套 CLI 的 `--list-models`，缓存五分钟并注册两个模型命名空间。 |
| `ProviderExecutor` | 支持 Chat Completions、普通输出、SSE 流式输出、reasoning delta 和原生函数调用交接。每次推理使用独立 CLI 进程。 |
| `RequestInterceptor` | 为匹配的 Qoder 请求传递 CPA 请求 ID，便于关联取消和完成事件。 |
| `RequestLifecyclePlugin` | CPA 报告完成、失败或取消时终止仍在运行的 CLI 子进程。 |
| `ResponseTranslator` | 把插件内部输出转为 CPA 客户端协议，并对流数据施加背压。 |

`proxy-url` 可为模型发现和推理指定代理；留空时继承容器中的 `HTTP_PROXY`、`HTTPS_PROXY` 和 `ALL_PROXY`。插件不输出或保存解密后的 token。

## CPA 加载插件的生命周期

1. CPA 扫描 `plugins.dir`，根据 `plugins.configs.cpa-qoder.enabled` 选择 `cpa-qoder.so`。
2. CPA 打开动态库，调用 `cliproxy_plugin_init`，校验 C ABI 并交换调用、内存释放和关闭回调。此时不会启动 Qoder CLI。
3. CPA 调用 `plugin.register`。插件读取 YAML 配置，创建 Provider，返回元数据、配置字段和能力清单。
4. CPA 从 `oauth.auth-dir` 读取账号描述，通过 `auth.parse` 建立 Global 和 CN 认证记录。
5. CPA 调用 `model.for_auth`。插件使用对应 CLI 发现模型，并把模型绑定到正确的账号记录。
6. 请求到达后，CPA 完成协议转换和账号选择，再调用 `executor.execute` 或 `executor.execute_stream`。插件启动对应 CLI；函数调用返回给外部 Agent 执行。
7. CPA 的完成或取消事件会清理进程和临时目录。重新加载配置时，旧 Provider 停止接收请求并等待现有任务退出。
8. 停用插件或关闭 CPA 时，宿主停止新调用，等待原生调用结束，再执行插件关闭回调并卸载动态库。

## 可增加的 CPA 扩展点

| 扩展点 | 可增加的内容 |
|---|---|
| `ManagementAPI` / `ManagementHandler` | 展示两套 CLI 的版本、登录状态、模型发现错误和缓存刷新入口。 |
| `QuotaProvider` | 在 CLI 提供稳定查询接口后展示各区域额度。当前 token 用量不能代表剩余额度。 |
| `UsagePlugin` | 按区域和模型汇总 CPA 已完成请求的使用记录。 |
| `AuthProvider` + `Scheduler` | 支持同一区域多账号，并按可用性或额度调度。 |
| `ModelRouter` | 增加稳定别名和显式路由规则，同时保留账号与命名空间检查。 |
| `RequestTranslator` / `ResponseNormalizer` | 在 CLI 支持相应语义后补充图片、结构化输出等字段转换。 |
| `CommandLinePlugin` | 增加登录检查、模型刷新和账号描述生成命令。 |

这些扩展目前没有实现。新增能力需要同时声明 capability、实现 RPC 方法并补充宿主兼容测试。

## 开发和验证

本机开发构建需要 Go 1.26 和 C 编译器：

```sh
make build
make test
```

端到端测试使用外部 CPA 可执行文件，不读取相邻源码目录：

```sh
python3 scripts/smoke.py --fixture --cpa-binary /path/to/cli-proxy-api
```

真实账号测试去掉 `--fixture`，会使用当前用户的两套 CLI 登录并产生模型请求。更多信息见 [架构说明](docs/architecture.md) 和 [验证记录](docs/verification.md)。

当前公开 CLI 以文本和流式 JSON 为主。多轮消息会转成带角色标记的文本；图片、强制工具选择、严格结构化输出和部分生成参数暂不支持。
