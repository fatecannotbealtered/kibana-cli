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

Do not commit live output, credentials, internal hostnames, or logs.
