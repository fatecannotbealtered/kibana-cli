package cmd

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestLogSearchDefaultPageFitsTheBudget: a default page of long messages stops early
// within the 8,000-character budget and continues by next_offset (CLI-042); a
// production page of 20 reached 9,990 characters in the live run. An explicit
// --limit gets every hit it asked for.
func TestLogSearchDefaultPageFitsTheBudget(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 20; i++ {
		e.kb.docs = append(e.kb.docs, fakeDoc{Index: "big-log-2026.10.25", ID: fmt.Sprintf("b%02d", i), Source: map[string]any{
			"@timestamp": fmt.Sprintf("2026-10-25T09:00:%02d.000Z", 59-i), "level": "INFO", "log_app": "order-svc",
			"msg": fmt.Sprintf(`{"event":"request","n":%d,"body":"%s"}`, i, strings.Repeat("z", 480))}})
	}
	r := e.run("log", "search", "--index", "big-log-*")
	n := len(r.items())
	if r.exit != 0 || n == 0 || n >= 20 || utf8.RuneCountInString(strings.TrimSpace(r.out)) > 8000 {
		t.Fatalf("default page: %d items, %d chars: %.300s", n, utf8.RuneCountInString(r.out), r.out)
	}
	if r.data()["has_more"] != true || num(r.data()["next_offset"]) != n || !hasString(r.notices(), "page_shortened") {
		t.Fatalf("continuation: %.600s", r.out)
	}
	if next := e.run("log", "search", "--index", "big-log-*", "--offset", fmt.Sprint(n)); next.exit != 0 ||
		next.items()[0]["ref"] != fmt.Sprintf("big-log-2026.10.25/b%02d", n) {
		t.Fatalf("next page: %.300s", next.out)
	}
	if all := e.run("log", "search", "--index", "big-log-*", "--limit", "20"); len(all.items()) != 20 || hasString(all.notices(), "page_shortened") {
		t.Fatalf("explicit limit: %d items", len(all.items()))
	}
}
