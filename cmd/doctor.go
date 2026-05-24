package cmd

import (
	"fmt"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check Kibana configuration and log-query connectivity",
	Long: `Check configuration, Kibana login, and whether log search works.

With --json, read ok, status, message, hint, errorCode, and exitCode first (Agent-friendly).`,
	RunE: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

type doctorResult struct {
	AgentStatus
	ConfigExists    bool   `json:"configExists"`
	AuthValid       bool   `json:"authValid"`
	LatencyMs       int64  `json:"latencyMs"`
	Host            string `json:"host,omitempty"`
	AuthMode        string `json:"authMode,omitempty"`
	Username        string `json:"username,omitempty"`
	KibanaVersion   string `json:"kibanaVersion,omitempty"`
	SearchReachable bool   `json:"searchReachable"`
	SearchError     string `json:"searchError,omitempty"`
	Error           string `json:"error,omitempty"`
}

func runDoctor(_ *cobra.Command, _ []string) error {
	out, err := runBootstrapCheck()
	if err != nil {
		return err
	}
	result := &doctorResult{
		AgentStatus:     out.AgentStatus,
		ConfigExists:    out.ConfigExists,
		AuthValid:       out.AuthValid,
		LatencyMs:       out.LatencyMs,
		Host:            out.Host,
		AuthMode:        out.AuthMode,
		Username:        out.Username,
		KibanaVersion:   out.KibanaVersion,
		SearchReachable: out.SearchReachable,
		SearchError:     out.SearchError,
	}
	if !result.OK {
		result.Error = result.Message
	}
	printDoctor(result)
	if !result.OK {
		return ErrSilent
	}
	return nil
}

func printDoctor(result *doctorResult) {
	if jsonMode {
		output.PrintJSON(result)
		return
	}
	fmt.Println()
	output.Bold("  kibana-cli Doctor")
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
	if result.ConfigExists && result.AuthValid && result.SearchReachable {
		output.Success("Log search reachable via Console Proxy")
	} else if result.SearchError != "" {
		output.Warn(result.SearchError)
	}
	output.Gray(fmt.Sprintf("  Latency: %dms | exit %d", result.LatencyMs, result.ExitCode))
	fmt.Println()
}
