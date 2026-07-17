package cmd

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestLoadFieldMapOrExit_InvalidYAML(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, ":\n\t[")
	resetCLIState(t)
	jsonMode = true
	_, err := loadFieldMapOrExit()
	if !errors.Is(err, ErrSilent) || lastExit != ExitBadArgs {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestResolveQueryTarget_DataViewMetadata(t *testing.T) {
	resetCLIState(t)
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cmd.Flags().String("data-view", "", "")
	_ = cmd.Flags().Set("data-view", "dv-1")
	target, err := resolveQueryTarget(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if target.Index != "logs-*" || target.DataViewID != "dv-1" || target.DataViewTimeField != "event.time" || target.Client == nil {
		t.Fatalf("target=%+v", target)
	}
}
