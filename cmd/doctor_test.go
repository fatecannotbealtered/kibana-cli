package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestDoctor_Help(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"doctor", "--help"})
	_ = rootCmd.Execute()
	rootCmd.SetOut(nil)
	if f := doctorCmd.Flags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
	}
	if !strings.Contains(buf.String(), "doctor") {
		t.Fatal("missing doctor in help")
	}
}

func TestDoctor_NoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0
	rootCmd.SetArgs([]string{"doctor"})
	_ = rootCmd.Execute()
	if lastExit != ExitBadArgs {
		t.Fatalf("expected exit %d, got %d", ExitBadArgs, lastExit)
	}
}

func TestDoctor_WithMockServer(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0
	rootCmd.SetArgs([]string{"doctor"})
	_ = rootCmd.Execute()
	if lastExit != ExitOK {
		t.Fatalf("expected exit 0, got %d", lastExit)
	}
}

func TestDoctor_NoConfig_JSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("KIBANA_CLI_HOST", "")
	origJM := jsonMode
	origExit := lastExit
	defer func() { jsonMode = origJM; lastExit = origExit }()
	lastExit = 0
	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"doctor", "--json"})
		_ = rootCmd.Execute()
	})
	if !strings.Contains(out, `"configExists"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
