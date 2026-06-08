package kibanacli

import _ "embed"

// ChangelogMarkdown is the single source CHANGELOG.md embedded into the binary.
//
//go:embed CHANGELOG.md
var ChangelogMarkdown string

// PackageJSON is embedded so the CLI default version follows package.json.
//
//go:embed package.json
var PackageJSON string
