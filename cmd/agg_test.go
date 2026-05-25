package cmd

import (
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

func TestResolveTermsField(t *testing.T) {
	fm := &fieldmap.Map{}
	resolved := fieldmap.ResolvedSearch{
		LevelFields:   []string{"log_level"},
		ServiceFields: []string{"log_app"},
	}
	if got := resolveTermsField("level", resolved, fm, ""); got != "log_level" {
		t.Fatalf("level: %q", got)
	}
	if got := resolveTermsField("service", resolved, fm, ""); got != "log_app" {
		t.Fatalf("service: %q", got)
	}
	if got := resolveTermsField("custom_field", resolved, fm, ""); got != "custom_field" {
		t.Fatalf("custom: %q", got)
	}
	empty := fieldmap.ResolvedSearch{}
	if got := resolveTermsField("level", empty, fm, ""); got != "level" {
		t.Fatalf("default level: %q", got)
	}
	if got := resolveTermsField("service", empty, fm, ""); got != "service_name" {
		t.Fatalf("default service: %q", got)
	}
}

func TestPrintAggResult_Table(t *testing.T) {
	resetCLIState(t)
	jsonMode = false
	out := captureStdout(t, func() {
		if err := printAggResult(&kibanaclient.AggResult{
			Field:   "level",
			Total:   10,
			TookMs:  3,
			Buckets: []kibanaclient.AggBucket{{Key: "ERROR", Count: 2}},
		}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "ERROR") {
		t.Fatalf("table output: %q", out)
	}
}

func TestAgg_TableOutput(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "ERROR") || !strings.Contains(out, "KEY") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestAgg_ProfileServiceTerms(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--profile", "e2e", "--terms", "service", "--service", "order-svc", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"buckets"`) {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestAgg_DryRun(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"dryRun"`) {
		t.Fatalf("expected dry-run: %s", out)
	}
}

func TestAgg_AllFieldsQuery(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--query", "timeout", "--all-fields", "--json"})
	if code != ExitOK {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_BucketsTooLow(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--buckets", "0", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_DataViewTimeField(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--data-view", "dv-1", "--terms", "level", "--time-field", "event.time", "--json"})
	if code != ExitOK {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_UnknownProfile(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--profile", "missing-profile", "--terms", "level", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_InvalidIndexPattern(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "_system", "--terms", "level", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_DataViewResolveError(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{IndexPatternFail: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--data-view", "bad-id", "--terms", "level", "--json"})
	if code == ExitOK {
		t.Fatal("expected API error")
	}
}

func TestAgg_UnknownService(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--service", "no-such-svc", "--terms", "level", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestAgg_InvalidFieldMap(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, "not: valid: yaml: [")
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}
