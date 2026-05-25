# kibana-cli

[![CI](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![npm version](https://img.shields.io/npm/v/@fatecannotbealtered-/kibana-cli.svg)](https://www.npmjs.com/package/@fatecannotbealtered-/kibana-cli)

[English](README.md) | 中文

面向人类与 AI Agent 的 **Kibana 日志查询 CLI**。所有查询经 **Kibana Console Proxy**（HTTP Basic）。适用于内网托管 ELK（如 Kibana 7.7+）。

## 为什么需要 kibana-cli？

团队往往只有 **Kibana 地址** 可读日志。`kibana-cli` 把高频 **search / agg** 封装成 Agent 友好的 JSON 契约，沿用 [`jira-cli`](https://github.com/fatecannotbealtered/jira-cli) 与 [`gitlab-cli`](https://github.com/fatecannotbealtered/gitlab-cli) 的约定：

- **统一 JSON 契约** — bootstrap、校验、API 错误共用 `AgentStatus` 信封（全部 stdout）
- **`field-map.yaml`**（可选）— 跨索引统一逻辑服务名；`index_rules` 见 `field-map.example.yaml`
- **`--data-view`** — 用 Kibana 数据视图 ID 解析索引模式
- **`--dry-run`** — 预览 search/agg 查询体与写操作，**不发起任何 Kibana API 请求**（含 `--data-view`：预览中使用占位索引，不解析 Saved Objects）
- **`--force`** — 在 `config init` 时覆盖已有 `field-map.yaml`
- **`--insecure` / `--timeout`**
- **机器可读错误** — `ok`、`status`、`errorCode`、`statusCode`、`hint`、`exitCode`
- **语义化退出码**（`0`/`2`/`3`/`4`/`5`/`6`/`7`）
- **`SKILL.md`** — `npx skills add fatecannotbealtered/kibana-cli`

## 安装

### 快速开始

将下面整段复制发给您的 Agent（或自行执行）：

```bash
# 请帮我安装 kibana-cli，并记住以后查 Kibana 日志、做日志聚合都使用 kibana-cli（查询时加 --json）。
# 安装 CLI
npm install -g @fatecannotbealtered-/kibana-cli

# 安装 CLI Skill（必需）— 复制到你支持的 skills 目录下
npx skills add fatecannotbealtered/kibana-cli -y -g

# 配置（CI/Agent 推荐环境变量；也可 kibana-cli auth login 交互登录）
export KIBANA_CLI_HOST=https://kibana.example.com
export KIBANA_CLI_USER=dev_ro
export KIBANA_CLI_PASSWORD='...'

# 验证
kibana-cli context --json
```

```powershell
# Windows PowerShell
$env:KIBANA_CLI_HOST = "https://kibana.example.com"
$env:KIBANA_CLI_USER = "dev_ro"
$env:KIBANA_CLI_PASSWORD = "..."

kibana-cli context --json
```

CLI 包提供 `kibana-cli` 命令；Skill 会让兼容的 AI 编程助手知道如何安全地调用它。如果你是正在协助用户安装的 AI Agent，请执行同一组步骤，并在遇到交互式浏览器或终端提示时让用户配合完成。

更习惯交互式登录？凭据默认写入**操作系统凭据库**：

```bash
kibana-cli auth login --host https://kibana.example.com --user dev_ro
kibana-cli context --json
```

### 其他安装方式

```bash
# Go install
go install github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli@v1.0.1
```

或从 [GitHub Releases](https://github.com/fatecannotbealtered/kibana-cli/releases) 下载二进制文件并添加到 PATH。

## 鉴权

**仅支持 HTTP Basic**。CI/Agent 优先用环境变量，避免在 argv 传 `--password`。

```bash
kibana-cli auth login --host https://kibana.example.com --user dev_ro
kibana-cli context --json
kibana-cli auth status --json
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
| 2 | 参数错误 / 校验失败 / 未配置 |
| 3 | 鉴权失败 |
| 4 | 未找到 |
| 5 | 无权限 |
| 6 | 限流 |
| 7 | 网络 / 服务端错误 |

## 命令

> 完整命令树：`kibana-cli reference`

```bash
kibana-cli auth login|logout|status
kibana-cli context --json
kibana-cli doctor --json
kibana-cli config init|show
kibana-cli patterns list|fields --json
kibana-cli search --index 'app-test-log-*' --level ERROR --json
kibana-cli search --data-view <uuid> --query 'timeout' --json
kibana-cli agg --index 'app-test-log-*' --terms level --from now-1h --json
```

`search` 默认 `--from now-15m`（不写 `--from` 即使用该时间窗）。

可选 `~/.kibana-cli/field-map.yaml`（`kibana-cli config init`）。`profiles` 与按索引匹配的 `index_rules` 见仓库内 `field-map.example.yaml`。

全局标志：`--json`、`--quiet`、`--dry-run`、`--force`（`config init` 覆盖已有 field-map）、`--timeout`、`--insecure`（或 `KIBANA_CLI_INSECURE=1` / `true`）。

### Agent 工作流

```text
kibana-cli context --json          # 鉴权 + 日志检索可达性（先读 ok）
kibana-cli patterns fields --json  # 字段发现
kibana-cli search ... --json       # 查日志
kibana-cli agg ... --json          # 聚合统计
```

## License

MIT
