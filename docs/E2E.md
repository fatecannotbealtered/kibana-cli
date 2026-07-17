# E2E / Integration Testing

`kibana-cli` unit tests use a mock Kibana server and are safe to run locally:

```bash
go test ./...
```

Live Kibana smoke tests are manual because they require an internal Kibana base URL and HTTP Basic credentials.

## Required Environment

Set all three auth variables together:

```bash
export KIBANA_CLI_HOST=https://kibana.example.com
export KIBANA_CLI_USER=<user>
export KIBANA_CLI_PASSWORD=<pass>
```

Optional:

```bash
export KIBANA_CLI_ALLOWED_INDEX_PREFIXES=logs-,app-
export KIBANA_CLI_TIMEOUT=60
```

## Smoke Flow

Use a narrow test index and time window:

```bash
kibana-cli context --compact
kibana-cli doctor --compact
kibana-cli patterns list --compact
kibana-cli patterns fields --index 'logs-*' --compact
kibana-cli search --index 'logs-*' --from now-15m --limit 1 --fields '@timestamp,msg,level,traceId' --compact
kibana-cli agg --index 'logs-*' --terms level --from now-1h --compact
```

For Discover parity, use an absolute window and compare both query modes without returning raw logs:

```bash
kibana-cli search --context <context> --data-view <data-view-id> \
  --query 'msg:"1" and msg:"2"' --query-language kql \
  --from '<absolute-start>' --to '<absolute-end>' --limit 1 --compact

kibana-cli search --context <context> --data-view <data-view-id> \
  --query 'msg:"1" AND msg:"2"' --query-language lucene \
  --from '<absolute-start>' --to '<absolute-end>' --limit 1 --compact

kibana-cli agg --context <context> --data-view <data-view-id> --terms level \
  --query 'msg:"1" and msg:"2"' --query-language kql \
  --from '<absolute-start>' --to '<absolute-end>' --compact
```

Assert the returned `context`, `host`, `index`, `dataViewId`, `timeField`, `from`, `to`, and `queryLanguage` before comparing `total`. Also verify that lowercase KQL-style booleans in default Lucene mode and unquoted extra positional arguments return `E_VALIDATION` without sending `_search`.

Do not commit live output, credentials, internal hostnames, or logs.
