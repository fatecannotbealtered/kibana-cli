package cmd

import (
	"fmt"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show Kibana auth context (AI Agent bootstrap)",
	Long: `Show Kibana authentication and log-query reachability.

With --json, read ok, status, message, hint, errorCode, and exitCode first, then the kibana object.`,
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
	Kibana *contextKibana `json:"kibana"`
}

func runContext(_ *cobra.Command, _ []string) error {
	k := &contextKibana{}
	out, _ := runBootstrapCheck()
	applyBootstrapToContext(out, k)
	if out.ConfigError != "" {
		k.AuthError = out.ConfigError
	}
	result := &contextResult{AgentStatus: out.AgentStatus, Kibana: k}
	printContext(result)
	if !result.OK {
		return ErrSilent
	}
	return nil
}

func printContext(result *contextResult) {
	if jsonMode {
		output.PrintJSON(result)
		return
	}
	fmt.Println()
	output.Bold("  kibana-cli Context")
	output.Gray("  ────────────────────────────────────────")
	fmt.Println()
	if result.Message != "" {
		if result.OK {
			output.Success(result.Message)
		} else {
			output.Error(result.Message)
		}
		if result.Hint != "" {
			output.Gray("  " + result.Hint)
		}
	}
	k := result.Kibana
	if !k.Configured {
		return
	}
	output.Gray(fmt.Sprintf("  Host: %s (%s, source=%s)", k.Host, k.AuthMode, k.Source))
	if k.SearchError != "" && !k.SearchReachable {
		output.Warn(k.SearchError)
	}
	output.Gray(fmt.Sprintf("  exit %d", result.ExitCode))
	fmt.Println()
}
