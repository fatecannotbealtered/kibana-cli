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

## Before push

```bash
make check-clean
go test ./...
```

## After publish

- Enable GitHub secret scanning
- Repository description: *Kibana log query CLI for AI Agents*
