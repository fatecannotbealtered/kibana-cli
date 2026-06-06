package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOutputFormat_DefaultJSONWithoutJsonFlag(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)

	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lastJSONLine(out)), &payload); err != nil {
		t.Fatalf("default output should be json: %v out=%s", err, out)
	}
	data := envelopeData(t, out)
	if payload["ok"] != true || data["hits"] == nil {
		t.Fatalf("unexpected payload: %s", out)
	}
}

func TestOutputFormat_CompactJSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()

	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{"context", "--compact"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	trimmed := strings.TrimSpace(out)
	if strings.Contains(trimmed, "\n") {
		t.Fatalf("compact json should be single-line: %q", out)
	}
	if !strings.Contains(trimmed, `"ok":true`) {
		t.Fatalf("unexpected compact json: %s", out)
	}
}

func TestOutputFormat_QuietDoesNotSuppressDefaultJSON(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")

	out, code := runCLI(t, []string{"context", "--quiet"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lastJSONLine(out)), &payload); err != nil {
		t.Fatalf("quiet default output should still be json: %v out=%s", err, out)
	}
	details := envelopeErrorDetails(t, out)
	if details["status"] != AgentStatusNotConfigured {
		t.Fatalf("unexpected payload: %s", out)
	}
}

func TestOutputFormat_JSONAliasConflictsWithNonJSONFormat(t *testing.T) {
	for _, jsonArg := range []string{"--json", "--json=false"} {
		for _, format := range []string{"text", "raw"} {
			t.Run(jsonArg+"_"+format, func(t *testing.T) {
				out, code := runCLI(t, []string{"context", jsonArg, "--format", format})
				if code != ExitBadArgs {
					t.Fatalf("exit %d: %s", code, out)
				}
				want := "--json cannot be combined with --format " + format
				if !strings.Contains(out, want) {
					t.Fatalf("unexpected conflict output: %s", out)
				}
			})
		}
	}
}

func TestOutputFormat_RawUnsupportedExplicitError(t *testing.T) {
	out, code := runCLI(t, []string{"search", "--index", "logs-*", "--format", "raw"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "does not support --format raw") {
		t.Fatalf("unexpected raw error: %s", out)
	}
}

func TestOutputFormat_ReferenceRaw(t *testing.T) {
	out, code := runCLI(t, []string{"reference", "--format", "raw", "--quiet"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "# kibana-cli Command Reference") || strings.Contains(out, `"content"`) {
		t.Fatalf("raw reference should be markdown, not json: %s", out)
	}
}

func TestOutputFormat_FieldsOnlySupportedWithJSON(t *testing.T) {
	out, code := runCLI(t, []string{
		"search", "--index", "logs-*", "--fields", "msg", "--format", "text",
	})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "--fields is only supported with --format json") {
		t.Fatalf("unexpected fields error: %s", out)
	}
}

func TestOutputFormat_TextQuietKeepsPrimaryContextOutput(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()

	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"context", "--format", "text", "--quiet",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Ready for log search") || !strings.Contains(out, "Host:") {
		t.Fatalf("quiet text should keep primary context output: %s", out)
	}
	if strings.Contains(out, "kibana-cli Context") {
		t.Fatalf("quiet text should suppress auxiliary heading: %s", out)
	}
}

func TestOutputFormat_TextQuietKeepsPrimaryConfigShowOutput(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, configTestFieldMapYAML)

	out, code := runCLI(t, []string{"config", "show", "--format", "text", "--quiet"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "defaults.index") || !strings.Contains(out, "profiles:") {
		t.Fatalf("quiet text should keep primary config output: %s", out)
	}
	if strings.Contains(out, "field-map.yaml") {
		t.Fatalf("quiet text should suppress auxiliary config heading/path: %s", out)
	}
}

func TestOutputFormat_TextQuietKeepsEmptySearchPrimaryOutput(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchNoHits: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)

	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--format", "text", "--quiet",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "No hits") {
		t.Fatalf("quiet text should keep empty search result: %s", out)
	}
	if strings.Contains(out, "widen --from/--to") {
		t.Fatalf("quiet text should suppress auxiliary zero-hit hint: %s", out)
	}
}
