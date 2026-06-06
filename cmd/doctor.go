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

JSON is the default output. Read ok, status, message, hint, errorCode, and exitCode first (Agent-friendly).`,
	RunE: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

type doctorResult struct {
	AgentStatus
	Checks          []doctorCheck `json:"checks"`
	ConfigExists    bool          `json:"configExists"`
	AuthValid       bool          `json:"authValid"`
	LatencyMs       int64         `json:"latencyMs"`
	Host            string        `json:"host,omitempty"`
	AuthMode        string        `json:"authMode,omitempty"`
	Username        string        `json:"username,omitempty"`
	KibanaVersion   string        `json:"kibanaVersion,omitempty"`
	SearchReachable bool          `json:"searchReachable"`
	SearchError     string        `json:"searchError,omitempty"`
	Error           string        `json:"error,omitempty"`
}

type doctorCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Fix    string `json:"fix,omitempty"`
}

func runDoctor(_ *cobra.Command, _ []string) error {
	out, _ := runBootstrapCheck()
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
	result.Checks = buildDoctorChecks(result)
	if !result.OK {
		result.Error = result.Message
	}
	printDoctor(result)
	if !result.OK {
		return ErrSilent
	}
	return nil
}

func buildDoctorChecks(result *doctorResult) []doctorCheck {
	configCheck := doctorCheck{Check: "config", Status: "pass"}
	if !result.ConfigExists {
		configCheck.Status = "fail"
		configCheck.Fix = "run kibana-cli auth login --dry-run, then rerun with --confirm"
	}
	authCheck := doctorCheck{Check: "auth", Status: "pass"}
	if !result.AuthValid {
		authCheck.Status = "fail"
		authCheck.Fix = "check Kibana host, username, password, or credential store"
	}
	searchCheck := doctorCheck{Check: "search", Status: "pass"}
	if !result.SearchReachable {
		searchCheck.Status = "fail"
		searchCheck.Fix = "check index read privileges and Console Proxy availability"
	}
	return []doctorCheck{configCheck, authCheck, searchCheck}
}

func printDoctor(result *doctorResult) {
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
			"status":    result.Status,
			"hint":      result.Hint,
			"exitCode":  result.ExitCode,
			"errorCode": result.ErrorCode,
			"checks":    result.Checks,
			"diagnostics": map[string]any{
				"configExists":    result.ConfigExists,
				"authValid":       result.AuthValid,
				"latencyMs":       result.LatencyMs,
				"host":            result.Host,
				"authMode":        result.AuthMode,
				"username":        result.Username,
				"kibanaVersion":   result.KibanaVersion,
				"searchReachable": result.SearchReachable,
				"searchError":     result.SearchError,
			},
		}, elapsedDurationMs()))
		return
	}
	fmt.Println()
	output.AuxBold("  kibana-cli Doctor")
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
	if result.ConfigExists && result.AuthValid && result.SearchReachable {
		output.Success("Log search reachable via Console Proxy")
	} else if result.SearchError != "" {
		output.Warn(result.SearchError)
	}
	output.AuxGray(fmt.Sprintf("  Latency: %dms | exit %d", result.LatencyMs, result.ExitCode))
	fmt.Println()
}
