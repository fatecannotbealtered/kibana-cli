package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
)

// stateDir is the directory aicli keeps kibana-cli's state in: App.StateDir, else
// KIBANA_CLI_STATE_DIR, else ~/.kibana-cli (the 1.x config directory, where
// field-map.yaml already lives).
func stateDir(app *aicli.App) (string, error) {
	dir := app.StateDir
	if dir == "" {
		dir = app.Env("KIBANA_CLI_STATE_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", aicli.NewError("E_CONFIG", "The home directory is unknown, so the state directory cannot be found.", map[string]any{
				"next_action": "Set KIBANA_CLI_STATE_DIR to a private directory."})
		}
		dir = filepath.Join(home, ".kibana-cli")
	}
	return filepath.Abs(dir)
}

// fieldMapPath is the field-map file a command uses: --field-map-file (relative to
// the current directory) on the commands that have it, else the field_map_file
// setting (KIBANA_CLI_FIELD_MAP_FILE or the profile; relative to the state
// directory), else <state dir>/field-map.yaml.
func fieldMapPath(app *aicli.App, cmd *cobra.Command) (string, error) {
	raw, source, e := app.Setting(cmd, "field_map_file")
	if e != nil {
		return "", e
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fieldmap.FileName
	}
	if source == "flag" || filepath.IsAbs(raw) {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", aicli.Usage("invalid_value", "--field-map-file", "The field-map path is not a valid path.")
		}
		return abs, nil
	}
	dir, err := stateDir(app)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, raw), nil
}

// loadFieldMap reads the field-map file; a missing file is a nil map.
func loadFieldMap(app *aicli.App, cmd *cobra.Command) (*fieldmap.Map, string, error) {
	path, err := fieldMapPath(app, cmd)
	if err != nil {
		return nil, "", err
	}
	m, err := fieldmap.LoadFile(path)
	if err != nil {
		e := aicli.NewError("E_CONFIG", fmt.Sprintf("The field-map file %s cannot be read as YAML; details.reason says why.", path), map[string]any{
			"reason": err.Error(), "next_action": fmt.Sprintf("Ask the user to fix %s (`kibana-cli field-map get` shows what is read).", path)})
		e.Untrusted = []string{"reason"}
		return nil, path, e
	}
	return m, path, nil
}
