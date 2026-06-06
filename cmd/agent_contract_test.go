package cmd

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentContract_AuthFailed(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{AuthFail: true})
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--json"})
	if code != ExitAuth {
		t.Fatalf("exit %d want %d: %s", code, ExitAuth, out)
	}
	assertAgentJSON(t, out, false, AgentStatusAuthFailed, ExitAuth)
}

func TestAgentContract_SearchUnavailable(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchProbeFail: true, SearchProbeStatus: http.StatusForbidden})
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--json"})
	if code != ExitForbidden {
		t.Fatalf("exit %d want %d: %s", code, ExitForbidden, out)
	}
	assertAgentJSON(t, out, false, AgentStatusSearchUnavailable, ExitForbidden)
}

func TestAgentContract_Ready(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	assertAgentJSON(t, out, true, AgentStatusReady, ExitOK)
}

func TestValidationError_JSONEnvelope(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"search", "--index", "logs-*", "--field", "bad", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	assertAgentJSON(t, out, false, StatusValidationError, ExitBadArgs)
}

func TestSearch_DryRun_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"search", "--index", "logs-*", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"dryRun": true`) && !strings.Contains(out, `"dryRun":true`) {
		t.Fatalf("expected dry-run json: %s", out)
	}
}

func TestSearch_DryRun_DataView_SkipsResolve(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{IndexPatternFail: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"search", "--data-view", "dv-1", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	var payload map[string]any
	if err := json.Unmarshal([]byte(j), &payload); err != nil {
		t.Fatalf("json parse: %v out=%s", err, out)
	}
	payload = envelopeData(t, out)
	if idx, _ := payload["index"].(string); idx != "<data-view:dv-1>" {
		t.Fatalf("index=%q want placeholder json=%s", idx, j)
	}
}

func TestMainProcess_ExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("subprocess exit test skipped on windows in CI matrix")
	}
	bin := buildCLIBinary(t)
	cmd := exec.Command(bin, "context", "--json")
	home := setupTestHomeDir(t)
	cmd.Env = filteredEnv(home, map[string]string{
		"KIBANA_CLI_HOST":     "",
		"KIBANA_CLI_USER":     "",
		"KIBANA_CLI_PASSWORD": "",
	})
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != ExitBadArgs {
		t.Fatalf("exit code %v output %s", err, out)
	}
	if !strings.Contains(string(out), AgentStatusNotConfigured) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func assertAgentJSON(t *testing.T, out string, wantOK bool, wantStatus string, wantExit int) {
	t.Helper()
	payload := envelopePayload(t, out)
	ok, _ := payload["ok"].(bool)
	if ok != wantOK {
		t.Fatalf("ok=%v want %v json=%s", ok, wantOK, lastJSONLine(out))
	}
	var fields map[string]any
	if wantOK {
		fields = envelopeData(t, out)
	} else {
		fields = envelopeErrorDetails(t, out)
	}
	if status, _ := fields["status"].(string); status != wantStatus {
		t.Fatalf("status=%q want %q json=%s", status, wantStatus, lastJSONLine(out))
	}
	exitF, ok := fields["exitCode"].(float64)
	if !ok || int(exitF) != wantExit {
		t.Fatalf("exitCode=%v want %d json=%s", fields["exitCode"], wantExit, lastJSONLine(out))
	}
}

func setupTestHomeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "kibana-cli")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "./kibana-cli").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	return bin
}
