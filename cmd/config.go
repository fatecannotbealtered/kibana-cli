package cmd

import (
	"fmt"
	"os"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage local field-map.yaml for log queries",
	Long: `Manage ~/.kibana-cli/field-map.yaml — maps logical service names and profiles
to heterogeneous log field names across indices (log_app, service_name, etc.).`,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create example field-map.yaml",
	RunE:  runConfigInit,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show loaded field-map.yaml",
	RunE:  runConfigShow,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd, configShowCmd)
	markWrite(configInitCmd)
}

func runConfigInit(_ *cobra.Command, _ []string) error {
	path := fieldmap.FilePath()
	if _, err := os.Stat(path); err == nil && !forceMode {
		return failValidation("field-map already exists: " + path + " (use --force to overwrite)")
	}
	skipped, err := writePlan("write field-map.yaml", map[string]any{"path": path, "force": forceMode}, nil)
	if err != nil || skipped {
		return err
	}
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return failConfig(err.Error())
	}
	if err := os.WriteFile(path, []byte(fieldmap.ExampleYAML), 0600); err != nil {
		return failConfig(err.Error())
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"status": "ok", "path": path})
		return nil
	}
	output.Success("Created " + path)
	output.AuxGray("  Edit profiles and services to match your log indices.")
	return nil
}

func runConfigShow(_ *cobra.Command, _ []string) error {
	m, err := fieldmap.Load()
	if err != nil {
		return failValidation(err.Error())
	}
	if m == nil {
		msg := "no field-map.yaml at " + fieldmap.FilePath() + " — run 'kibana-cli config init'"
		st := AgentStatus{
			OK:        false,
			Status:    "not_found",
			Message:   msg,
			Error:     msg,
			Hint:      "Run 'kibana-cli config init' to create field-map.yaml",
			ErrorCode: output.ErrNotFound,
			ExitCode:  ExitNotFound,
		}
		if jsonMode {
			output.PrintJSON(output.FailureEnvelope(output.ErrNotFound, msg, map[string]any{
				"status":    "not_found",
				"exists":    false,
				"path":      fieldmap.FilePath(),
				"hint":      st.Hint,
				"errorCode": st.ErrorCode,
				"exitCode":  st.ExitCode,
			}, elapsedDurationMs()))
		} else {
			output.Warn(msg)
		}
		applyAgentExit(st)
		return ErrSilent
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"path": fieldmap.FilePath(), "fieldMap": m})
		return nil
	}
	fmt.Println()
	output.AuxBold("  field-map.yaml")
	output.AuxGray("  " + fieldmap.FilePath())
	fmt.Println()
	output.Info(fmt.Sprintf("defaults.index: %s", m.Defaults.Index))
	output.Info(fmt.Sprintf("profiles: %d", len(m.Profiles)))
	output.Info(fmt.Sprintf("services: %d", len(m.Services)))
	for name := range m.Profiles {
		output.AuxGray("  profile: " + name)
	}
	fmt.Println()
	return nil
}
