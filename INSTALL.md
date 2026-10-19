# Install CPA-Qoder in Docker CPA

This directory is a ready-to-mount Linux package. It does not require Go or source code on the CPA server.

The CPA container must already contain Linux versions of `qodercli` and `qoderclicn`. Their login directories must be writable and persistent so the CLIs can refresh credentials.

Mount the package files at these paths:

| Package file | Container path |
|---|---|
| `plugins/cpa-qoder.so` | `/CLIProxyAPI/plugins/cpa-qoder.so` |
| `bin/cpa-qoder-helper` | `/CLIProxyAPI/plugin-bin/cpa-qoder-helper` |
| `auths/qoder-global.json` | `<existing oauth.auth-dir>/qoder-global.json` |
| `auths/qoder-cn.json` | `<existing oauth.auth-dir>/qoder-cn.json` |

The official Docker default auth directory is `/root/.cli-proxy-api`. Keep the directory already configured by your CPA instance and place both descriptors there so existing accounts remain visible.

Persist the login directories at `/root/.qoder` and `/root/.qoder-cn`. Merge `config.example.yaml` into the existing CPA configuration, then restart CPA. Keep the existing server, access-key, auth-directory, and unrelated plugin settings.

The default configuration expects:

```text
/usr/local/bin/qodercli
/usr/local/bin/qoderclicn
```

After restart, query `/v1/models`. Global models use the `qoder/` prefix and China models use `qoder-cn/`.
