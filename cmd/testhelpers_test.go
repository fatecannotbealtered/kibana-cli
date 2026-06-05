package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
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
	insecureTLS = false
	timeoutSeconds = defaultTimeoutSeconds
	lastExit = 0
	activeCmd = nil
	output.Quiet = false
	resetCommandFlags(searchCmd, aggCmd, authLoginCmd, patternsListCmd, patternsFieldsCmd, configInitCmd, updateCmd)
	resetCommandFlags(rootCmd)
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

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = stderrW
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderrBuf, stderrR)
		close(stderrDone)
	}()

	stdoutCaptured := captureStdout(t, func() {
		err := rootCmd.Execute()
		if err != nil && !errors.Is(err, ErrSilent) {
			t.Fatalf("execute %v: %v", args, err)
		}
	})
	_ = stderrW.Close()
	os.Stderr = origStderr
	<-stderrDone
	_ = stderrR.Close()
	return buf.String() + stdoutCaptured + stderrBuf.String(), lastExit
}

func runCLIWithEnv(t *testing.T, env map[string]string, args []string) (stdout string, exitCode int) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	return runCLI(t, args)
}

func runCLIWithStdin(t *testing.T, stdin string, args []string) (stdout string, exitCode int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	done := make(chan struct{})
	go func() {
		_, _ = io.WriteString(w, stdin)
		_ = w.Close()
		close(done)
	}()
	stdout, exitCode = runCLI(t, args)
	<-done
	os.Stdin = orig
	_ = r.Close()
	return stdout, exitCode
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureWriter(t, &os.Stdout, fn)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureWriter(t, &os.Stderr, fn)
}

func captureCLIOutput(t *testing.T, fn func()) string {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&outBuf, stdoutR)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&errBuf, stderrR)
	}()
	fn()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	os.Stdout, os.Stderr = origOut, origErr
	wg.Wait()
	_ = stdoutR.Close()
	_ = stderrR.Close()
	return outBuf.String() + errBuf.String()
}

func captureWriter(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := *target
	*target = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	_ = w.Close()
	*target = orig
	<-done
	_ = r.Close()
	return buf.String()
}
