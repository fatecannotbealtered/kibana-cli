package cmd

import (
	"strings"
	"testing"
)

func TestSearch_DSL_RawPassthrough(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*",
		"--dsl", `{"query":{"match_all":{}},"size":5}`,
		"--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["hits"] == nil {
		t.Fatalf("missing hits: %s", out)
	}
	tags, _ := data["_untrusted"].([]any)
	if len(tags) == 0 || tags[0] != "hits" {
		t.Fatalf("missing _untrusted hits tag: %s", out)
	}
}

func TestSearch_DSL_InvalidJSON(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--dsl", `{not valid`, "--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestSearch_DSL_DryRun(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--dsl", `{"query":{"match_all":{}}}`, "--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"dry_run"`) {
		t.Fatalf("expected dry-run: %s", out)
	}
}

func TestSearch_SearchAfter_Cursor(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchTotalGte: true})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	// First page: limit 1 with a gte total => has_more true => a cursor is emitted.
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--limit", "1", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	token, _ := data["next_search_after"].(string)
	if token == "" {
		t.Fatalf("expected next_search_after token: %s", out)
	}
	// Follow the cursor.
	out2, code2 := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{
		"search", "--index", "logs-*", "--limit", "1", "--search-after", token, "--json",
	})
	if code2 != ExitOK {
		t.Fatalf("exit %d: %s", code2, out2)
	}
	data2 := envelopeData(t, out2)
	if data2["next_offset"] != nil {
		t.Fatalf("cursor mode must not emit next_offset: %s", out2)
	}
}

func TestSearch_SearchAfter_OffsetConflict(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLI(t, []string{
		"search", "--index", "logs-*", "--search-after", "abc", "--offset", "5", "--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}

func TestSearch_SearchAfter_InvalidToken(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, searchExtraFieldMap)
	_, code := runCLI(t, []string{
		"search", "--index", "logs-*", "--search-after", "!!!notbase64!!!", "--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("got exit %d", code)
	}
}
