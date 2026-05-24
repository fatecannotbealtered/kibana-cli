# kibana-cli

[![CI](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[English](README.md) | 中文

面向 AI Agent 的 **Kibana 日志查询 CLI**。所有查询经 **Kibana Console Proxy**（HTTP Basic）。适用于内网托管 ELK（如 Kibana 7.7+）。

## 为什么需要 kibana-cli？

团队往往只有 **Kibana 地址** 可读日志。`kibana-cli` 把高频 **search / agg** 封装成 Agent 友好的 JSON 契约，沿用 [`jira-cli`](https://github.com/fatecannotbealtered/jira-cli) 与 [`gitlab-cli`](https://github.com/fatecannotbealtered/gitlab-cli) 的约定：

- **统一 JSON 契约** — bootstrap、校验、API 错误共用 `AgentStatus` 信封（全部 stdout）
- **`field-map.yaml`** — 跨索引统一逻辑服务名
- **`--data-view`** — 用 Kibana 数据视图 ID 解析索引模式
- **`--dry-run`** — 预览 search/agg 查询与写操作，不实际执行
- **机器可读错误** — `ok`、`status`、`errorCode`、`statusCode`、`hint`、`exitCode`
- **语义化退出码**（`0`/`2`/`3`/`4`/`5`/`6`/`7`）
- **`SKILL.md`** — `npx skills add fatecannotbealtered/kibana-cli`

## 安装

```bash
npm install -g @fatecannotbealtered-/kibana-cli
npx skills add fatecannotbealtered/kibana-cli -y -g
export KIBANA_CLI_HOST=https://kibana.example.com
export KIBANA_CLI_USER=dev_ro
export KIBANA_CLI_PASSWORD='...'
kibana-cli context --json
```

## 鉴权

**仅支持 HTTP Basic**。CI/Agent 优先用环境变量，避免在 argv 传 `--password`。

| 变量 | 说明 |
|------|------|
| `KIBANA_CLI_HOST` | Kibana 根 URL |
| `KIBANA_CLI_USER` / `KIBANA_CLI_PASSWORD` | Basic 鉴权 |
| `KIBANA_CLI_KIBANA_VERSION` | 可选，跳过自动探测 |
| `KIBANA_CLI_INSECURE` | `1` 跳过 TLS 校验 |
| `KIBANA_CLI_TIMEOUT` | HTTP 超时秒数（默认 60） |
| `KIBANA_CLI_ALLOWED_INDEX_PREFIXES` | 可选，逗号分隔的索引前缀白名单 |

### 退出码

| 码 | 含义 |
|----|------|
| 0 | 成功 |
| 2 | 参数错误 / 未配置 |
| 3 | 鉴权失败 |
| 4 | 未找到 |
| 5 | 无权限 |
| 6 | 限流 |
| 7 | 网络 / 服务端错误 |

## Agent 工作流

```text
kibana-cli context --json          # 先读 ok
kibana-cli patterns fields --json  # 字段发现
kibana-cli search ... --json       # 查日志
kibana-cli agg ... --json          # 聚合统计
```

## License

MIT
