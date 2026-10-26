package cmd

import (
	"strings"
	"testing"
)

// doctorChecks returns doctor's checks by name.
func doctorChecks(t *testing.T, r cliResult) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, c := range listOf(r.data()["checks"]) {
		m := c.(map[string]any)
		out[m["check"].(string)] = m
	}
	return out
}

func TestDoctor(t *testing.T) {
	e := newTestEnv(t)
	c := doctorChecks(t, e.run("doctor"))
	// Kibana 9 hides its version from this account; Elasticsearch tells it.
	if c["connectivity"]["status"] != "pass" || c["credentials"]["status"] != "pass" || c["console_proxy"]["status"] != "pass" ||
		c["kibana_version"]["status"] != "pass" || !strings.Contains(c["kibana_version"]["message"].(string), "Kibana 9.5.2, from Elasticsearch") {
		t.Fatalf("checks: %v", c)
	}
	e.kb.fullStatus = true
	if c = doctorChecks(t, e.run("doctor")); !strings.Contains(c["kibana_version"]["message"].(string), "from Kibana's GET /api/status") {
		t.Fatalf("status with the version: %v", c["kibana_version"])
	}
	e.kb.fullStatus, e.kb.esRootForbidden = false, true
	if c = doctorChecks(t, e.run("doctor")); c["kibana_version"]["status"] != "pass" || !strings.HasPrefix(c["kibana_version"]["message"].(string), "Unknown") {
		t.Fatalf("version unknown: %v", c["kibana_version"])
	}
	e.vars["KIBANA_CLI_KIBANA_VERSION"] = "9.5.0"
	if c = doctorChecks(t, e.run("doctor")); !strings.Contains(c["kibana_version"]["message"].(string), "9.5.0, from the kibana_version setting") {
		t.Fatalf("configured version: %v", c["kibana_version"])
	}
	e.vars["KIBANA_CLI_KIBANA_VERSION"], e.kb.esRootForbidden = "", false
	// Kibana 9's restriction, when a proxy strips the header: named as what it is.
	e.kb.stripOrigin = true
	c = doctorChecks(t, e.run("doctor"))
	if c["console_proxy"]["status"] != "fail" || !strings.Contains(c["console_proxy"]["message"].(string), "x-elastic-internal-origin") {
		t.Fatalf("internal origin: %v", c["console_proxy"])
	}
	e.kb.stripOrigin = false
	e.vars["KIBANA_CLI_PASSWORD"] = "wrong"
	if c = doctorChecks(t, e.run("doctor")); c["credentials"]["status"] != "fail" || c["connectivity"]["status"] != "pass" {
		t.Fatalf("wrong password: %v", c)
	}
	e.vars["KIBANA_CLI_PASSWORD"] = ""
	if c = doctorChecks(t, e.run("doctor")); c["credentials"]["status"] != "fail" || !strings.Contains(c["credentials"]["fix"].(string), "auth login") {
		t.Fatalf("no password: %v", c)
	}
	e.vars["KIBANA_CLI_HOST"] = "http://127.0.0.1:1"
	if c = doctorChecks(t, e.run("doctor")); c["connectivity"]["status"] != "fail" {
		t.Fatalf("unreachable: %v", c)
	}
	e.vars["KIBANA_CLI_HOST"] = ""
	if c = doctorChecks(t, e.run("doctor")); c["connectivity"]["status"] != "warn" {
		t.Fatalf("not configured: %v", c)
	}
}

func TestContextReportsTheFieldMapAndAllowlist(t *testing.T) {
	e := newTestEnv(t)
	e.writeFieldMap("profiles:\n  app:\n    index: app-*\n")
	e.vars["KIBANA_CLI_ALLOWED_INDEX_PREFIXES"] = "app-,legacy-"
	e.vars["KIBANA_CLI_DEFAULT_INDEX"] = "app-log-*"
	r := e.run("context")
	d := r.data()
	raw := r.out
	if r.exit != 0 || d["account"] != testUser || !strings.Contains(raw, `"allowed_index_prefixes":["app-","legacy-"]`) ||
		!strings.Contains(raw, `"default_index":"app-log-*"`) || !strings.Contains(raw, `"profiles":["app"]`) {
		t.Fatalf("context: %s", r.out)
	}
	if len(e.kb.Requests()) != 0 {
		t.Fatal("context made a request")
	}
}
