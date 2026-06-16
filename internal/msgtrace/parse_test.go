package msgtrace

import "testing"

func TestParseBracketIDs(t *testing.T) {
	msg := "[aabbccdd00112233445566778899aabb, 00cfa11a2dfa446a920d59a76aa56df1] [worker-1]: example"
	tid, sid, ok := ParseBracketIDs(msg)
	if !ok {
		t.Fatal("expected ok")
	}
	if tid != "aabbccdd00112233445566778899aabb" || sid != "00cfa11a2dfa446a920d59a76aa56df1" {
		t.Fatalf("got trace=%q span=%q", tid, sid)
	}
	if _, _, ok := ParseBracketIDs("no bracket here"); ok {
		t.Fatal("expected false for plain text")
	}
}

func TestExtractTrace_Formats(t *testing.T) {
	cases := []struct {
		name    string
		msg     string
		wantTID string
		wantSID string
	}{
		{"mdc bracket", "[aabbccdd00112233445566778899aabb, 00cfa11a2dfa446a920d59a76aa56df1] hi", "aabbccdd00112233445566778899aabb", "00cfa11a2dfa446a920d59a76aa56df1"},
		{"key equals", "level=ERROR traceId=4bf92f3577b34da6a3ce929d0e0e4736 op=x", "4bf92f3577b34da6a3ce929d0e0e4736", ""},
		{"key colon snake", "msg trace_id: 80f198ee56343ba864fe8b2a57d3eff7 done", "80f198ee56343ba864fe8b2a57d3eff7", ""},
		{"hyphen quoted", `trace-id="abc123def456" rest`, "abc123def456", ""},
		{"bare 32hex", "request 1234567890abcdef1234567890abcdef finished", "1234567890abcdef1234567890abcdef", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tid, sid, ok := ExtractTrace(tc.msg, nil)
			if !ok {
				t.Fatalf("expected match for %q", tc.msg)
			}
			if tid != tc.wantTID || sid != tc.wantSID {
				t.Fatalf("got trace=%q span=%q want trace=%q span=%q", tid, sid, tc.wantTID, tc.wantSID)
			}
		})
	}
	if _, _, ok := ExtractTrace("no ids here at all", nil); ok {
		t.Fatal("expected no match")
	}
	if _, _, ok := ExtractTrace("", nil); ok {
		t.Fatal("expected no match for empty")
	}
}

func TestExtractTrace_CustomPatternWins(t *testing.T) {
	// A custom pattern is tried before built-ins and can capture a non-hex id.
	extra, bad := CompilePatterns([]string{`reqId=([A-Z0-9]{6,})`})
	if len(bad) != 0 {
		t.Fatalf("unexpected bad patterns: %v", bad)
	}
	tid, _, ok := ExtractTrace("reqId=ABC123XYZ level=INFO", extra)
	if !ok || tid != "ABC123XYZ" {
		t.Fatalf("custom pattern failed: ok=%v tid=%q", ok, tid)
	}
}

func TestCompilePatternsSkipsBad(t *testing.T) {
	compiled, bad := CompilePatterns([]string{`valid\d+`, `(unclosed`, ""})
	if len(compiled) != 1 {
		t.Fatalf("expected 1 compiled, got %d", len(compiled))
	}
	if len(bad) != 1 || bad[0] != "(unclosed" {
		t.Fatalf("expected 1 bad pattern, got %v", bad)
	}
}
