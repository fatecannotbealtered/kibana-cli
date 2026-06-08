package cmd

import (
	"strings"
	"testing"
)

func TestPatterns_List_Table(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "list", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "logs-*") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestPatterns_List_JSONUntrusted(t *testing.T) {
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
	data := envelopeData(t, out)
	if _, ok := data["_untrusted"]; !ok {
		t.Fatalf("missing _untrusted marker: %s", out)
	}
}

func TestPatterns_List_JSONFieldsProjection(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "list", "--fields", "count", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["count"] == nil || data["patterns"] != nil {
		t.Fatalf("unexpected projection: %s", out)
	}
}

func TestPatterns_List_Pagination(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "list", "--limit", "1", "--offset", "0", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["limit"] != float64(1) || data["offset"] != float64(0) || data["has_more"] != false {
		t.Fatalf("unexpected pagination: %s", out)
	}
}

func TestPatterns_List_Empty(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{EmptyPatterns: true})
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "list", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "No index patterns") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestPatterns_Fields_Table(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "logs-*", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "@timestamp") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestPatterns_Fields_JSONUntrusted(t *testing.T) {
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
	data := envelopeData(t, out)
	if _, ok := data["_untrusted"]; !ok {
		t.Fatalf("missing _untrusted marker: %s", out)
	}
}

func TestPatterns_Fields_JSONFieldsProjection(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "logs-*", "--fields", "index,count", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["index"] == nil || data["count"] == nil || data["fields"] != nil {
		t.Fatalf("unexpected projection: %s", out)
	}
}

func TestPatterns_Fields_Pagination(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "logs-*", "--limit", "2", "--offset", "1", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["count"] != float64(2) || data["total"] != float64(3) || data["next_offset"] != nil {
		t.Fatalf("unexpected pagination: %s", out)
	}
}

func TestPatterns_Fields_Empty(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{EmptyFields: true})
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "logs-*", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "No fields returned") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestPatterns_List_APIError(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SavedObjectsFail: true, SavedObjectsStatus: 503})
	defer srv.Close()
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "list", "--json"})
	if code == ExitOK {
		t.Fatal("expected API error")
	}
}

func TestPatterns_Fields_APIError(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{FieldsAPIFail: true})
	defer srv.Close()
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "logs-*", "--json"})
	if code == ExitOK {
		t.Fatal("expected API error")
	}
}

func TestPatterns_Fields_NotConfigured(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"patterns", "fields", "--index", "logs-*", "--json"})
	if code != ExitAuth {
		t.Fatalf("got exit %d", code)
	}
}

func TestPatterns_Fields_EmptyIndex(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"patterns", "fields", "--index", "  ", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}
