# Changelog

All notable changes to this project will be documented in this file.

## [1.0.0] - 2026-05-24

First release: **Kibana-only log query CLI** for humans and AI Agents.

### Features

- Log search and terms aggregation via **Kibana Console Proxy** (HTTP Basic only)
- OS credential store by default; env vars for CI/agents (`KIBANA_CLI_*`)
- Commands: `auth`, `context`, `doctor`, `config`, `patterns list|fields`, `search`, `agg`, `reference`
- Unified **`--json` Agent contract** — same `AgentStatus` envelope for bootstrap, validation, and API errors (stdout)
- `context` / `doctor` bootstrap with semantic exit codes (`0`/`2`/`3`/`4`/`5`/`6`/`7`)
- `field-map.yaml` for cross-index logical service names (`--profile`, `--service`)
- `search` / `agg`: `--data-view`, `--level`, `--trace-id`, `--msg-only`, `--dry-run`
- Optional index allowlist: `KIBANA_CLI_ALLOWED_INDEX_PREFIXES`
- Global flags: `--json`, `--quiet`, `--timeout`, `--insecure`, `--dry-run`, `--force`
- Agent skill: `skills/kibana-cli/SKILL.md` (`npx skills add fatecannotbealtered/kibana-cli`)

### Environment

- `KIBANA_CLI_HOST` — Kibana base URL (no path)
- `KIBANA_CLI_USER` / `KIBANA_CLI_PASSWORD`
- Optional: `KIBANA_CLI_KIBANA_VERSION`, `KIBANA_CLI_TIMEOUT`, `KIBANA_CLI_INSECURE`, `KIBANA_CLI_ALLOWED_INDEX_PREFIXES`

[1.0.0]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.0
