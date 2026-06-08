package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const configTestFieldMapYAML = `version: 1
defaults:
  index: "logs-*"
  time_field: "@timestamp"
profiles:
  demo:
    index: "logs-*"
services:
  order-svc:
    match_fields: [service_name]
`

func TestConfig_Init_JSON(t *testing.T) {
	home := setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{"config", "init", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"ok":true`) && !strings.Contains(lastJSONLine(out), `"ok": true`) {
		t.Fatalf("unexpected: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".kibana-cli", "field-map.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestConfig_Init_Text(t *testing.T) {
	home := setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{"config", "init", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Created ") || !strings.Contains(out, "field-map.yaml") {
		t.Fatalf("expected success text: %s", out)
	}
	_ = home
}

func TestConfig_Init_DryRun_JSON(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"config", "init", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"confirm_token"`) || !strings.Contains(out, `"preview"`) {
		t.Fatalf("expected confirm-token dry-run: %s", out)
	}
}

func TestConfig_Init_DryRun_Text(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"config", "init", "--dry-run", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("expected dry-run text: %s", out)
	}
}

func TestConfig_Init_AlreadyExists(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, "version: 1\n")
	_, code := runCLI(t, []string{"config", "init", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestConfig_Init_AlreadyExists_Text(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, "version: 1\n")
	out, code := runCLI(t, []string{"config", "init", "--format", "text"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
	if !strings.Contains(out, "field-map already exists") {
		t.Fatalf("expected validation message: %s", out)
	}
}

func TestConfig_Init_ForceOverwrite(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, "version: 1\ndefaults:\n  index: old-*\n")
	_, code := runConfirmedCLI(t, []string{"config", "init", "--force", "--json"})
	if code != ExitOK {
		t.Fatal(code)
	}
	data, err := os.ReadFile(filepath.Join(home, ".kibana-cli", "field-map.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "java-app") {
		t.Fatalf("expected example yaml, got %s", data)
	}
}

func TestConfig_Init_MkdirError(t *testing.T) {
	home := setupTestHome(t)
	cliDir := filepath.Join(home, ".kibana-cli")
	if err := os.WriteFile(cliDir, []byte("blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	_, code := runConfirmedCLI(t, []string{"config", "init", "--force", "--json"})
	if code != ExitAuth {
		t.Fatalf("expected ExitAuth, got %d", code)
	}
}

func TestConfig_Init_WriteError(t *testing.T) {
	home := setupTestHome(t)
	cliDir := filepath.Join(home, ".kibana-cli")
	if err := os.MkdirAll(cliDir, 0700); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(cliDir, "field-map.yaml")
	if err := os.MkdirAll(mapPath, 0700); err != nil {
		t.Fatal(err)
	}
	_, code := runConfirmedCLI(t, []string{"config", "init", "--force", "--json"})
	if code != ExitAuth {
		t.Fatalf("expected ExitAuth, got %d", code)
	}
}

func TestConfig_Show_JSON_WithMap(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, configTestFieldMapYAML)
	out, code := runCLI(t, []string{"config", "show", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"fieldMap"`) {
		t.Fatalf("show output: %s", out)
	}
}

func TestConfig_Show_Text_WithMap(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, configTestFieldMapYAML)
	out, code := runCLI(t, []string{"config", "show", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"field-map.yaml", "defaults.index", "profile: demo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
}

func TestConfig_Show_Missing_JSON(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"config", "show", "--json"})
	if code != ExitNotFound {
		t.Fatalf("expected ExitNotFound, got %d", code)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"exists":false`) && !strings.Contains(j, `"exists": false`) {
		t.Fatalf("unexpected: %s", j)
	}
}

func TestConfig_Show_Missing_Text(t *testing.T) {
	setupTestHome(t)
	var stdout string
	stderr := captureStderr(t, func() {
		stdout, _ = runCLI(t, []string{"config", "show", "--format", "text"})
	})
	if !strings.Contains(stderr, "field-map.yaml") && !strings.Contains(stdout, "field-map.yaml") {
		t.Fatalf("expected warning: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestConfig_Show_InvalidYAML(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, "not: valid: yaml: [")
	_, code := runCLI(t, []string{"config", "show", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}
