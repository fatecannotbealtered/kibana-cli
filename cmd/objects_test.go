package cmd

import (
	"strings"
	"testing"
)

func objectsEnv(srvURL string) map[string]string {
	return map[string]string{
		"KIBANA_CLI_HOST":     srvURL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}
}

func TestObjects_List_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, objectsEnv(srv.URL), []string{
		"objects", "list", "--type", "dashboard", "--limit", "50", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["type"] != "dashboard" {
		t.Fatalf("type not echoed: %s", out)
	}
	if _, ok := data["objects"]; !ok {
		t.Fatalf("missing objects: %s", out)
	}
	tags, _ := data["_untrusted"].([]any)
	if len(tags) == 0 || tags[0] != "objects" {
		t.Fatalf("missing _untrusted objects tag: %s", out)
	}
}

func TestObjects_List_Search_Text(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, objectsEnv(srv.URL), []string{
		"objects", "list", "--type", "visualization", "--search", "latency", "--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Latency") || !strings.Contains(out, "ID") {
		t.Fatalf("unexpected text output: %s", out)
	}
}

func TestObjects_List_InvalidType(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"objects", "list", "--type", "config", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestObjects_List_DryRun(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, objectsEnv(srv.URL), []string{
		"objects", "list", "--type", "dashboard", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"dry_run"`) {
		t.Fatalf("expected dry-run: %s", out)
	}
}

func TestObjects_Get_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, objectsEnv(srv.URL), []string{
		"objects", "get", "--type", "dashboard", "--id", "abc-123", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["id"] != "abc-123" || data["type"] != "dashboard" {
		t.Fatalf("unexpected object: %s", out)
	}
	if data["title"] == nil {
		t.Fatalf("missing title: %s", out)
	}
}

func TestObjects_Get_InvalidType(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"objects", "get", "--type", "bogus", "--id", "x", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestObjects_Get_NotFound(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SavedObjectsFail: true, SavedObjectsStatus: 404})
	defer srv.Close()
	setupTestHome(t)
	_, code := runCLIWithEnv(t, objectsEnv(srv.URL), []string{
		"objects", "get", "--type", "dashboard", "--id", "missing", "--json",
	})
	if code == ExitOK {
		t.Fatal("expected error exit")
	}
}
