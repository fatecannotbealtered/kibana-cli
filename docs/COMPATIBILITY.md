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
# Optional: skip TLS verify for corporate CA
export KIBANA_CLI_INSECURE=1
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

## Limitations

- Requires Console Proxy enabled on your Kibana (standard on Kibana 7.x).
- User needs index read privileges.
- `agg` on `text` fields retries with `.keyword` when the backend returns 400.
