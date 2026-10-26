## Summary

Brief description of the changes in this PR.

## Related Issues

Closes #(issue number)

## Changes

- [ ] Feature / fix description
- [ ] Another change

## Checklist

- [ ] Tests pass, the aicli conformance suite included, with a `Sample` for every write command (VER-001)
- [ ] Every new command runs in a test; R06 coverage passes on the full test run (VER-003)
- [ ] Each changed command's `Maturity` still matches its `Evidence` (VER-005)
- [ ] New effect-bearing commands go through `App.Write` or `App.WriteBatch` with truthful `Effects` and `Dangerous`; nothing writes the scope file (CLI-025, CLI-050, SEC-011)
- [ ] Scope and authorization behaviour unchanged, or `SECURITY.md` and the README's Authorization section updated (SEC-011, SEC-013)
- [ ] `node scripts/check-spec.js` passes: nothing vendored was edited
- [ ] No hardcoded secrets or credentials; secret flags are in `Meta.Secrets` (SEC-021)
- [ ] `CHANGELOG.md` `## [Unreleased]` updated for user-facing changes
- [ ] Command maps regenerated (README Capabilities and the Skill); Skill playbooks and `test-prompts.json` updated for changed commands
- [ ] Bilingual docs synced (README.md ↔ README_zh.md)
