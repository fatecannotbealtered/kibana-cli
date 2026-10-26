// Package kibanacli embeds the repository's CHANGELOG.md for the changelog built-in.
package kibanacli

import _ "embed"

// ChangelogMarkdown is the single changelog source, served by `kibana-cli changelog`.
//
//go:embed CHANGELOG.md
var ChangelogMarkdown string
