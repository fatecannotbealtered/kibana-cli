# Compatibility

kibana-cli is a **Kibana log query CLI**. All search traffic uses **Kibana Console Proxy** (`POST /api/console/proxy`).

## Verified stacks

| Environment | Kibana | Auth | Notes |
|-------------|--------|------|-------|
| Managed Kibana 7.7.x | 7.7.1 | HTTP Basic | Console Proxy + Saved Objects API |
| Generic managed ELK | 7.7 – 7.10 | HTTP Basic | Auto-detect version via `GET /api/status` |

## Configuration

```bash
export KIBANA_CLI_HOST=https://kibana.example.com   # base URL only, no /app/discover
export KIBANA_CLI_USER=<user>
export KIBANA_CLI_PASSWORD=<pass>
# Optional: skip TLS verify for corporate CA (1 or true)
export KIBANA_CLI_INSECURE=1
```

```powershell
$env:KIBANA_CLI_HOST = "https://kibana.example.com"
$env:KIBANA_CLI_USER = "<user>"
$env:KIBANA_CLI_PASSWORD = "<pass>"
$env:KIBANA_CLI_INSECURE = "true"
```

Optional `KIBANA_CLI_KIBANA_VERSION` avoids an extra status round-trip.

## Doctor checks

1. `GET /api/status` — detect Kibana version  
2. Console Proxy `GET _security/_authenticate`  
3. Console Proxy `POST */_search` with `size: 0` — log read probe

## Kibana APIs used

| API | Command |
|-----|---------|
| `GET /api/status` | Version detection |
| `POST /api/console/proxy` | `search`, `agg`, auth probe |
| `GET /api/index_patterns/_fields_for_wildcard` | `patterns fields` |
| `GET /api/saved_objects/_find?type=index-pattern` | `patterns list` |
| `GET /api/saved_objects/index-pattern/{id}` | `search --data-view`, `agg --data-view` |

## Query-language compatibility

- Lucene remains the default for backward compatibility; its boolean operators must be uppercase.
- `--query-language kql` compiles supported Kibana 7.10 KQL syntax locally to Elasticsearch DSL. There is no Kibana 7.7/7.10 server endpoint that converts KQL for the CLI.
- Metadata-independent syntax is supported: field terms/phrases, case-insensitive booleans, grouping/lists, ranges, exists, value wildcards, explicit nested groups, and escaping.
- Syntax that cannot be expanded safely without data-view field metadata, including wildcard field names other than the single all-fields `*`, fails closed with `E_VALIDATION`.
- `--data-view` resolves `title` and `timeFieldName`; a view without a time field requires explicit `--time-field`. It does not reproduce Discover/Dashboard application state.

## Limitations

- Requires Console Proxy enabled on your Kibana (standard on Kibana 7.x).
- User needs index read privileges.
- `agg` on `text` fields retries with `.keyword` when the backend returns 400.
- Kibana Advanced Settings such as `query:allowLeadingWildcards` and custom date timezone settings are not imported. Value wildcards follow the CLI's documented behavior; use raw `--dsl` when exact deployment-specific settings are required.
- `--trace-id` in field mode is intentionally recall-first: configured trace fields are ORed with a quoted all-fields fallback for heterogeneous indices. To reproduce a strict Discover field filter, use `--field traceId=<id>` (or the actual field name) instead.
