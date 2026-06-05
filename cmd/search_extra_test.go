package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

const searchExtraFieldMap = `version: 1
defaults:
  index: "logs-*"
  time_field: "@timestamp"
  service_fields: [service_name]
  level_fields: [level]
  message_fields: [msg, message]
  trace_id_fields: [trace_id]
  trace_mode: field
profiles:
  e2e:
    index: "logs-*"
    trace_mode: msg
services:
  order-svc:
    match_fields: [service_name]
`

func searchMockEnv(srvURL string) map[string]string {
	return map[string]string{
		"KIBANA_CLI_HOST":     srvURL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}
}

func TestSearch_TextMode_Hits(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--service", "order-svc", "--level", "ERROR",
		"--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "timeout") || !strings.Contains(out, "ERROR") {
		t.Fatalf("expected text hit lines: %s", out)
	}
	if !strings.Contains(out, "hits on logs-*") {
		t.Fatalf("expected summary line: %s", out)
	}
}

func TestSearch_TextMode_NoHits(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchNoHits: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "No hits") {
		t.Fatalf("expected no hits message: %s", out)
	}
}

func TestSearch_ZeroHits_Diagnostics(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchNoHits: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--query", "timeout", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "zeroReason") || !strings.Contains(out, "no_data_in_window") {
		t.Fatalf("expected zero-hit diagnostics: %s", out)
	}
}

func TestExplainZeroHits_Reasons(t *testing.T) {
	t.Run("no data in window", func(t *testing.T) {
		client, closeFn := newCountClient(t, []int64{0}, 0)
		defer closeFn()
		reason, hint, probes := explainZeroHits(client, kibanaclient.SearchOptions{Index: "logs-*", Query: "timeout"}, false)
		if reason != "no_data_in_window" || hint == "" || probes["windowTotal"].(int64) != 0 {
			t.Fatalf("reason=%q hint=%q probes=%v", reason, hint, probes)
		}
	})

	t.Run("matched in other fields", func(t *testing.T) {
		client, closeFn := newCountClient(t, []int64{10, 3}, 0)
		defer closeFn()
		reason, hint, probes := explainZeroHits(client, kibanaclient.SearchOptions{Index: "logs-*", Query: "timeout"}, true)
		if reason != "matched_in_other_fields" || !strings.Contains(hint, "Drop --precise") || probes["broadMatchTotal"].(int64) != 3 {
			t.Fatalf("reason=%q hint=%q probes=%v", reason, hint, probes)
		}
	})

	t.Run("filters excluded all", func(t *testing.T) {
		client, closeFn := newCountClient(t, []int64{10}, 0)
		defer closeFn()
		reason, hint, probes := explainZeroHits(client, kibanaclient.SearchOptions{Index: "logs-*"}, false)
		if reason != "filters_excluded_all" || !strings.Contains(hint, "relax") || probes["windowTotal"].(int64) != 10 {
			t.Fatalf("reason=%q hint=%q probes=%v", reason, hint, probes)
		}
	})

	t.Run("probe failure", func(t *testing.T) {
		client, closeFn := newCountClient(t, nil, http.StatusBadGateway)
		defer closeFn()
		reason, hint, probes := explainZeroHits(client, kibanaclient.SearchOptions{Index: "logs-*"}, false)
		if reason != "" || hint != "" || probes != nil {
			t.Fatalf("reason=%q hint=%q probes=%v", reason, hint, probes)
		}
	})
}

func TestSearch_TextMode_TraceHint(t *testing.T) {
	traceMsg := "[aabbccdd00112233445566778899aabb, 00cfa11a2dfa446a920d59a76aa56df1] worker timeout"
	srv := newMockKibanaServerWith(mockKibanaOptions{
		SearchSource: map[string]any{
			"@timestamp":   "2024-01-01T00:00:00Z",
			"level":        "ERROR",
			"service_name": "order-svc",
			"msg":          traceMsg,
		},
	})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "aabbccdd") {
		t.Fatalf("expected trace hint prefix: %s", out)
	}
}

func TestSearch_BroadDefault_Query(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--query", "timeout", "--json",
	})
	if code != ExitOK {
		t.Fatal(code)
	}
}

func TestSearch_Precise_Query(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--query", "timeout", "--precise", "--json",
	})
	if code != ExitOK {
		t.Fatal(code)
	}
}

func TestSearch_TraceFlags(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*",
		"--trace-id", "abc123",
		"--trace-mode", "msg",
		"--trace-field", "log_traceId",
		"--json",
	})
	if code != ExitOK {
		t.Fatal(code)
	}
}

func TestSearch_Profile_TimeField_Fields(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--profile", "e2e", "--service", "order-svc",
		"--time-field", "@timestamp",
		"--fields", "@timestamp,msg",
		"--field", "level=ERROR",
		"--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"hits"`) {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestSearch_DataView_Mock(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--data-view", "dv-1", "--json",
	})
	if code != ExitOK {
		t.Fatal(code)
	}
}

func TestSearch_UnknownProfile(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLI(t, []string{"search", "--profile", "missing", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestSearch_InvalidFieldMap(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, ":\nnot: valid: yaml: [")
	_, code := runCLI(t, []string{"search", "--index", "logs-*", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestSearch_InvalidSize(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"search", "--index", "logs-*", "--size", "0", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestSearch_DataView_APIError(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{IndexPatternFail: true, IndexPatternStatus: 404})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--data-view", "bad-id", "--json",
	})
	if code != ExitNotFound {
		t.Fatalf("got exit %d", code)
	}
}

func TestSearch_Quiet_JSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--quiet", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"ok":true`) && !strings.Contains(lastJSONLine(out), `"ok": true`) {
		t.Fatalf("json should still print: %s", out)
	}
}

func TestFirstMessageAndFieldValue(t *testing.T) {
	src := map[string]any{"message": "from-message", "level": "WARN"}
	if got := firstMessage(src, []string{"msg"}); got != "from-message" {
		t.Fatalf("firstMessage=%q", got)
	}
	if got := firstFieldValue(src, []string{"level"}); got != "WARN" {
		t.Fatalf("firstFieldValue=%q", got)
	}
	if got := firstMessage(map[string]any{}, nil); got != "" {
		t.Fatal("expected empty")
	}
}

func newCountClient(t *testing.T, totals []int64, failStatus int) (*kibanaclient.Client, func()) {
	t.Helper()
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/console/proxy" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if failStatus != 0 {
			w.WriteHeader(failStatus)
			_, _ = w.Write([]byte(`{"error":{"reason":"count failed"}}`))
			return
		}
		total := totals[len(totals)-1]
		if call < len(totals) {
			total = totals[call]
		}
		call++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 1,
			"hits": map[string]any{
				"total": map[string]any{"value": total, "relation": "eq"},
				"hits":  []any{},
			},
		})
	}))
	client := kibanaclient.NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	return client, srv.Close
}
