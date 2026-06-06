package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/audit"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
)

func TestEmitAgentFailure_Quiet(t *testing.T) {
	resetCLIState(t)
	jsonMode = false
	outputFormat = FormatText
	quietMode = true
	output.Quiet = true
	defer func() { output.Quiet = false }()
	st := agentNotConfigured()
	out := captureCLIOutput(t, func() {
		emitAgentFailure(st)
	})
	if !strings.Contains(out, st.Message) {
		t.Fatalf("quiet should keep primary text errors: %q", out)
	}
	if strings.Contains(out, st.Hint) {
		t.Fatalf("quiet should suppress auxiliary text hints: %q", out)
	}
}

func TestDryRunOutput_Modes(t *testing.T) {
	resetCLIState(t)
	if dryRunOutput("preview", nil) {
		t.Fatal("dry-run flag off should return false")
	}
	dryRun = true
	defer func() { dryRun = false }()

	jsonMode = true
	out := captureStdout(t, func() {
		if !dryRunOutput("preview", map[string]any{"index": "logs-*"}) {
			t.Fatal("expected true")
		}
	})
	if !strings.Contains(out, `"dryRun":true`) && !strings.Contains(out, `"dryRun": true`) {
		t.Fatalf("json dry-run: %s", out)
	}

	jsonMode = false
	out = captureStdout(t, func() {
		_ = dryRunOutput("preview", nil)
	})
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("text dry-run: %s", out)
	}
}

func TestIsWriteCommand(t *testing.T) {
	if isWriteCommand(rootCmd) {
		t.Fatal("root is not a write command")
	}
	if !isWriteCommand(authLogoutCmd) || !isWriteCommand(configInitCmd) {
		t.Fatal("expected write commands")
	}
}

func TestNewKibanaClient_NotConfigured(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	client, cfg, err := newKibanaClient()
	if !errors.Is(err, ErrSilent) || client != nil || cfg != nil {
		t.Fatalf("client=%v cfg=%v err=%v", client, cfg, err)
	}
	if lastExit != ExitBadArgs {
		t.Fatalf("exit=%d", lastExit)
	}
}

func TestNewKibanaClient_ConfigError(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "://bad-url")
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	_, _, err := newKibanaClient()
	if !errors.Is(err, ErrSilent) || lastExit != ExitBadArgs {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestApplyInsecureFromEnv(t *testing.T) {
	resetCLIState(t)
	insecureTLS = false
	t.Setenv("KIBANA_CLI_INSECURE", "true")
	applyInsecureFromEnv()
	if !insecureTLS {
		t.Fatal("expected insecure from env")
	}
}

func TestAudit_LogOnWriteCommand(t *testing.T) {
	home := setupTestHome(t)
	auditDir := filepath.Join(home, ".kibana-cli", "audit-test")
	audit.SetDirForTest(auditDir)
	defer audit.SetDirForTest("")

	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"host":"http://x","username":"u","password":"p"}`), 0600); err != nil {
		t.Fatal(err)
	}

	_, code := runConfirmedCLI(t, []string{"auth", "logout", "--json"})
	if code != ExitOK {
		t.Fatal(code)
	}
	files, err := audit.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected audit log file after write command")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"cmd":"kibana-cli auth logout"`) {
		t.Fatalf("audit entry: %s", data)
	}
}

func TestSetExitCode_Max(t *testing.T) {
	lastExit = ExitOK
	setExitCode(ExitAuth)
	setExitCode(ExitBadArgs)
	if lastExit != ExitAuth {
		t.Fatalf("got %d", lastExit)
	}
}
