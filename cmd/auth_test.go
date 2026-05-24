package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestAuth_Help_ListsSubcommands(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"auth", "--help"})
	_ = rootCmd.Execute()
	rootCmd.SetOut(nil)
	for _, want := range []string{"login", "logout", "status"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("missing %q in auth help", want)
		}
	}
}

func TestAuth_Login_DryRun(t *testing.T) {
	origDR, origJM, origExit := dryRun, jsonMode, lastExit
	defer func() { dryRun = origDR; jsonMode = origJM; lastExit = origExit }()
	lastExit = 0
	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"auth", "login",
			"--host", "https://kibana.example.com",
			"--user", "ops",
			"--password", "secret",
			"--dry-run", "--json"})
		_ = rootCmd.Execute()
	})
	if lastExit != ExitOK {
		t.Fatalf("exit %d", lastExit)
	}
	if !strings.Contains(out, `"dryRun": true`) {
		t.Fatalf("expected dry-run json: %s", out)
	}
}

func TestAuth_Login_NonInteractive(t *testing.T) {
	resetCLIState(t)
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	jsonMode = true
	rootCmd.SetArgs([]string{"auth", "login", "--host", srv.URL, "--user", "ops", "--password", "secret", "--plaintext"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".kibana-cli", "config.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAuth_Status_NoConfig(t *testing.T) {
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0
	rootCmd.SetArgs([]string{"auth", "status", "--json"})
	_ = rootCmd.Execute()
	if lastExit != ExitBadArgs {
		t.Fatal("expected ExitBadArgs")
	}
}
