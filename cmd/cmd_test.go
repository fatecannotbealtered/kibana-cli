package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitCodeForStatus(t *testing.T) {
	tests := map[int]int{
		401: ExitAuth,
		403: ExitForbidden,
		404: ExitNotFound,
		429: ExitRateLimit,
		500: ExitNetwork,
		400: ExitBadArgs,
	}
	for status, want := range tests {
		if got := exitCodeForStatus(status); got != want {
			t.Errorf("status %d: got %d want %d", status, got, want)
		}
	}
}

func TestReferenceCommand(t *testing.T) {
	resetCLIState(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"reference", "--format", "text"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "kibana-cli search") {
		t.Fatalf("missing search in reference")
	}
	if strings.Contains(buf.String(), "install-skill") {
		t.Fatal("install-skill should not be in reference")
	}
}

func TestSearchRequiresIndex(t *testing.T) {
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     "http://127.0.0.1:9",
		"KIBANA_CLI_USER":     "u",
		"KIBANA_CLI_PASSWORD": "p",
		"HOME":                t.TempDir(),
	}, []string{"search", "--index", "", "--json"})
	if code == ExitOK {
		t.Fatalf("expected error for empty --index, got exit %d out=%s", code, out)
	}
}

func TestGetFieldsFlag(t *testing.T) {
	rootCmd.SetArgs([]string{"search", "--fields", " @timestamp , level "})
	_ = searchCmd.Flags().Set("fields", "@timestamp, level")
	got := getFieldsFlag(searchCmd)
	if len(got) != 2 || got[0] != "@timestamp" || got[1] != "level" {
		t.Fatalf("got %v", got)
	}
}
