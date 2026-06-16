package cmd

import (
	"fmt"
	"os"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show Kibana auth context (AI Agent bootstrap)",
	Long: `Show Kibana authentication and log-query reachability.

JSON is the default output. Read ok, status, message, hint, errorCode, and exitCode first, then the kibana object.`,
	RunE: runContext,
}

func init() {
	rootCmd.AddCommand(contextCmd)
}

type contextKibana struct {
	Host            string `json:"host,omitempty"`
	Configured      bool   `json:"configured"`
	AuthMode        string `json:"authMode,omitempty"`
	Source          string `json:"source,omitempty"`
	Authenticated   bool   `json:"authenticated"`
	Username        string `json:"username,omitempty"`
	KibanaVersion   string `json:"kibanaVersion,omitempty"`
	SearchReachable bool   `json:"searchReachable"`
	SearchError     string `json:"searchError,omitempty"`
	AuthError       string `json:"authError,omitempty"`
}

type contextResult struct {
	AgentStatus
	Tool            string         `json:"tool"`
	Version         string         `json:"version"`
	SecurityTier    string         `json:"securityTier"`
	SkillMinVersion string         `json:"skillMinVersion"`
	ActiveContext   string         `json:"activeContext,omitempty"`
	Kibana          *contextKibana `json:"kibana"`
	Notices         []updateNotice `json:"notices,omitempty"`
}

func runContext(_ *cobra.Command, _ []string) error {
	k := &contextKibana{}
	out, _ := runBootstrapCheck()
	applyBootstrapToContext(out, k)
	if out.ConfigError != "" {
		k.AuthError = out.ConfigError
	}
	result := &contextResult{
		AgentStatus:     out.AgentStatus,
		Tool:            toolName,
		Version:         version,
		SecurityTier:    securityTier,
		SkillMinVersion: skillMinVersion,
		ActiveContext:   out.ContextName,
		Kibana:          k,
		Notices:         readCachedUpdateNotices(),
	}
	printContext(result)
	if !result.OK {
		return ErrSilent
	}
	return nil
}

func printContext(result *contextResult) {
	if jsonMode {
		if result.OK {
			printJSONSuccess(result)
			return
		}
		msg := result.Message
		if msg == "" {
			msg = result.Error
		}
		output.PrintJSON(output.FailureEnvelope(result.ErrorCode, msg, map[string]any{
			"status":          result.Status,
			"hint":            result.Hint,
			"exitCode":        result.ExitCode,
			"errorCode":       result.ErrorCode,
			"tool":            result.Tool,
			"version":         result.Version,
			"securityTier":    result.SecurityTier,
			"skillMinVersion": result.SkillMinVersion,
			"activeContext":   result.ActiveContext,
			"kibana":          result.Kibana,
			"notices":         result.Notices,
		}, elapsedDurationMs()))
		return
	}
	fmt.Println()
	output.AuxBold("  kibana-cli Context")
	output.AuxGray("  ────────────────────────────────────────")
	fmt.Println()
	if result.Message != "" {
		if result.OK {
			output.Success(result.Message)
		} else {
			output.Error(result.Message)
		}
		if result.Hint != "" {
			output.AuxGray("  " + result.Hint)
		}
	}
	k := result.Kibana
	if !k.Configured {
		printUpdateNoticeHint(os.Stdout, result.Notices)
		return
	}
	if result.ActiveContext != "" {
		output.Gray("  Context: " + result.ActiveContext)
	}
	output.Gray(fmt.Sprintf("  Host: %s (%s, source=%s)", k.Host, k.AuthMode, k.Source))
	if k.SearchError != "" && !k.SearchReachable {
		output.Warn(k.SearchError)
	}
	output.AuxGray(fmt.Sprintf("  exit %d", result.ExitCode))
	printUpdateNoticeHint(os.Stdout, result.Notices)
	fmt.Println()
}
