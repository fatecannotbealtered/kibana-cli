# Open Source Push Checklist

Use before the first public `git push`.

## Must not commit

- [ ] `.idea/`, `.vscode/` (IDE settings)
- [ ] `.env` (local Kibana credentials, if used)
- [ ] `docs/evidence/*.log` (may contain machine paths)
- [ ] `bin/`, `*.exe`, `dist/`
- [ ] Production Kibana URLs, passwords, or internal hostnames

## Safe to commit

- [ ] `LICENSE` (MIT)
- [ ] `NOTICE.md` (trademark notice)
- [ ] `SECURITY.md` (public security contact)
- [ ] `docs/COMPATIBILITY.md`
- [ ] `docs/E2E.md`
- [ ] `AGENTS.md` and `.agent/`
- [ ] `skills/kibana-cli/SKILL.md`
- [ ] `CODE_OF_CONDUCT.md`

## Before push

```bash
make check-clean
go test ./...
kibana-cli reference --compact
kibana-cli changelog --since <previous_version> --compact
```

## After publish

- Enable GitHub secret scanning
- Repository description: *Kibana log query CLI for AI Agents*
