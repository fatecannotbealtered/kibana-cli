# Live Smoke Evidence

Recorded live smoke for `release_readiness.required_evidence:
recorded_live_smoke_for_stable`, run against a real Elasticsearch + Kibana
7.10.2 instance with X-Pack security enabled.

- **Date:** 2026-06-13
- **Environment:** Docker `docker.elastic.co/{elasticsearch,kibana}:7.10.2`,
  single-node, `xpack.security.enabled=true`; Kibana on `localhost:5601`,
  HTTP Basic auth as `elastic`.
- **Fixture data:** index `logs-test` seeded with 5 docs (`@timestamp`, `msg`,
  `level`, `traceId`; 3 `info` / 2 `error`) plus a matching Kibana index
  pattern with `@timestamp` as the time field.
- **Method:** each leaf invoked with `--format json`; envelope `ok`/`error`
  asserted. Credentials persisted via `auth login` into the OS keyring.

## Result — all commands PASS

| Command | Result | Notes |
|---|---|---|
| `auth login` (dry-run → confirm) | PASS | `authMode: basic`, `credentialStore: keyring`, `searchReachable: true`; the `/_security/_authenticate` probe requires a security-enabled Kibana |
| `auth status` | PASS | `configured: true` |
| `doctor` | PASS | "Ready for log search as elastic on Kibana 7.10.2" |
| `context` | PASS | |
| `patterns list` | PASS | 1 pattern, `_untrusted: [patterns]` |
| `patterns fields --index logs-test` | PASS | 12 fields with es types |
| `search --index logs-test --from now-1h --fields ...` | PASS | 3 hits, per-hit `_untrusted` tags |
| `agg --index logs-test --terms level --from now-1h` | PASS | buckets `info:3`, `error:2` — matches seeded data |
| `changelog` | PASS | |
| `auth logout` (dry-run → confirm) | PASS | clears config + keyring |

## 2026-06-14 — v1.1.3 new commands (live)

Re-run against a live Elasticsearch + Kibana **7.10.1** instance (Docker
`tools-e2e-{elasticsearch,kibana}`) holding real multi-million-doc log indices
(e.g. `iot-3.0-test-log-*`). Each new leaf invoked with `--compact`; envelope
asserted.

| Command | Result | Notes |
|---|---|---|
| `search --dsl '{"query":{"match_all":{}},"size":1}'` | PASS | raw ES `_search` passthrough; hits returned with per-hit `_untrusted` tags |
| `search --index iot-3.0-test-log-* --search-after <token>` | PASS | returned `next_search_after` cursor (base64 sort), pages stably |
| `agg --agg-type date_histogram --interval 1d --from now-30d` | PASS | per-day buckets with real counts (e.g. `2026-06-08: 12.2M`) |
| `agg --terms service_name --metric count` | PASS | top services by doc count (`ota-server: 56M`, …) |
| `objects list --type index-pattern` | PASS | 7 saved index-patterns, `_untrusted: [objects]` |
| `objects get --type index-pattern --id metrics-*` | PASS | full saved-object, `_untrusted: [title,description,object]` |

All v1.1.3 new commands are live-verified.

## 2026-07-17 — Discover/KQL parity and query provenance

Re-run against a configured real Kibana **7.7.1** test deployment. Evidence is
sanitized: no hostname, account, credentials, returned log body, or raw hit is
recorded here. Commands used one fixed absolute 24-hour window and returned only
count/provenance summaries.

| Check | Result | Sanitized evidence |
|---|---|---|
| Data-view phrase query in the intended test context | PASS | `total: 32`; resolved data view/index, `timeField: @timestamp`, and `queryLanguage: kql` were reported |
| Same index/query/window in a different configured test context | PASS | `total: 0`; output identified the different context |
| `msg:"1" and msg:"2"` in KQL | PASS | exact total matched both equivalents below and was greater than 10,000 |
| Uppercase Lucene equivalent | PASS | same exact total as KQL |
| Explicit Elasticsearch bool DSL equivalent | PASS | same exact total as KQL |
| `agg` KQL and Lucene equivalents | PASS | same exact total as search; `track_total_hits: true` avoids the 10,000 cap |
| Search/agg dry-run | PASS | resolved provenance plus complete initial `dsl`; no `_search` issued by dry-run |
| Lowercase KQL-like boolean under default Lucene | PASS | `E_VALIDATION`, exit 2, non-retryable |
| Unquoted expression split into positional args | PASS | `E_VALIDATION`, exit 2, non-retryable |

This verifies the reported count discrepancy was reproducible from both query
language and context selection, and that the repaired CLI makes both inputs
explicit while matching the equivalent Elasticsearch bool query.

### Credential-at-rest

- After `auth login`, the configured password does **not** appear in any file
  under the config home (keyring-backed). Verified by grep.
- After `auth logout`, `auth status` returns `E_CONFIG` (not configured).

### Error taxonomy

| Path | Result |
|---|---|
| `search --index nonexistent` | `E_NOT_FOUND` |
| `patterns fields --index nope` | `E_NOT_FOUND` |
| `search` with no `--index` | `E_VALIDATION` |
| `auth status` when logged out | `E_CONFIG` |

## Note on the test environment

The kibana-cli auth flow requires a **security-enabled** Kibana (it validates
via `/_security/_authenticate`). The shared dev ES/Kibana instance originally
ran with security disabled; for this smoke it was recreated with
`xpack.security.enabled=true`, the `elastic` bootstrap password set via
`ELASTIC_PASSWORD`, and `kibana_system`'s password set through the security
API. This is the configuration kibana-cli targets in production.

## Reproduce

```bash
export KIBANA_CLI_HOST=http://localhost:5601
kibana-cli auth login --host "$KIBANA_CLI_HOST" --user elastic --password '...' --dry-run
kibana-cli auth login --host "$KIBANA_CLI_HOST" --user elastic --password '...' --confirm <token>
kibana-cli doctor --compact
kibana-cli patterns list --compact
kibana-cli search --index 'logs-test' --from now-1h --limit 1 --fields '@timestamp,msg,level' --compact
kibana-cli agg --index 'logs-test' --terms level --from now-1h --compact
```
