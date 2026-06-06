# kibana-cli

[![CI](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![npm version](https://img.shields.io/npm/v/@fatecannotbealtered-/kibana-cli.svg)](https://www.npmjs.com/package/@fatecannotbealtered-/kibana-cli)

[English](README.md) | 中文

面向人类与 AI Agent 的 **Kibana 日志查询 CLI**。所有查询经 **Kibana Console Proxy**（HTTP Basic）。适用于内网托管 ELK（如 Kibana 7.7+）。

## 为什么需要 kibana-cli？

团队往往只有 **Kibana 地址** 可读日志。`kibana-cli` 把高频 **search / agg** 封装成默认 JSON、Agent 友好的契约，沿用 [`jira-cli`](https://github.com/fatecannotbealtered/jira-cli) 与 [`gitlab-cli`](https://github.com/fatecannotbealtered/gitlab-cli) 的约定：

- **默认 JSON 契约** — 所有 JSON 响应都是单个 envelope，包含 `ok`、`schema_version`、`data` 或 `error`、`meta`（全部 stdout）
- **`field-map.yaml`**（可选）— 跨索引统一逻辑服务名；`index_rules` 见 `field-map.example.yaml`
- **`--data-view`** — 用 Kibana 数据视图 ID 解析索引模式
- **`--dry-run` + `--confirm`** — 写操作先预览并返回确认 token；search/agg dry-run 不发起 Kibana API 请求（含 `--data-view`：预览中使用占位索引）
- **`update`** — 检查 GitHub Releases；独立二进制在校验 checksum 后自更新，npm / Go 管理的安装返回对应包管理命令
- **`--force`** — 在 `config init` 时覆盖已有 `field-map.yaml`
- **`--insecure` / `--timeout`**
- **机器可读错误** — `error.code`、`error.message`、`error.details`、`error.retryable`
- **语义化退出码**（`0`-`8`）
- **`SKILL.md`** — `npx skills add fatecannotbealtered/kibana-cli`

## 安装

### 快速开始

将下面整段复制发给您的 Agent（或自行执行）：

```bash
# 请帮我安装 kibana-cli，并记住以后查 Kibana 日志、做日志聚合都使用 kibana-cli。输出默认是 JSON。
# 安装 CLI
npm install -g @fatecannotbealtered-/kibana-cli

# 安装 CLI Skill（必需）— 复制到你支持的 skills 目录下
npx skills add fatecannotbealtered/kibana-cli -y -g

# 配置（CI/Agent 推荐环境变量；也可用 auth login 的 dry-run/confirm 流程）
export KIBANA_CLI_HOST=https://kibana.example.com
export KIBANA_CLI_USER=dev_ro
export KIBANA_CLI_PASSWORD='...'

# 验证
kibana-cli context
```

```powershell
# Windows PowerShell
$env:KIBANA_CLI_HOST = "https://kibana.example.com"
$env:KIBANA_CLI_USER = "dev_ro"
$env:KIBANA_CLI_PASSWORD = "..."

kibana-cli context
```

CLI 包提供 `kibana-cli` 命令；Skill 会让兼容的 AI 编程助手知道如何安全地调用它。

更习惯保存登录？凭据默认写入**操作系统凭据库**。写操作非交互执行，必须先 dry-run 再 confirm：

```bash
kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --dry-run
kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --confirm <confirm_token>
kibana-cli context
```

### 其他安装方式

```bash
# Go install
go install github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli@v1.1.0
```

或从 [GitHub Releases](https://github.com/fatecannotbealtered/kibana-cli/releases) 下载二进制文件并添加到 PATH。

## 更新

```bash
kibana-cli update --check
kibana-cli update --dry-run
kibana-cli update --confirm <confirm_token>
```

`update` 会检查 GitHub Releases。独立 Unix 二进制会在通过 `checksums.txt` SHA256 校验并携带 `--confirm` 后原地替换；如果 CLI 由 npm 或 Go 管理，则不会直接修改包管理器管理的文件，而是返回应执行的命令，例如 `npm install -g @fatecannotbealtered-/kibana-cli@1.1.0` 或 `go install github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli@v1.1.0`。

## 鉴权

**仅支持 HTTP Basic**。CI/Agent 优先用环境变量，避免在 argv 传 `--password`。

```bash
kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --dry-run
kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --confirm <confirm_token>
kibana-cli context
kibana-cli auth status
```

默认密码在**操作系统凭据库**；`config.json` 不含明文密码。

| 变量 | 说明 |
|------|------|
| `KIBANA_CLI_HOST` | Kibana 根 URL |
| `KIBANA_CLI_USER` / `KIBANA_CLI_PASSWORD` | Basic 鉴权 |
| `KIBANA_CLI_KIBANA_VERSION` | 可选，跳过自动探测 |
| `KIBANA_CLI_INSECURE` | `1` 或 `true` 跳过 TLS 校验 |
| `KIBANA_CLI_TIMEOUT` | HTTP 超时秒数（默认 60） |
| `KIBANA_CLI_ALLOWED_INDEX_PREFIXES` | 可选，逗号分隔前缀；索引模式须**以其中某一前缀开头** |

### 退出码

| 码 | 含义 |
|----|------|
| 0 | 成功 |
| 1 | 通用错误 |
| 2 | 参数 / 用法错误 |
| 3 | 资源不存在 |
| 4 | 鉴权 / 权限失败 |
| 5 | 需要确认 token |
| 6 | 前置条件冲突 |
| 7 | 可重试瞬时错误（网络 / 限流 / 服务端） |
| 8 | 超时 |

## 命令

> 人类可读完整命令树：`kibana-cli reference --format text`

```bash
kibana-cli auth login|logout|status
kibana-cli context
kibana-cli doctor
kibana-cli config init|show
kibana-cli patterns list|fields
kibana-cli search --index 'app-test-log-*' --level ERROR
kibana-cli search --data-view <uuid> --query 'timeout'
kibana-cli agg --index 'app-test-log-*' --terms level --from now-1h
kibana-cli update --check
```

`search` 默认 `--from now-15m`（不写 `--from` 即使用该时间窗）。

可选 `~/.kibana-cli/field-map.yaml`（`kibana-cli config init`）。`profiles` 与按索引匹配的 `index_rules` 见仓库内 `field-map.example.yaml`。

输出标志：`--format json|text|raw`（默认 `json`）、`--compact`（仅影响 JSON）、`--quiet`（只压制 text 的辅助输出）、`--json`（兼容别名，等价于 `--format json`）。`--fields` 只作用于 JSON 输出；命令不支持某种格式时会明确报参数错误。

其他全局标志：`--dry-run`、`--confirm`、`--force`（`config init` 覆盖已有 field-map）、`--timeout`、`--insecure`（或 `KIBANA_CLI_INSECURE=1` / `true`）。

### Agent 工作流

```text
kibana-cli context                 # 鉴权 + 日志检索可达性（先读顶层 ok）
kibana-cli patterns fields         # 字段发现
kibana-cli search ...              # 查日志
kibana-cli agg ...                 # 聚合统计
```

JSON 输出中，成功数据在 `data`；失败详情在 `error.details`。

## License

MIT
