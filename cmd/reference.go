package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var referenceCmd = &cobra.Command{
	Use:   "reference",
	Short: "Print all commands and flags (for AI Agents)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		var lines []string
		lines = append(lines, "# kibana-cli Command Reference", "")
		lines = append(lines, fmt.Sprintf("Version: %s", rootCmd.Version), "")
		walkCommands(rootCmd, &lines, "")
		if jsonMode {
			printJSONSuccess(referenceData(strings.Join(lines, "\n")))
			return nil
		}
		for _, line := range lines {
			cmd.Println(line)
		}
		return nil
	},
}

type commandSpec struct {
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	Use          string     `json:"use"`
	Type         string     `json:"type"`
	Short        string     `json:"short,omitempty"`
	Description  string     `json:"description,omitempty"`
	Permission   string     `json:"permission"`
	SecurityTier string     `json:"security_tier"`
	Write        bool       `json:"write"`
	RawFormat    bool       `json:"raw_format"`
	Params       []flagSpec `json:"params,omitempty"`
	Flags        []flagSpec `json:"flags,omitempty"`
	OutputSchema string     `json:"output_schema"`
	Examples     []string   `json:"examples,omitempty"`
	Subcommands  []string   `json:"subcommands,omitempty"`
}

// referenceDataSchema is a machine-readable description of one command's data
// payload: shape ("object" or "array"), the enumerated top-level data keys, and
// the externally-sourced fields tagged _untrusted (document content an agent must
// treat as data, never instructions).
type referenceDataSchema struct {
	Shape           string   `json:"shape"`
	Fields          []string `json:"fields"`
	UntrustedFields []string `json:"untrusted_fields,omitempty"`
}

type flagSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Usage    string `json:"usage"`
	Required bool   `json:"required,omitempty"`
	Multiple bool   `json:"multiple,omitempty"`
}

func init() {
	rootCmd.AddCommand(referenceCmd)
}

func referenceData(markdown string) map[string]any {
	return map[string]any{
		"tool":              toolName,
		"version":           rootCmd.Version,
		"schema_version":    output.SchemaVersion,
		"skillMinVersion":   skillMinVersion,
		"release_readiness": buildReleaseReadiness(),
		"formats": []string{
			FormatJSON,
			FormatText,
			FormatRaw,
		},
		"security": map[string]any{
			"risk_tier":    securityTier,
			"blast_radius": "Read log data through Kibana and mutate only local kibana-cli config, audit files, field-map.yaml, or a standalone local binary during update.",
			"permissions": []map[string]any{
				{"tier": "read", "description": "context, doctor, reference, changelog, patterns, search, agg, objects, config show, auth status, update --check"},
				{"tier": "write", "description": "auth login/logout, config init; always requires dry-run then confirm"},
				{"tier": "self_update", "description": "update; single command (no confirm token), self-verifying via in-process Sigstore signature, then Skill sync"},
				{"tier": "dangerous", "description": "not implemented"},
			},
		},
		"query_conventions": map[string]any{
			"fields":     "Query commands support --fields in JSON mode; _untrusted markers are preserved only for returned fields.",
			"pagination": "search and patterns support --limit plus --offset; search also offers a stable --search-after cursor (next_search_after token) for large time-ordered sets; objects list supports --limit/--offset; agg supports --limit for top-N buckets and has no stable cursor.",
			"sort":       "search defaults to descending event time; patterns keep Kibana Saved Objects order; agg keeps Elasticsearch terms or date_histogram order.",
			"raw_dsl":    "search --dsl <json> sends a raw Elasticsearch _search body through the Console Proxy, bypassing the flag query builder; returned hits keep _untrusted tagging.",
		},
		"exit_codes":  exitCodeReference(),
		"error_codes": errorCodeReference(),
		"commands":    collectCommandSpecs(rootCmd, ""),
		"schemas":     referenceSchemas(),
		"markdown":    markdown,
	}
}

func exitCodeReference() map[string]string {
	return map[string]string{
		"0": "success",
		"1": "generic error",
		"2": "argument or validation error",
		"3": "resource not found",
		"4": "configuration, authentication, or permission failure",
		"5": "confirmation required",
		"6": "precondition conflict or invalid/expired confirmation token",
		"7": "retryable transient error: network, rate limit, or server",
		"8": "timeout",
	}
}

func errorCodeReference() map[output.ErrorCode]map[string]any {
	codes := []output.ErrorCode{
		output.ErrConfig,
		output.ErrAuth,
		output.ErrForbidden,
		output.ErrNotFound,
		output.ErrRateLimit,
		output.ErrServer,
		output.ErrValidation,
		output.ErrNetwork,
		output.ErrTimeout,
		output.ErrConfirmRequired,
		output.ErrConflict,
		output.ErrUnknown,
	}
	out := map[output.ErrorCode]map[string]any{}
	for _, code := range codes {
		out[code] = map[string]any{
			"retryable": output.RetryableForErrorCode(code),
			"hint":      output.HintForErrorCode(code),
		}
	}
	return out
}

func walkCommands(cmd *cobra.Command, lines *[]string, prefix string) {
	if cmd.Hidden {
		return
	}
	name := prefix + cmd.Use
	*lines = append(*lines, "## "+name, "")
	if cmd.Short != "" {
		*lines = append(*lines, cmd.Short, "")
	}
	if cmd.Long != "" && cmd.Long != cmd.Short {
		*lines = append(*lines, cmd.Long, "")
	}
	if schema := outputSchemaForCommand(name); schema != "group" {
		*lines = append(*lines, "Output schema: `"+schema+"`", "")
	}
	if examples := examplesForCommand(name); len(examples) > 0 {
		*lines = append(*lines, "Examples:")
		for _, ex := range examples {
			*lines = append(*lines, "  "+ex)
		}
		*lines = append(*lines, "")
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		*lines = append(*lines, fmt.Sprintf("  --%s (%s) %s", f.Name, f.Value.Type(), f.Usage))
	})
	*lines = append(*lines, "")
	children := cmd.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		if child.Name() == "help" || child.Name() == "completion" {
			continue
		}
		walkCommands(child, lines, name+" ")
	}
}

func collectCommandSpecs(cmd *cobra.Command, prefix string) []commandSpec {
	if cmd.Hidden {
		return nil
	}
	name := strings.TrimSpace(prefix + cmd.Use)
	cmdType := commandType(cmd)
	spec := commandSpec{
		Name:         name,
		Path:         name,
		Use:          cmd.Use,
		Type:         cmdType,
		Short:        cmd.Short,
		Description:  strings.TrimSpace(firstNonEmptyString(cmd.Long, cmd.Short)),
		Permission:   commandPermission(cmd),
		SecurityTier: securityTier,
		Write:        isWriteCommand(cmd),
		RawFormat:    commandSupportsFormat(cmd, FormatRaw),
		OutputSchema: outputSchemaForCommand(name),
		Examples:     examplesForCommand(name),
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		param := flagSpec{
			Name:     f.Name,
			Type:     f.Value.Type(),
			Usage:    f.Usage,
			Required: f.Annotations != nil && len(f.Annotations[cobra.BashCompOneRequiredFlag]) > 0,
			Multiple: f.Value.Type() == "stringArray" || f.Value.Type() == "stringSlice",
		}
		spec.Params = append(spec.Params, param)
		spec.Flags = append(spec.Flags, param)
	})
	children := cmd.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	var specs []commandSpec
	for _, child := range children {
		if child.Name() == "help" || child.Name() == "completion" || child.Hidden {
			continue
		}
		spec.Subcommands = append(spec.Subcommands, strings.TrimSpace(name+" "+child.Use))
	}
	specs = append(specs, spec)
	for _, child := range children {
		if child.Name() == "help" || child.Name() == "completion" {
			continue
		}
		specs = append(specs, collectCommandSpecs(child, name+" ")...)
	}
	return specs
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func commandType(cmd *cobra.Command) string {
	if isWriteCommand(cmd) {
		return "write"
	}
	path := cmd.CommandPath()
	switch {
	case path == "kibana-cli reference" || path == "kibana-cli context" || path == "kibana-cli doctor" || path == "kibana-cli changelog":
		return "self_description"
	case strings.HasPrefix(path, "kibana-cli search") || strings.HasPrefix(path, "kibana-cli agg") || strings.HasPrefix(path, "kibana-cli patterns") || strings.HasPrefix(path, "kibana-cli objects"):
		return "query"
	case strings.HasPrefix(path, "kibana-cli config") || strings.HasPrefix(path, "kibana-cli auth") || strings.HasPrefix(path, "kibana-cli update"):
		return "config"
	default:
		return "group"
	}
}

func commandPermission(cmd *cobra.Command) string {
	if isWriteCommand(cmd) {
		return "write"
	}
	return "read"
}

// outputSchemaForCommand maps a command path to a schema label defined in
// referenceSchemas(). Group/parent commands that emit no data of their own map to
// the "group" label. The label is the contract an agent resolves against schemas.
func outputSchemaForCommand(path string) string {
	switch path {
	case "kibana-cli context":
		return "context"
	case "kibana-cli context list":
		return "context_list"
	case "kibana-cli context current":
		return "context_current"
	case "kibana-cli context use":
		return "context_use"
	case "kibana-cli context add":
		return "context_add"
	case "kibana-cli context remove":
		return "context_remove"
	case "kibana-cli doctor":
		return "doctor"
	case "kibana-cli search":
		return "search_result"
	case "kibana-cli agg":
		return "agg_result"
	case "kibana-cli patterns list":
		return "patterns"
	case "kibana-cli patterns fields":
		return "fields"
	case "kibana-cli patterns infer":
		return "patterns_infer"
	case "kibana-cli objects list":
		return "objects_list"
	case "kibana-cli objects get":
		return "objects_get"
	case "kibana-cli changelog":
		return "changelog"
	case "kibana-cli reference":
		return "reference"
	case "kibana-cli auth login":
		return "auth_login"
	case "kibana-cli auth logout":
		return "auth_logout"
	case "kibana-cli auth status":
		return "auth_status"
	case "kibana-cli config init":
		return "config_init"
	case "kibana-cli config show":
		return "config_show"
	case "kibana-cli update":
		return "update_report"
	default:
		// Root and parent/group commands (kibana-cli, auth, config, patterns).
		return "group"
	}
}

// examplesForCommand returns one runnable example per leaf command. Group/parent
// commands return nil because they emit only help. Write commands show the
// dry-run/confirm flow an agent must follow.
func examplesForCommand(path string) []string {
	switch path {
	case "kibana-cli context":
		return []string{"kibana-cli context --compact"}
	case "kibana-cli context list":
		return []string{"kibana-cli context list --compact"}
	case "kibana-cli context current":
		return []string{"kibana-cli context current --compact"}
	case "kibana-cli context use":
		return []string{
			"kibana-cli context use sys-a --dry-run --compact",
			"kibana-cli context use sys-a --confirm <confirm_token> --compact",
		}
	case "kibana-cli context add":
		return []string{
			"kibana-cli context add sys-a --host https://kibana-a.example.com --user dev_ro --password '...' --default-index 'sysA-app-*' --dry-run --compact",
			"kibana-cli context add sys-a --host https://kibana-a.example.com --user dev_ro --password '...' --confirm <confirm_token> --compact",
		}
	case "kibana-cli context remove":
		return []string{
			"kibana-cli context remove sys-a --dry-run --compact",
			"kibana-cli context remove sys-a --confirm <confirm_token> --compact",
		}
	case "kibana-cli doctor":
		return []string{"kibana-cli doctor --compact"}
	case "kibana-cli reference":
		return []string{"kibana-cli reference --compact"}
	case "kibana-cli changelog":
		return []string{"kibana-cli changelog --since 0.1.0 --compact"}
	case "kibana-cli search":
		return []string{
			"kibana-cli search --index 'app-test-log-*' --query timeout --from now-15m --limit 20 --compact",
			"kibana-cli search --index 'app-test-log-*' --limit 100 --search-after <next_search_after> --compact",
			"kibana-cli search --index 'app-test-log-*' --dsl '{\"query\":{\"match_all\":{}},\"size\":5}' --compact",
		}
	case "kibana-cli agg":
		return []string{
			"kibana-cli agg --index 'app-test-log-*' --terms level --from now-1h --limit 10 --compact",
			"kibana-cli agg --index 'app-test-log-*' --agg-type date_histogram --interval 1h --metric avg --metric-field took_ms --compact",
		}
	case "kibana-cli patterns list":
		return []string{"kibana-cli patterns list --limit 50 --compact"}
	case "kibana-cli patterns fields":
		return []string{"kibana-cli patterns fields --index 'app-test-log-*' --limit 100 --compact"}
	case "kibana-cli patterns infer":
		return []string{
			"kibana-cli patterns infer --index 'sysA-app-*' --compact",
			"kibana-cli patterns infer --index 'sysA-app-*' --write --dry-run --compact",
			"kibana-cli patterns infer --index 'sysA-app-*' --write --confirm <confirm_token> --compact",
		}
	case "kibana-cli objects list":
		return []string{"kibana-cli objects list --type dashboard --limit 50 --compact"}
	case "kibana-cli objects get":
		return []string{"kibana-cli objects get --type dashboard --id 7adfa750-4c81-11e8-b3d7-01146121b73d --compact"}
	case "kibana-cli auth status":
		return []string{"kibana-cli auth status --compact"}
	case "kibana-cli config show":
		return []string{"kibana-cli config show --compact"}
	case "kibana-cli auth login":
		return []string{
			"kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --dry-run --compact",
			"kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --confirm <confirm_token> --compact",
		}
	case "kibana-cli auth logout":
		return []string{
			"kibana-cli auth logout --dry-run --compact",
			"kibana-cli auth logout --confirm <confirm_token> --compact",
		}
	case "kibana-cli config init":
		return []string{
			"kibana-cli config init --dry-run --compact",
			"kibana-cli config init --confirm <confirm_token> --compact",
		}
	case "kibana-cli update":
		return []string{
			"kibana-cli update --compact",
			"kibana-cli update --check --compact",
			"kibana-cli update --dry-run --compact",
		}
	default:
		// Root and parent/group commands emit only help text.
		return nil
	}
}

// referenceSchemas enumerates the real data payload of every command, keyed by the
// label outputSchemaForCommand() returns. Fields are taken from the actual JSON
// payloads each command emits; _untrusted marks externally-sourced document/source
// content (Kibana hits, buckets, saved objects) that agents must treat as data.
func referenceSchemas() map[string]referenceDataSchema {
	return map[string]referenceDataSchema{
		"search_result": {
			Shape:           "object",
			Fields:          []string{"index", "profile", "total", "totalRelation", "tookMs", "hits", "count", "limit", "offset", "has_more", "next_offset", "next_search_after", "limit_capped", "limit_max", "traceMode", "zeroReason", "hint", "diagnostics"},
			UntrustedFields: []string{"hits"},
		},
		"agg_result": {
			Shape:           "object",
			Fields:          []string{"field", "aggType", "metric", "total", "tookMs", "buckets", "count", "limit", "has_more", "next_offset", "_untrusted"},
			UntrustedFields: []string{"buckets"},
		},
		"objects_list": {
			Shape:           "object",
			Fields:          []string{"type", "objects", "count", "total", "limit", "offset", "has_more", "next_offset", "_untrusted"},
			UntrustedFields: []string{"objects"},
		},
		"objects_get": {
			Shape:           "object",
			Fields:          []string{"id", "type", "title", "description", "updated_at", "object", "_untrusted"},
			UntrustedFields: []string{"title", "description", "object"},
		},
		"patterns": {
			Shape:           "object",
			Fields:          []string{"patterns", "count", "total", "limit", "offset", "has_more", "next_offset", "_untrusted"},
			UntrustedFields: []string{"patterns"},
		},
		"fields": {
			Shape:           "object",
			Fields:          []string{"index", "fields", "count", "total", "limit", "offset", "has_more", "next_offset", "_untrusted"},
			UntrustedFields: []string{"fields"},
		},
		"patterns_infer": {
			Shape:  "object",
			Fields: []string{"index", "profileName", "inferredProfile", "yamlSnippet", "notes", "status", "path", "profile"},
		},
		"context": {
			Shape:  "object",
			Fields: []string{"status", "message", "error", "hint", "errorCode", "statusCode", "exitCode", "tool", "version", "securityTier", "skillMinVersion", "activeContext", "kibana", "notices"},
		},
		"context_list": {
			Shape:  "object",
			Fields: []string{"currentContext", "contexts", "count"},
		},
		"context_current": {
			Shape:  "object",
			Fields: []string{"currentContext", "host"},
		},
		"context_use": {
			Shape:  "object",
			Fields: []string{"status", "currentContext"},
		},
		"context_add": {
			Shape:  "object",
			Fields: []string{"status", "context", "host", "kibanaVersion", "current"},
		},
		"context_remove": {
			Shape:  "object",
			Fields: []string{"status", "context"},
		},
		"doctor": {
			Shape:  "object",
			Fields: []string{"status", "message", "error", "hint", "errorCode", "statusCode", "exitCode", "tool", "version", "skillMinVersion", "securityTier", "checks", "notices", "configExists", "authValid", "latencyMs", "host", "authMode", "username", "kibanaVersion", "searchReachable", "searchError"},
		},
		"changelog": {
			Shape:  "object",
			Fields: []string{"current_version", "since", "entries", "count"},
		},
		"reference": {
			Shape:  "object",
			Fields: []string{"tool", "version", "schema_version", "skillMinVersion", "release_readiness", "formats", "security", "query_conventions", "exit_codes", "error_codes", "commands", "schemas", "markdown"},
		},
		"auth_login": {
			Shape:  "object",
			Fields: []string{"status", "host", "authMode", "username", "kibanaVersion", "searchReachable", "credentialStore"},
		},
		"auth_logout": {
			Shape:  "object",
			Fields: []string{"status"},
		},
		"auth_status": {
			Shape:  "object",
			Fields: []string{"status", "configured", "host", "authMode", "source", "credentialStore", "kibanaVersion"},
		},
		"config_init": {
			Shape:  "object",
			Fields: []string{"status", "path"},
		},
		"config_show": {
			Shape:           "object",
			Fields:          []string{"path", "fieldMap"},
			UntrustedFields: []string{"fieldMap"},
		},
		"update_report": {
			Shape:  "object",
			Fields: []string{"status", "message", "previous_version", "current_version", "target_version", "latest_version", "update_available", "install_method", "path", "asset", "url", "command", "hint", "dry_run", "checksum_verified", "signature_status", "signature_verified", "trust_root_source", "skill_sync_command", "skill_sync_status", "notices"},
		},
		"group": {
			Shape:  "object",
			Fields: []string{"subcommands"},
		},
	}
}
