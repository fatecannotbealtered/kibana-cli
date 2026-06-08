## Summary

## Test plan

- [ ] `go test -race ./...`
- [ ] `make check-clean` (or `scripts/check-clean.ps1`)
- [ ] Updated README / CHANGELOG if user-facing
- [ ] Updated `skills/kibana-cli/SKILL.md` if command behavior changed
- [ ] Checked `.agent/AGENT.md` and the relevant spec checklist

## Checklist

- [ ] No secrets or real cluster URLs in the diff
- [ ] JSON mode emits one envelope on stdout and keeps stderr as a side channel
- [ ] Write commands still require dry-run then confirm
