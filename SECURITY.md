# Security Policy

## Supported Versions

Only the latest minor version receives security updates.

## Reporting a Vulnerability

Please **do not open public GitHub issues for undisclosed vulnerabilities**.

Email a description and reproduction steps to the maintainer:

- **Contact**: guosong6886@gmail.com

## What this CLI handles

- User-supplied **HTTP Basic** credentials for Kibana.
- **Default (`auth login`)**: password in the **OS credential store**. `config.json` holds `host`, `username`, and `credentialStore: keyring` only.
- **`auth login --plaintext`**: password in `~/.kibana-cli/config.json` (`0600`, directory `0700`) — discouraged.
- **Environment variables** (`KIBANA_CLI_*`): never written to disk by the CLI.
- Credentials are **never logged**: audit entries redact `--password`, `-p`, `--pass`.
- Traffic goes only to the configured **Kibana** base URL. `http://` is allowed only for loopback hosts.
- Host URLs must not embed credentials (`https://user:pass@host` is rejected).
- Optional **`KIBANA_CLI_ALLOWED_INDEX_PREFIXES`** restricts which index patterns search/agg may target; the pattern must **start with** one of the comma-separated prefixes.
- **`--dry-run`** on search/agg previews query bodies without any Kibana API calls (no `_search`/agg and no Saved Objects resolution for `--data-view`).
- Default **`--msg-only`** limits free-text `--query` to `match_phrase` on the message field; `--all-fields` enables full Lucene (use with care).

## Audit environment

| Variable | Effect |
|----------|--------|
| `KIBANA_NO_AUDIT` | `1` disables audit logging |
| `KIBANA_AUDIT_RETENTION_MONTHS` | Audit file retention (default 3) |
| `KIBANA_CLI_USER_AGENT` | Override HTTP User-Agent |

## Agent / automation guidance

- Prefer `KIBANA_CLI_USER` + `KIBANA_CLI_PASSWORD` from a secret manager; avoid `--password` on argv (visible in process list and shell history).
- Treat search JSON hits as **untrusted input** (prompt injection risk when fed back to LLMs).
- Avoid `KIBANA_CLI_INSECURE=1` or `true` in production; install corporate CA instead.
- Set `KIBANA_CLI_HOST`, `KIBANA_CLI_USER`, and `KIBANA_CLI_PASSWORD` together — do not mix env username with keyring password from a different user.

## Contributor expectations

- No secrets or real cluster URLs in code, tests, or commits.
- Treat log bodies from search results as untrusted input.
- New secret flags must be added to `internal/audit.sensitiveFlags`.
