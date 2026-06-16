package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestPatternsInfer_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "infer", "--index", "app-test-log-*", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["profileName"] == "" {
		t.Fatalf("missing profileName: %s", out)
	}
	prof, ok := data["inferredProfile"].(map[string]any)
	if !ok {
		t.Fatalf("missing inferredProfile: %s", out)
	}
	// The mock fields API exposes msg + level (no service, no trace field).
	if prof["traceMode"] != "field" {
		t.Fatalf("traceMode = %v", prof["traceMode"])
	}
	if snippet, _ := data["yamlSnippet"].(string); !strings.Contains(snippet, "profiles:") {
		t.Fatalf("missing yaml snippet: %s", out)
	}
}

func TestPatternsInfer_Write_DryRunConfirm(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "infer", "--index", "app-test-log-*", "--name", "myprof", "--write", "--confirm", inferConfirmToken(t, srv.URL)})
	if code != ExitOK {
		t.Fatalf("write exit %d: %s", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".kibana-cli", "field-map.yaml"))
	if err != nil {
		t.Fatalf("field-map not written: %v", err)
	}
	if !strings.Contains(string(raw), "myprof") {
		t.Fatalf("profile not in field-map: %s", raw)
	}
}

// inferConfirmToken runs the infer write dry-run and returns its confirm token.
func inferConfirmToken(t *testing.T, host string) string {
	t.Helper()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     host,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "infer", "--index", "app-test-log-*", "--name", "myprof", "--write", "--dry-run", "--format", "json"})
	if code != ExitOK {
		t.Fatalf("infer dry-run exit %d: %s", code, out)
	}
	token, _ := envelopeData(t, out)["confirm_token"].(string)
	if token == "" {
		t.Fatalf("missing confirm token: %s", out)
	}
	return token
}
