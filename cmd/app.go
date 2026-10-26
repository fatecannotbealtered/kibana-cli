// Package cmd is kibana-cli's command tree on the aicli library (ai-native-cli-spec
// 2.0). NewApp builds a fresh App and tree for every run; each command group
// registers itself from one register function in its own file.
package cmd

import (
	"context"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	kibanacli "github.com/fatecannotbealtered/kibana-cli"
	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
)

// version is set at build time:
// -ldflags "-X github.com/fatecannotbealtered/kibana-cli/cmd.version=<version>".
var version = "dev"

// appVersion is the version this binary reports: the ldflags value, else the module
// version of a `go install` build, else "dev". aicli reports a development build as
// the next patch after the newest changelog release (P14).
func appVersion() string {
	if version != "dev" && version != "" {
		return strings.TrimPrefix(version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

// kibanaSettings are the values kibana-cli reads with App.Setting (CLI-100): a flag
// where one is declared, then KIBANA_CLI_<NAME>, then the active profile of
// settings.json, then the default. The password is a secret: it never comes from
// the settings file; without KIBANA_CLI_PASSWORD it comes from the OS keyring entry
// `auth login` stored for the host and user (the 1.x entry, kept compatible).
func kibanaSettings() []aicli.Setting {
	return []aicli.Setting{
		{Name: "host", Description: "Kibana base URL, like https://kibana.example.com (http:// only for localhost)"},
		{Name: "user", Description: "HTTP Basic username"},
		{Name: "password", Secret: true, Description: "HTTP Basic password: KIBANA_CLI_PASSWORD only (the settings file refuses it), else the OS keyring entry kibana-cli / <host>|basic|<user> that auth login stores"},
		{Name: "kibana_version", Description: "The Kibana version, like 9.5.2. Optional: doctor and auth status report it, and the APIs of that version are asked first; without it they are found by trying them"},
		{Name: "default_index", Description: "Index pattern the log commands search when neither --index, --data-view nor --field-profile names one"},
		{Name: "field_map_file", Flag: "field-map-file", Default: "field-map.yaml", Description: "The field-map file; relative to the state directory (~/.kibana-cli) unless given as --field-map-file"},
		{Name: "allowed_index_prefixes", Description: "Comma-separated index prefixes; when set, every index pattern a command searches must start with one"},
		{Name: "timeout", Default: "30s", Description: "Timeout of each Kibana request, at most 30s (a bare number is seconds)"},
	}
}

// NewApp builds a fresh App and command tree. env is how the App reads the
// environment: main passes os.Getenv, tests a map, the conformance suite its own
// overlay. Read the environment only through app.Env.
func NewApp(env func(string) string) *aicli.App {
	if env == nil {
		env = os.Getenv
	}
	root := &cobra.Command{Use: "kibana-cli",
		Short: "Kibana logs for AI agents: search, aggregate and read log documents, data views and saved objects"}
	app := &aicli.App{
		Name: "kibana-cli", Version: appVersion(), RiskTier: "T2", EnvPrefix: "KIBANA_CLI",
		Root: root, Changelog: kibanacli.ChangelogMarkdown, Env: env,
		Settings: kibanaSettings(),
		// The built-in update (CLI-070). No GoPackage: the module path has no /v2
		// suffix, so `go install ...@v2` cannot resolve it.
		Update: &aicli.UpdateConfig{Repo: "fatecannotbealtered/kibana-cli", NPMPackage: "@fateforge/kibana-cli"},
		// The fleet's measured guesses (CLI-022); groups add their own.
		Shortcuts:    map[string][]string{},
		FlagSynonyms: map[string][]string{"all": {"limit"}, "size": {"limit"}, "from": {"since"}, "to": {"until"}},
		VerbSynonyms: map[string][]string{"find": {"search"}, "query": {"search"}, "count": {"aggregate"}},
	}
	app.Identity = func(context.Context) (aicli.Identity, error) { return kibanaIdentity(app) }
	app.DoctorChecks = func(ctx context.Context) []aicli.Check { return kibanaDoctorChecks(ctx, app) }
	app.ContextExtra = func(context.Context) map[string]any { return kibanaContextExtra(app) }
	registerCommands(app)
	return app
}

// registerCommands registers every command group in a fixed order (the order of
// the Skill's command map).
func registerCommands(app *aicli.App) {
	registerLog(app)
	registerPattern(app)
	registerObject(app)
	registerFieldMap(app)
	registerAuth(app)
	registerProfile(app)
}

// addGroup adds a noun to the root and registers its summary (App.Group).
func addGroup(app *aicli.App, use, summary string, aliases ...string) *cobra.Command {
	g := &cobra.Command{Use: use, Aliases: aliases}
	app.Root.AddCommand(g)
	app.Group(g, summary)
	return g
}
