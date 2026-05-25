# kibana-cli

[![CI](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/fatecannotbealtered/kibana-cli/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![npm version](https://img.shields.io/npm/v/@fatecannotbealtered-/kibana-cli.svg)](https://www.npmjs.com/package/@fatecannotbealtered-/kibana-cli)

[English](README.md) | [中文](README_zh.md)

**Kibana log query CLI** for humans and AI Agents. All queries go through **Kibana Console Proxy** (HTTP Basic). Built for managed ELK (e.g. Kibana 7.7+).

## Why kibana-cli?

Many teams only expose a **Kibana URL** for log access. `kibana-cli` wraps high-intensity **search** and **agg** with an Agent-friendly JSON contract, following [`jira-cli`](https://github.com/fatecannotbealtered/jira-cli) and [`gitlab-cli`](https://github.com/fatecannotbealtered/gitlab-cli):

- **Unified `--json` contract** — same `AgentStatus` envelope for bootstrap, validation, and API errors (all on stdout)
- **`field-map.yaml`** — one logical service name across heterogeneous indices
- **`--data-view`** — resolve index pattern from a Kibana data view id
- **`--dry-run`** — preview search/agg queries and write actions without executing
- **`--force` / `--insecure` / `--timeout`**
- **Machine-readable error envelopes** — `ok`, `status`, `errorCode`, `statusCode`, `hint`, `exitCode`
- **Semantic exit codes** (`0`/`2`/`3`/`4`/`5`/`6`/`7`)
- **`SKILL.md`** — `npx skills add fatecannotbealtered/kibana-cli`

## Install

### Quick start

Recommended flow: install the CLI from npm, then install the AI Agent Skill with `npx skills add`.

```bash
# Install CLI
npm install -g @fatecannotbealtered-/kibana-cli

# Install CLI Skill
npx skills add fatecannotbealtered/kibana-cli -y -g

# Configure (CI / AI Agent — env vars recommended)
export KIBANA_CLI_HOST=https://kibana.example.com
export KIBANA_CLI_USER=dev_ro
export KIBANA_CLI_PASSWORD='...'

# Verify connectivity
kibana-cli context --json
```

The CLI package provides the `kibana-cli` binary; the Skill package teaches compatible AI coding assistants how to use it safely. If you are an AI Agent helping a user set this up, run the same steps and ask the user to complete any interactive browser or terminal prompts.

Prefer interactive login? Credentials are stored in the **OS credential store** by default:

```bash
kibana-cli auth login --host https://kibana.example.com --user dev_ro
kibana-cli context --json
```

### Alternative: Go install

```bash
go install github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli@v1.0.0
```

### Alternative: Download binary

Download from [GitHub Releases](https://github.com/fatecannotbealtered/kibana-cli/releases) and add to your PATH.

## Authentication

**HTTP Basic only** (Kibana username/password). Prefer env vars in CI/agents — avoid `--password` on argv.

```bash
kibana-cli auth login --host https://kibana.example.com --user dev_ro
kibana-cli context --json
kibana-cli auth status --json
```

Secrets default to the **OS credential store**; `config.json` has no plaintext password.

| Variable | Description |
|----------|-------------|
| `KIBANA_CLI_HOST` | Kibana base URL |
| `KIBANA_CLI_USER` / `KIBANA_CLI_PASSWORD` | HTTP Basic |
| `KIBANA_CLI_KIBANA_VERSION` | Optional; skip auto-detect |
| `KIBANA_CLI_INSECURE` | `1` — skip TLS verification |
| `KIBANA_CLI_TIMEOUT` | HTTP timeout seconds (default `60`) |
| `KIBANA_CLI_ALLOWED_INDEX_PREFIXES` | Optional comma-separated index allowlist |

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | OK |
| 2 | Bad args / validation / not configured |
| 3 | Auth failed |
| 4 | Not found |
| 5 | Forbidden |
| 6 | Rate limited |
| 7 | Network / server error |

## Commands

> Run `kibana-cli reference` for the full command tree.

```bash
kibana-cli auth login|logout|status
kibana-cli context --json
kibana-cli doctor --json
kibana-cli config init|show
kibana-cli patterns list|fields --json
kibana-cli search --index 'app-test-log-*' --level ERROR --from now-30m --json
kibana-cli search --data-view <uuid> --query 'timeout' --json
kibana-cli agg --index 'app-test-log-*' --terms level --from now-1h --json
```

Global flags: `--json`, `--quiet`, `--dry-run`, `--force`, `--timeout`, `--insecure`.

### Agent workflow

```text
kibana-cli context --json       # auth + log search reachability (read ok first)
kibana-cli patterns fields --json  # discover fields on an index pattern
kibana-cli search ... --json    # primary: query logs
kibana-cli agg ... --json       # count by level / service
```

## License

MIT
