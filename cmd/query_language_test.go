package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/zalando/go-keyring"
)

func TestQueryCommandsRejectExtraArguments(t *testing.T) {
	tests := [][]string{
		{"search", "--index", "logs-*", "--query", `msg:"1"`, "and", `msg:"2"`, "--dry-run", "--json"},
		{"agg", "--index", "logs-*", "--terms", "level", "--query", `msg:"1"`, "and", `msg:"2"`, "--dry-run", "--json"},
	}
	for _, args := range tests {
		_, code := runCLI(t, args)
		if code != ExitBadArgs {
			t.Fatalf("%s accepted extra arguments, exit=%d", args[0], code)
		}
	}
}

func TestAmbiguousLuceneBoolean(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{query: `msg:"1" and msg:"2"`, want: true},
		{query: `msg:"1" AnD msg:"2"`, want: true},
		{query: `(msg:"1" and msg:"2")`, want: true},
		{query: `日志.msg:"1" and 日志.msg:"2"`, want: true},
		{query: `kubernetes.container/name:"a" or kubernetes.container/name:"b"`, want: true},
		{query: `tag:(not legacy)`, want: true},
		{query: `a:1 AND not b:2`, want: true},
		{query: `a:1 OR not b:2`, want: true},
		{query: `msg:"1" AND msg:"2"`},
		{query: `msg:"rock and roll"`},
		{query: `rock and roll`, want: true},
		{query: `msg:and`},
	}
	for _, tt := range tests {
		if got := ambiguousLuceneBoolean(tt.query); got != tt.want {
			t.Fatalf("ambiguousLuceneBoolean(%q)=%v want %v", tt.query, got, tt.want)
		}
	}
}

func TestSearchQueryLanguages(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	env := searchMockEnv(srv.URL)

	out, code := runCLIWithEnv(t, env, []string{
		"search", "--index", "logs-*", "--query", `msg:"1" and msg:"2"`,
		"--query-language", "kql", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("KQL exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["queryLanguage"] != "kql" || data["context"] != "env" || data["contextSource"] != "environment_auth" || data["host"] != srv.URL {
		t.Fatalf("missing query provenance: %s", out)
	}
	dslJSON, _ := json.Marshal(data["dsl"])
	if !strings.Contains(string(dslJSON), `"match_phrase":{"msg":"1"}`) ||
		!strings.Contains(string(dslJSON), `"match_phrase":{"msg":"2"}`) ||
		strings.Contains(string(dslJSON), `"query_string"`) {
		t.Fatalf("unexpected KQL DSL: %s", dslJSON)
	}

	out, code = runCLIWithEnv(t, env, []string{
		"search", "--index", "logs-*", "--query", `msg:"1" and msg:"2"`, "--dry-run", "--json",
	})
	if code != ExitBadArgs || !strings.Contains(out, "--query-language kql") {
		t.Fatalf("ambiguous Lucene must fail closed, exit=%d out=%s", code, out)
	}

	out, code = runCLIWithEnv(t, env, []string{
		"search", "--index", "logs-*", "--query", `msg:"1" AND msg:"2"`, "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("Lucene exit %d: %s", code, out)
	}
	data = envelopeData(t, out)
	dslJSON, _ = json.Marshal(data["dsl"])
	if data["queryLanguage"] != "lucene" || !strings.Contains(string(dslJSON), `"query":"msg:\"1\" AND msg:\"2\""`) {
		t.Fatalf("unexpected Lucene DSL: %s", out)
	}

	out, code = runCLIWithEnv(t, env, []string{
		"search", "--index", "logs-*", "--query", "rock and roll",
		"--query-language", "lucene", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("explicit Lucene literal exit %d: %s", code, out)
	}
}

func TestDefaultLuceneRejectsUnfieldedKQLBoolean(t *testing.T) {
	for _, query := range []string{"timeout and error", "a:1 AND not b:2", "a:1 OR not b:2"} {
		out, code := runCLI(t, []string{
			"search", "--index", "logs-*", "--query", query, "--dry-run", "--json",
		})
		if code != ExitBadArgs || !strings.Contains(out, "--query-language kql") {
			t.Fatalf("query=%q exit=%d out=%s", query, code, out)
		}
	}
}

func TestPreciseTreatsLowercaseBooleanWordsAsLiteralText(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	env := searchMockEnv(srv.URL)
	for _, args := range [][]string{
		{"search", "--index", "logs-*", "--query", "rock and roll", "--precise", "--dry-run", "--json"},
		{"agg", "--index", "logs-*", "--terms", "level", "--query", "rock and roll", "--precise", "--dry-run", "--json"},
	} {
		out, code := runCLIWithEnv(t, env, args)
		if code != ExitOK {
			t.Fatalf("args=%v exit=%d out=%s", args, code, out)
		}
		dslJSON, _ := json.Marshal(envelopeData(t, out)["dsl"])
		if !strings.Contains(string(dslJSON), `"match_phrase"`) || !strings.Contains(string(dslJSON), "rock and roll") {
			t.Fatalf("precise query was not literal: %s", dslJSON)
		}
	}
}

func TestSearchKQLPreservesUserPhrase(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	phrase := "门锁语音业务 [11111111-2222-3333-4444-555555555555]"
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--query", `msg:"` + phrase + `"`,
		"--query-language", "kql", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	dslJSON, _ := json.Marshal(envelopeData(t, out)["dsl"])
	if !strings.Contains(string(dslJSON), `"match_phrase":{"msg":`+mustJSONText(t, phrase)) {
		t.Fatalf("phrase changed in DSL: %s", dslJSON)
	}
}

func mustJSONText(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestQueryLanguageValidationAndConflicts(t *testing.T) {
	tests := [][]string{
		{"search", "--index", "logs-*", "--query", "a:1", "--query-language", "sql", "--dry-run", "--json"},
		{"search", "--index", "logs-*", "--query", "a:(", "--query-language", "kql", "--dry-run", "--json"},
		{"search", "--index", "logs-*", "--query", "machine*:osx", "--query-language", "kql", "--dry-run", "--json"},
		{"search", "--index", "logs-*", "--query", "a:1", "--query-language", "kql", "--precise", "--dry-run", "--json"},
		{"search", "--index", "logs-*", "--dsl", `{"query":{"match_all":{}}}`, "--query-language", "kql", "--dry-run", "--json"},
	}
	for _, args := range tests {
		_, code := runCLI(t, args)
		if code != ExitBadArgs {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
}

func TestAggKQLDryRunIncludesFinalBody(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"agg", "--index", "logs-*", "--terms", "level", "--query", `msg:"1" and msg:"2"`,
		"--query-language", "kql", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	dsl, ok := data["dsl"].(map[string]any)
	if !ok || dsl["query"] == nil || dsl["aggs"] == nil || data["queryLanguage"] != "kql" {
		t.Fatalf("dry-run is not the final initial request: %s", out)
	}

	out, code = runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"agg", "--index", "logs-*", "--terms", "level", "--query", "timeout and error", "--dry-run", "--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("default lowercase boolean exit=%d out=%s", code, out)
	}
	out, code = runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"agg", "--index", "logs-*", "--terms", "level", "--query", "rock and roll", "--query-language", "lucene", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("explicit Lucene exit=%d out=%s", code, out)
	}
	out, code = runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"agg", "--index", "logs-*", "--terms", "level", "--query", "a:1", "--query-language", "kql", "--precise", "--dry-run", "--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("KQL precise conflict exit=%d out=%s", code, out)
	}
}

func TestCommandDryRunBodyMatchesFirstRequest(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/console/proxy" && r.URL.Query().Get("method") == "POST" {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &captured)
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		mockKibanaHandlerWith(w, r, mockKibanaOptions{})
	}))
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	env := searchMockEnv(srv.URL)
	tests := []struct {
		name string
		args []string
	}{
		{name: "search", args: []string{"search", "--index", "logs-*", "--query", `msg:"1" and msg:"2"`, "--query-language", "kql", "--dry-run", "--json"}},
		{name: "agg", args: []string{"agg", "--index", "logs-*", "--terms", "level", "--query", `msg:"1" and msg:"2"`, "--query-language", "kql", "--dry-run", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captured = nil
			out, code := runCLIWithEnv(t, env, tt.args)
			if code != ExitOK {
				t.Fatalf("dry-run exit %d: %s", code, out)
			}
			preview := envelopeData(t, out)["dsl"]
			if captured != nil {
				t.Fatal("dry-run sent a search request")
			}
			liveArgs := make([]string, 0, len(tt.args)-1)
			for _, arg := range tt.args {
				if arg != "--dry-run" {
					liveArgs = append(liveArgs, arg)
				}
			}
			out, code = runCLIWithEnv(t, env, liveArgs)
			if code != ExitOK {
				t.Fatalf("live exit %d: %s", code, out)
			}
			if !reflect.DeepEqual(preview, captured) {
				t.Fatalf("preview != first request\npreview=%#v\nrequest=%#v", preview, captured)
			}
		})
	}
}

func TestDataViewTimeFieldPrecedence(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	env := searchMockEnv(srv.URL)

	out, code := runCLIWithEnv(t, env, []string{
		"search", "--data-view", "dv-1", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if got := data["timeField"]; got != "event.time" {
		t.Fatalf("data-view time field=%v out=%s", got, out)
	}
	dslJSON, _ := json.Marshal(data["dsl"])
	if !strings.Contains(string(dslJSON), `"event.time"`) || strings.Contains(string(dslJSON), `"@timestamp"`) {
		t.Fatalf("search DSL did not use data-view time field: %s", dslJSON)
	}

	out, code = runCLIWithEnv(t, env, []string{
		"search", "--data-view", "dv-1", "--time-field", "explicit.time", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := envelopeData(t, out)["timeField"]; got != "explicit.time" {
		t.Fatalf("explicit time field=%v out=%s", got, out)
	}

	out, code = runCLIWithEnv(t, env, []string{
		"agg", "--data-view", "dv-1", "--terms", "level", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("agg exit %d: %s", code, out)
	}
	if got := envelopeData(t, out)["timeField"]; got != "event.time" {
		t.Fatalf("agg data-view time field=%v out=%s", got, out)
	}

	out, code = runCLIWithEnv(t, env, []string{
		"agg", "--data-view", "dv-1", "--agg-type", "date_histogram", "--time-field", "explicit.time", "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("agg explicit exit %d: %s", code, out)
	}
	data = envelopeData(t, out)
	dslJSON, _ = json.Marshal(data["dsl"])
	if data["timeField"] != "explicit.time" || !strings.Contains(string(dslJSON), `"explicit.time"`) || strings.Contains(string(dslJSON), `"event.time"`) {
		t.Fatalf("agg explicit time field not applied: %s", out)
	}
}

func TestDataViewWithoutTimeFieldFailsClosed(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{DataViewNoTimeField: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	env := searchMockEnv(srv.URL)
	for _, args := range [][]string{
		{"search", "--data-view", "dv-1", "--dry-run", "--json"},
		{"agg", "--data-view", "dv-1", "--terms", "level", "--dry-run", "--json"},
	} {
		out, code := runCLIWithEnv(t, env, args)
		if code != ExitBadArgs || !strings.Contains(out, "has no timeFieldName") {
			t.Fatalf("args=%v exit=%d out=%s", args, code, out)
		}
	}

	out, code := runCLIWithEnv(t, env, []string{
		"search", "--data-view", "dv-1", "--time-field", "explicit.time", "--dry-run", "--json",
	})
	if code != ExitOK || envelopeData(t, out)["timeField"] != "explicit.time" {
		t.Fatalf("explicit override exit=%d out=%s", code, out)
	}
}

func TestQueryOutputContextPrecedence(t *testing.T) {
	keyring.MockInit()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	for _, cfg := range []*config.Config{
		{ContextName: "current", Host: "https://current.example.com", Username: "u", Password: "p"},
		{ContextName: "selected", Host: "https://selected.example.com", Username: "u", Password: "p"},
	} {
		if err := config.Save(cfg, config.SaveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	t.Setenv("KIBANA_CLI_CONTEXT", "")

	assertMeta := func(args []string, wantContext, wantSource, wantHost string) {
		t.Helper()
		out, code := runCLI(t, args)
		if code != ExitOK {
			t.Fatalf("exit %d: %s", code, out)
		}
		data := envelopeData(t, out)
		if data["context"] != wantContext || data["contextSource"] != wantSource || data["host"] != wantHost {
			t.Fatalf("context=%v source=%v host=%v", data["context"], data["contextSource"], data["host"])
		}
	}

	assertMeta([]string{"search", "--index", "logs-*", "--dry-run", "--json"},
		"current", "current", "https://current.example.com")

	t.Setenv("KIBANA_CLI_CONTEXT", "selected")
	assertMeta([]string{"search", "--index", "logs-*", "--dry-run", "--json"},
		"selected", "environment", "https://selected.example.com")
	assertMeta([]string{"search", "--index", "logs-*", "--context", "current", "--dry-run", "--json"},
		"current", "flag", "https://current.example.com")

	t.Setenv("KIBANA_CLI_HOST", "https://env.example.com")
	t.Setenv("KIBANA_CLI_USER", "env-user")
	t.Setenv("KIBANA_CLI_PASSWORD", "env-pass")
	assertMeta([]string{"search", "--index", "logs-*", "--context", "current", "--dry-run", "--json"},
		"env", "environment_auth", "https://env.example.com")
}

func TestDirectIndexDryRunDoesNotReadKeyring(t *testing.T) {
	keyring.MockInit()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	if err := config.Save(&config.Config{
		ContextName: "preview",
		Host:        "https://preview.example.com",
		Username:    "u",
		Password:    "p",
	}, config.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	defer keyring.MockInit()
	out, code := runCLI(t, []string{"search", "--index", "logs-*", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["context"] != "preview" || data["host"] != "https://preview.example.com" {
		t.Fatalf("metadata missing: %s", out)
	}
}

func TestSearchTextOutputShowsProvenance(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "context=env") || !strings.Contains(out, "host="+srv.URL) || !strings.Contains(out, "queryLanguage=lucene") {
		t.Fatalf("missing text provenance: %s", out)
	}
}
