package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func resetCLIState(t *testing.T) {
	t.Helper()
	jsonMode = true
	jsonFlag = false
	outputFormat = FormatJSON
	compactMode = false
	forceMode = false
	quietMode = false
	dryRun = false
	confirmToken = ""
	insecureTLS = false
	timeoutSeconds = defaultTimeoutSeconds
	lastExit = 0
	activeCmd = nil
	output.Quiet = false
	output.JSONCompact = false
	resetCommandFlags(searchCmd, aggCmd, authLoginCmd, patternsListCmd, patternsFieldsCmd, configInitCmd, updateCmd, changelogCmd)
	resetCommandFlags(contextAddCmd, patternsInferCmd)
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
		err := ExecuteContext(context.Background())
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

func runConfirmedCLI(t *testing.T, args []string) (stdout string, exitCode int) {
	t.Helper()
	return runCLI(t, append(append([]string{}, args...), "--confirm", dryRunConfirmToken(t, args)))
}

func dryRunConfirmToken(t *testing.T, args []string) string {
	t.Helper()
	out, code := runCLI(t, dryRunArgs(args))
	if code != ExitOK {
		t.Fatalf("dry-run exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	token, _ := data["confirm_token"].(string)
	if token == "" {
		t.Fatalf("missing confirm token: %s", out)
	}
	return token
}

func dryRunArgs(args []string) []string {
	out := make([]string, 0, len(args)+3)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dry-run", "--json":
			continue
		case "--format", "--confirm":
			i++
			continue
		default:
			if strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "--confirm=") || strings.HasPrefix(arg, "--json=") {
				continue
			}
			out = append(out, arg)
		}
	}
	out = append(out, "--dry-run", "--format", "json")
	return out
}

func envelopePayload(t *testing.T, out string) map[string]any {
	t.Helper()
	j := lastJSONLine(out)
	var payload map[string]any
	if err := json.Unmarshal([]byte(j), &payload); err != nil {
		t.Fatalf("json parse: %v out=%s", err, out)
	}
	return payload
}

func envelopeData(t *testing.T, out string) map[string]any {
	t.Helper()
	payload := envelopePayload(t, out)
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %s", out)
	}
	return data
}

func envelopeErrorDetails(t *testing.T, out string) map[string]any {
	t.Helper()
	payload := envelopePayload(t, out)
	errObj, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error: %s", out)
	}
	details, ok := errObj["details"].(map[string]any)
	if !ok {
		t.Fatalf("missing error.details: %s", out)
	}
	return details
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
