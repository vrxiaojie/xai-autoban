# xai-autoban

`xai-autoban` 是一个 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 原生插件，用于自动隔离持续返回错误的 xAI OAuth 凭据，避免 CPA 在大号池中逐个重试无效账号而显著拉长首 token 时间。

## 功能

| 上游状态 | 默认处理 |
| --- | --- |
| `401` | 通过 Management API 停用 24 小时 |
| `402` | 通过 Management API 停用 24 小时 |
| `403` | 通过 Management API 停用 24 小时 |

- 只处理 `xai` provider，不影响 Codex、Claude、Gemini 等其他凭据。
- 错误发生后立即在 CPA 调度阶段跳过该凭据，后台再调用网页 Management API 真正停用账号。
- 24 小时到期后调用 Management API 重新启用账号；失败时自动重试。
- 状态落盘，CPA 重启后仍会继续执行未完成的停用和恢复任务。
- Management Key 错误时启用全局冷却，避免连续错误请求触发 CPA 的管理接口 IP 封禁。
- 可通过 `status-codes` 增加 `429` 或其他 HTTP 状态码。
- 提供实时统计、搜索、筛选、分页和批量解禁界面。
- 支持单个、选中项、按状态码和全部解禁。
- 保留带 CPA 管理密钥保护的 Management API。

## 构建

构建脚本使用官方 Debian Go 镜像，需要 Docker，并会运行测试后分别编译 Linux arm64 和 amd64：

```bash
bash build.sh
```

产物：

```text
dist/xai-autoban-linux-arm64.so
dist/xai-autoban-linux-amd64.so
```

## 安装

按服务器架构复制并重命名动态库：

```text
amd64: plugins/linux/amd64/xai-autoban.so
arm64: plugins/linux/arm64/xai-autoban.so
```

CPA 必须启用 Management API，并配置管理密钥：

```yaml
remote-management:
  allow-remote: false
  secret-key: "你的管理密钥"
```

推荐将同一个明文密钥通过环境变量传给 CPA 进程：

```bash
export CPA_MANAGEMENT_KEY='你的管理密钥'
```

然后在 `config.yaml` 中启用插件：

```yaml
plugins:
  enabled: true
  configs:
    xai-autoban:
      enabled: true
      priority: 200
      management-url: http://127.0.0.1:8317
      management-key-env: CPA_MANAGEMENT_KEY
      disable-hours: 24
      status-codes: [401, 402, 403]
      request-timeout-seconds: 10
      retry-interval-seconds: 60
      auth-failure-cooldown-seconds: 600
      state-file: data/xai-autoban-state.json
```

也可以使用 `management-key` 直接配置密钥，但不建议把明文密钥提交到 Git。`state-file` 所在目录必须允许 CPA 进程写入。

重启 CLIProxyAPI 后，日志应包含：

```text
pluginhost: plugin registered plugin_id=xai-autoban plugin_name=xai-autoban version=1.0.0
```

## 管理面板

```text
http://<CPA_HOST>:8317/v0/resource/plugins/xai-autoban/status
```

面板不需要 CPA 管理密钥，可以查看凭据标识并执行解禁。请只在受信网络中开放该路径，或在反向代理层增加访问控制。

公开 Resource API：

```text
GET /v0/resource/plugins/xai-autoban/data
GET /v0/resource/plugins/xai-autoban/action?op=unban&auth_id=<AUTH_ID>
GET /v0/resource/plugins/xai-autoban/action?op=unban-status&status=403
GET /v0/resource/plugins/xai-autoban/action?op=unban-many&auth_ids=<ID1>,<ID2>
GET /v0/resource/plugins/xai-autoban/action?op=unban-all
```

带 CPA 管理鉴权的兼容 API：

```text
GET  /v0/management/plugins/xai-autoban/bans
POST /v0/management/plugins/xai-autoban/unban
POST /v0/management/plugins/xai-autoban/unban-all
POST /v0/management/plugins/xai-autoban/import
```

## 状态说明

- 插件只会自动恢复由自己成功停用并记录的账号。
- `401`、`402`、`403` 默认统一在首次错误后 24 小时恢复，可通过 `disable-hours` 调整。
- 凭据修复、充值或恢复订阅后，可从面板手动解禁，无需等待到期。
- 插件仅处理已经被请求命中的错误凭据，不会主动扫描整个账号池。
- 如果所有候选凭据都被插件隔离，最终行为交由 CPA 自带调度器决定。
- Management API 暂时不可用时，本地调度隔离仍然生效；面板会显示停用或恢复重试状态。

## 致谢

本项目基于 [akihitohyh/xai-autoban](https://github.com/akihitohyh/xai-autoban)，并参考 [ysxk/codex-429-autoban](https://github.com/ysxk/codex-429-autoban) 的 CPA 插件 ABI 实现思路。

## License

[MIT](LICENSE)

[linux.do](https://linux.do)
