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
