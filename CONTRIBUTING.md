# Contributing

Thank you for improving kibana-cli — a **Kibana log query CLI** for AI Agents.

## Development setup

- Go **1.23+** (see `go.mod`)

```bash
git clone https://github.com/fatecannotbealtered/kibana-cli.git
cd kibana-cli
go mod download
go test ./...
go build -o bin/kibana-cli ./cmd/kibana-cli
```

## Commands

| Goal | Command |
|------|---------|
| Unit tests | `go test -race ./...` |
| Repo cleanliness | `make check-clean` |
| Format | `gofmt -w .` |
| Vet | `go vet ./...` |
| Build | `make build` |
| Full test report | `.\scripts\test-all.ps1` (unit tests + check-clean) |

CI runs unit tests on every push (`.github/workflows/ci.yml`). Live Kibana smoke tests are manual.

## Pull requests

1. One logical change per PR when possible.
2. **Tests** for behavior changes in `cmd/` or `internal/kibanaclient/`.
3. **Docs**: update `README.md` / `README_zh.md`, `skills/kibana-cli/SKILL.md`, and `CHANGELOG.md`.
4. Run `make check-clean` — no resurrected ES-direct transport or API keys.
5. No real passwords or production Kibana URLs in commits.

## AI Agent skill

Update `skills/kibana-cli/SKILL.md` when commands or auth flows change.
