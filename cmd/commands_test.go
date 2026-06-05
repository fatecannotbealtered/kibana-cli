package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testFieldMapYAML = `version: 1
defaults:
  index: "logs-*"
  time_field: "@timestamp"
  service_fields: [log_app, service_name]
  level_fields: [level]
  message_fields: [msg, message]
profiles:
  e2e:
    index: "logs-*"
services:
  order-svc:
    match_fields: [service_name, log_app]
`

func TestRoot_Version(t *testing.T) {
	out, code := runCLI(t, []string{"--version"})
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "1.0.2") && !strings.Contains(out, "kibana-cli") {
		t.Fatalf("version output: %q", out)
	}
}

func TestRoot_Help(t *testing.T) {
	out, code := runCLI(t, []string{"--help"})
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"search", "auth", "doctor", "agg", "patterns", "config", "context", "update", "reference"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q: %s", want, out)
		}
	}
}

func TestAuth_Logout_RemovesConfig(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"host":"http://x","username":"u","password":"p"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, code := runCLI(t, []string{"auth", "logout", "--json"})
	if code != ExitOK {
		t.Fatal(code)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatal("config should be removed")
	}
}

func TestAuth_Status_Configured_Mock(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"auth", "status", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"configured":true`) && !strings.Contains(j, `"configured": true`) {
		t.Fatalf("unexpected: %s", out)
	}
	if !strings.Contains(j, `"ok":true`) && !strings.Contains(j, `"ok": true`) {
		t.Fatalf("missing ok:true: %s", out)
	}
}

func TestContext_Mock(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"authenticated":true`) && !strings.Contains(j, `"authenticated": true`) {
		t.Fatalf("unexpected: %s", out)
	}
	if !strings.Contains(j, `"ok":true`) && !strings.Contains(j, `"ok": true`) {
		t.Fatalf("missing ok:true: %s", out)
	}
	if !strings.Contains(j, `"message"`) {
		t.Fatalf("missing message: %s", out)
	}
}

func TestSearch_Mock_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"search", "--index", "logs-*", "--service", "order-svc", "--level", "ERROR", "--size", "5", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	jsonLine := lastJSONLine(out)
	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonLine), &payload); err != nil {
		t.Fatalf("json: %v out=%s", err, out)
	}
	hits, _ := payload["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("hits=%v", payload["hits"])
	}
}

func TestSearch_EmptyIndex(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"search", "--index", "", "--json"})
	if code == ExitOK {
		t.Fatal("expected error for empty index")
	}
}

func TestSearch_InvalidField(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"search", "--index", "logs-*", "--field", "bad", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_Mock_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"buckets"`) {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestAgg_BucketsValidation(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--buckets", "200", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected bad args, got %d", code)
	}
}

func TestPatterns_List_Mock(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "list", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), "logs-*") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestPatterns_Fields_Mock(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "logs-*", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"msg"`) || !strings.Contains(j, `"count"`) {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestDoctor_JSON_Success(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"doctor", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"ok":true`) && !strings.Contains(j, `"ok": true`) {
		t.Fatalf("unexpected: %s", out)
	}
	if !strings.Contains(j, `"message"`) {
		t.Fatalf("missing message: %s", out)
	}
}

func TestReference_AllCommandsListed(t *testing.T) {
	out, code := runCLI(t, []string{"reference"})
	if code != ExitOK {
		t.Fatal(code)
	}
	required := []string{
		"kibana-cli auth login",
		"kibana-cli auth logout",
		"kibana-cli auth status",
		"kibana-cli doctor",
		"kibana-cli context",
		"kibana-cli config init",
		"kibana-cli config show",
		"kibana-cli search",
		"kibana-cli agg",
		"kibana-cli patterns list",
		"kibana-cli patterns fields",
		"kibana-cli update",
		"kibana-cli reference",
	}
	for _, cmd := range required {
		if !strings.Contains(out, cmd) {
			t.Fatalf("reference missing %q", cmd)
		}
	}
}

func lastJSONLine(out string) string {
	if idx := strings.LastIndex(out, "✖"); idx >= 0 {
		out = out[idx+len("✖"):]
	}
	start := strings.Index(out, "{")
	if start < 0 {
		return strings.TrimSpace(out)
	}
	return extractJSONObject(out[start:])
}

func extractJSONObject(s string) string {
	depth := 0
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[:i+1])
			}
		}
	}
	return strings.TrimSpace(s)
}

func TestRequireSize_Cap(t *testing.T) {
	setupTestHome(t)
	srv := newMockKibanaServer()
	defer srv.Close()
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"search", "--index", "logs-*", "--size", "5000", "--json"})
	if code != ExitOK {
		t.Fatalf("size cap should still succeed, got %d", code)
	}
}
