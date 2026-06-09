# AGENTS.md

This repository follows the AI-native tool standards in `.agent/`.

Start with `.agent/AGENT.md`, then read only the specific spec needed for the task:

- `.agent/CLI-SPEC.md` for command output, errors, write confirmation, self-description, and versioning.
- `.agent/SKILL-SPEC.md` for `skills/kibana-cli/SKILL.md` changes.
- Shared [`REPO-SPEC.md`](https://github.com/fatecannotbealtered/ai-native-cli-spec/blob/main/REPO-SPEC.md) for repository structure, release, and documentation conventions.
- `.agent/SEC-SPEC.md` for risk tier, untrusted content, credential storage, least privilege, and supply chain rules.

For behavior changes, update code, tests, `CHANGELOG.md`, and the Skill together.

Before release, Functional Contract Coverage must remain 100%: every public README / Skill / reference / help / context / doctor / changelog / update behavior needs command-level tests.
