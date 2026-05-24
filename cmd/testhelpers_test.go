package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func resetCLIState(t *testing.T) {
	t.Helper()
	jsonMode = false
	forceMode = false
	quietMode = false
	dryRun = false
	lastExit = 0
	activeCmd = nil
	output.Quiet = false
	resetCommandFlags(searchCmd, aggCmd, authLoginCmd, patternsListCmd, patternsFieldsCmd, configInitCmd)
}

func resetCommandFlags(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			switch f.Value.Type() {
			case "stringArray", "stringSlice":
				if sv, ok := f.Value.(pflag.SliceValue); ok {
					_ = sv.Replace([]string{})
				} else {
					_ = f.Value.Set("")
				}
			default:
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	}
}

func setupTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func writeFieldMap(t *testing.T, home string, content string) {
	t.Helper()
	dir := filepath.Join(home, ".kibana-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "field-map.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args []string) (stdout string, exitCode int) {
	t.Helper()
	resetCLIState(t)
	rootCmd.SetArgs(args)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	stdoutCaptured := captureStdout(t, func() {
		err := rootCmd.Execute()
		if err != nil && !errors.Is(err, ErrSilent) {
			t.Fatalf("execute %v: %v", args, err)
		}
	})
	return buf.String() + stdoutCaptured, lastExit
}

func runCLIWithEnv(t *testing.T, env map[string]string, args []string) (stdout string, exitCode int) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	return runCLI(t, args)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	<-done
	_ = r.Close()
	return buf.String()
}
