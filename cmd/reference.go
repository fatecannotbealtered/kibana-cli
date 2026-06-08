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
	Name         string         `json:"name"`
	Path         string         `json:"path"`
	Use          string         `json:"use"`
	Type         string         `json:"type"`
	Short        string         `json:"short,omitempty"`
	Description  string         `json:"description,omitempty"`
	Permission   string         `json:"permission"`
	SecurityTier string         `json:"security_tier"`
	Write        bool           `json:"write"`
	RawFormat    bool           `json:"raw_format"`
	Params       []flagSpec     `json:"params,omitempty"`
	Flags        []flagSpec     `json:"flags,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	Subcommands  []string       `json:"subcommands,omitempty"`
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
		"tool":            toolName,
		"version":         rootCmd.Version,
		"schema_version":  output.SchemaVersion,
		"skillMinVersion": skillMinVersion,
		"formats": []string{
			FormatJSON,
			FormatText,
			FormatRaw,
		},
		"security": map[string]any{
			"risk_tier":    securityTier,
			"blast_radius": "Read log data through Kibana and mutate only local kibana-cli config, audit files, field-map.yaml, or a standalone local binary during update.",
			"permissions": []map[string]any{
				{"tier": "read", "description": "context, doctor, reference, changelog, patterns, search, agg, config show, auth status, update check"},
				{"tier": "write", "description": "auth login/logout, config init, standalone binary update; always requires dry-run then confirm"},
				{"tier": "dangerous", "description": "not implemented"},
			},
		},
		"query_conventions": map[string]any{
			"fields":     "Query commands support --fields in JSON mode; _untrusted markers are preserved only for returned fields.",
			"pagination": "search and patterns support --limit plus --offset; agg supports --limit for top-N buckets and has no stable cursor.",
			"sort":       "search defaults to descending event time; patterns keep Kibana Saved Objects order; agg keeps Elasticsearch terms order.",
		},
		"exit_codes":  exitCodeReference(),
		"error_codes": errorCodeReference(),
		"commands":    collectCommandSpecs(rootCmd, ""),
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
	case strings.HasPrefix(path, "kibana-cli search") || strings.HasPrefix(path, "kibana-cli agg") || strings.HasPrefix(path, "kibana-cli patterns"):
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

func outputSchemaForCommand(path string) map[string]any {
	switch path {
	case "kibana-cli context":
		return map[string]any{"data": "Agent status, tool/version, security tier, and Kibana auth/search reachability"}
	case "kibana-cli doctor":
		return map[string]any{"data": "Agent status plus checks[] diagnostics and environment details"}
	case "kibana-cli search":
		return map[string]any{"data": "Log search result with index, total, tookMs, count, limit, offset, next_offset, has_more, hits[], zero-hit diagnostics, and _untrusted markers on hit content"}
	case "kibana-cli agg":
		return map[string]any{"data": "Terms aggregation result with field, total, tookMs, count, limit, buckets[], and _untrusted markers on external bucket data; no cursor because terms aggregation returns top-N buckets"}
	case "kibana-cli patterns list":
		return map[string]any{"data": "Saved Kibana index-pattern objects with count, total, limit, offset, next_offset, has_more, and _untrusted markers"}
	case "kibana-cli patterns fields":
		return map[string]any{"data": "Kibana index pattern field descriptors with count, total, limit, offset, next_offset, has_more, and _untrusted markers"}
	case "kibana-cli changelog":
		return map[string]any{"data": "current_version, since, and Keep-a-Changelog entries parsed from CHANGELOG.md"}
	default:
		return map[string]any{"envelope": "Unified ok/schema_version/data-or-error/meta envelope"}
	}
}
