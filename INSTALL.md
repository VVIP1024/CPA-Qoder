# 在 Docker 版 CPA 中安装 CPA-Qoder

本目录是可直接挂载的 Linux 发布包，CPA 服务器不需要安装 Go 或保留插件源码。

CPA 容器内必须已经安装 Linux 版 `qoder` 和 `qodercn`。两套 CLI 的登录目录必须可写并持久化，否则 CLI 无法刷新凭证。

按以下路径挂载发布包文件：

| 发布包文件 | 容器内路径 |
|---|---|
| `plugins/cpa-qoder.so` | `/CLIProxyAPI/plugins/cpa-qoder.so` |
| `bin/cpa-qoder-helper` | `/CLIProxyAPI/plugin-bin/cpa-qoder-helper` |
| `auths/qoder.json` | `<现有 oauth.auth-dir>/qoder.json` |
| `auths/qodercn.json` | `<现有 oauth.auth-dir>/qodercn.json` |

官方 Docker 默认认证目录是 `/root/.cli-proxy-api`。请继续使用 CPA 当前已配置的目录，把两份账号描述放到该目录，避免原有账号从列表中消失。

持久化 `/root/.qoder` 和 `/root/.qoder-cn` 两个登录目录。将 `config.example.yaml` 合并到 CPA 现有配置后重启 CPA。保留原有的服务端口、访问密钥、认证目录和其他插件设置。

默认配置要求以下命令可执行：

```text
/usr/local/bin/qoder
/usr/local/bin/qodercn
```

重启后查询 `/v1/models`。模型 ID 是两套 CLI 返回的原始名称。如果两个账号提供同名模型，CPA 无法仅凭模型 ID 唯一路由；不要同时启用提供该重名模型的账号。
