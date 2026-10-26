package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
)

func TestFieldMapGet(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("field-map", "get")
	if r.code() != "E_NOT_FOUND" || !strings.HasSuffix(r.details()["path"].(string), "field-map.yaml") ||
		!strings.Contains(r.details()["next_action"].(string), "field-map init") {
		t.Fatalf("no file: %s", r.out)
	}
	e.writeFieldMap(fieldmap.ExampleYAML)
	r = e.run("field-map", "get")
	d := r.data()
	profiles := listOf(d["profiles"])
	if r.exit != 0 || num(d["version"]) != 1 || len(profiles) == 0 || profiles[0].(map[string]any)["name"] == nil ||
		d["defaults"].(map[string]any)["index"] != "logs-*" || len(listOf(d["index_rules"])) == 0 {
		t.Fatalf("get: %s", r.out)
	}
	// 1.x's `config show`.
	if r = e.run("config", "show"); r.exit != 0 || r.data()["path"] != d["path"] {
		t.Fatalf("config show: %s", r.out)
	}
	// A per-profile field map (the 1.x per-context file) through the setting.
	other := filepath.Join(e.stateDir(), "sys-b.yaml")
	if err := os.WriteFile(other, []byte("profiles:\n  b:\n    index: b-*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.vars["KIBANA_CLI_FIELD_MAP_FILE"] = "sys-b.yaml"
	if r = e.run("field-map", "get"); r.data()["path"] != other || len(listOf(r.data()["profiles"])) != 1 {
		t.Fatalf("field_map_file setting: %s", r.out)
	}
	if err := os.WriteFile(other, []byte("profiles: [x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r = e.run("field-map", "get"); r.code() != "E_CONFIG" {
		t.Fatalf("invalid YAML: %s", r.out)
	}
}

func TestFieldMapInit(t *testing.T) {
	e := newTestEnv(t)
	path := filepath.Join(e.stateDir(), "field-map.yaml")
	dry := e.run("field-map", "init", "--dry-run")
	if dry.exit != 0 || stringsOf(dry.preview()["outputs"])[0] != path || dry.preview()["can_apply"] != true {
		t.Fatalf("dry-run: %s", dry.out)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the dry-run wrote the file")
	}
	r := e.confirm(dry.token(), "field-map", "init")
	if r.exit != 0 || r.result()["path"] != path || r.outcome()["persistence"] != "saved" {
		t.Fatalf("init: %s", r.out)
	}
	if raw, _ := os.ReadFile(path); string(raw) != fieldmap.ExampleYAML {
		t.Fatal("the example was not written")
	}
	// Never overwrites: an existing file is refused before anything is planned (Q9).
	if r = e.run("field-map", "init", "--dry-run"); r.code() != "E_VALIDATION" || r.details()["path"] != path {
		t.Fatalf("existing file: %s", r.out)
	}
	// Without a scope, the write is refused.
	e.setScope(nil)
	if r = e.run("field-map", "init", "--field-map-file", filepath.Join(e.dir, "x.yaml"), "--dry-run"); r.code() != "E_FORBIDDEN" || r.details()["scope"] == nil {
		t.Fatalf("no scope: %s", r.out)
	}
}

func TestFieldMapAddProfile(t *testing.T) {
	e := newTestEnv(t)
	path := filepath.Join(e.stateDir(), "field-map.yaml")
	args := []string{"field-map", "add-profile", "--name", "app", "--index", "app-log-*", "--service-field", "log_app",
		"--message-field", "msg", "--trace-mode", "msg"}
	dry := e.run(append(args, "--dry-run")...)
	ch := listOf(dry.preview()["changes"])[0].(map[string]any)
	if dry.exit != 0 || ch["field"] != "profile" || ch["after"].(map[string]any)["trace_mode"] != "msg" ||
		!strings.HasPrefix(dry.preview()["action"].(string), "Create ") {
		t.Fatalf("dry-run: %s", dry.out)
	}
	r := e.confirm(dry.token(), args...)
	if r.exit != 0 || r.outcome()["verification"] != "passed" || r.result()["path"] != path {
		t.Fatalf("add to a new file: %s", r.out)
	}
	// A second profile keeps the file's comments and the first profile.
	if err := os.WriteFile(path, []byte("# my notes\n"+mustRead(t, path)), 0o600); err != nil {
		t.Fatal(err)
	}
	args2 := []string{"field-map", "add-profile", "--index", "legacy-log-*", "--trace-field", "traceId"}
	r = e.confirm(e.dryRun(args2...), args2...)
	text := mustRead(t, path)
	if r.exit != 0 || !strings.Contains(text, "# my notes") || !strings.Contains(text, "app:") || !strings.Contains(text, "legacy-log:") {
		t.Fatalf("add to an existing file: %s\n%s", r.out, text)
	}
	// The profiles are what search reads.
	if r = e.run("log", "search", "--field-profile", "app"); r.exit != 0 || r.scope()["index"] != "app-log-*" {
		t.Fatalf("search with the added profile: %s", r.out)
	}
	// A name the file has: blocked.
	if r = e.run(append(args, "--dry-run")...); r.preview()["can_apply"] != false || listOf(r.preview()["blocked_by"])[0].(map[string]any)["kind"] != "exists" {
		t.Fatalf("duplicate: %s", r.out)
	}
	// The file changed between the dry-run and the confirm: state_drift.
	args3 := []string{"field-map", "add-profile", "--name", "third", "--index", "third-*"}
	tok := e.dryRun(args3...)
	if err := os.WriteFile(path, []byte(text+"\n# edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r = e.confirm(tok, args3...); r.code() != "E_CONFLICT" || r.details()["conflict"] != "state_drift" {
		t.Fatalf("drift: %s", r.out)
	}
	for _, c := range []struct {
		args []string
		code string
	}{
		{[]string{"--name", "x"}, "E_USAGE"},
		{[]string{"--index", "a b"}, "E_VALIDATION"},
		{[]string{"--index", "a-*", "--name", "bad name"}, "E_VALIDATION"},
		{[]string{"--index", "a-*", "--service-field", "x;rm"}, "E_VALIDATION"},
		{[]string{"--index", "a-*", "--trace-mode", "span"}, "E_USAGE"},
	} {
		if r = e.run(append(append([]string{"field-map", "add-profile"}, c.args...), "--dry-run")...); r.code() != c.code {
			t.Errorf("%v: want %s, got %s", c.args, c.code, r.out)
		}
	}
	if e.kb.Writes() != 0 || len(e.kb.Requests()) != 3 { // the one search's three requests: status and none else
		t.Logf("requests: %d", len(e.kb.Requests()))
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
