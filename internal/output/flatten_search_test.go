package output

import (
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

const bracketMsg = "[aabbccdd00112233445566778899aabb, 00cfa11a2dfa446a920d59a76aa56df1] worker log"

func TestFlattenSearchHits_projection(t *testing.T) {
	hits := []kibanaclient.SearchHit{
		{
			Index:     "logs-1",
			ID:        "a",
			Timestamp: "2024-01-01T00:00:00Z",
			Source:    map[string]any{"level": "ERROR", "msg": "boom", "extra": "x"},
		},
	}
	out := FlattenSearchHits(hits, []string{"level", "msg"})
	if len(out) != 1 {
		t.Fatal(len(out))
	}
	if out[0]["level"] != "ERROR" || out[0]["msg"] != "boom" {
		t.Fatalf("%v", out[0])
	}
	if _, ok := out[0]["extra"]; ok {
		t.Fatal("extra should be filtered")
	}
}

func TestFlattenSearchHits_noTimestampNoFields(t *testing.T) {
	hits := []kibanaclient.SearchHit{
		{Index: "i", ID: "id", Source: map[string]any{"k": "v"}},
	}
	out := FlattenSearchHits(hits, nil)
	if len(out) != 1 {
		t.Fatal(len(out))
	}
	if _, ok := out[0]["@timestamp"]; ok {
		t.Fatal("empty timestamp should be omitted")
	}
	if out[0]["_index"] != "i" || out[0]["_id"] != "id" || out[0]["k"] != "v" {
		t.Fatalf("%v", out[0])
	}
}

func TestFlattenSearchHits_traceEnrichment(t *testing.T) {
	tests := []struct {
		name   string
		source map[string]any
		fields []string
		check  func(t *testing.T, m map[string]any)
	}{
		{
			name:   "from msg bracket",
			source: map[string]any{"msg": bracketMsg},
			check: func(t *testing.T, m map[string]any) {
				if m["traceId"] != "aabbccdd00112233445566778899aabb" {
					t.Fatalf("traceId: %v", m)
				}
				if m["spanId"] != "00cfa11a2dfa446a920d59a76aa56df1" {
					t.Fatalf("spanId: %v", m)
				}
			},
		},
		{
			name:   "from message field",
			source: map[string]any{"message": bracketMsg},
			check: func(t *testing.T, m map[string]any) {
				if m["traceId"] == nil {
					t.Fatal("expected trace from message")
				}
			},
		},
		{
			name:   "existing traceId skips enrich",
			source: map[string]any{"traceId": "existing", "msg": bracketMsg},
			check: func(t *testing.T, m map[string]any) {
				if m["traceId"] != "existing" {
					t.Fatalf("got %v", m["traceId"])
				}
			},
		},
		{
			name:   "empty msg skips",
			source: map[string]any{"msg": ""},
			check: func(t *testing.T, m map[string]any) {
				if _, ok := m["traceId"]; ok {
					t.Fatal("unexpected traceId")
				}
			},
		},
		{
			name:   "projection keeps traceId from source",
			source: map[string]any{"traceId": "tid", "spanId": "sid", "level": "INFO"},
			fields: []string{"level"},
			check: func(t *testing.T, m map[string]any) {
				if m["traceId"] != "tid" || m["spanId"] != "sid" {
					t.Fatalf("%v", m)
				}
			},
		},
		{
			name:   "projection injects traceId when filtered out",
			source: map[string]any{"traceId": "tid", "spanId": "sid", "level": "INFO"},
			fields: []string{"level"},
			check: func(t *testing.T, m map[string]any) {
				if m["traceId"] != "tid" {
					t.Fatalf("%v", m)
				}
			},
		},
		{
			name:   "traceId already in projected output",
			source: map[string]any{"traceId": "tid", "level": "INFO"},
			fields: []string{"traceId", "level"},
			check: func(t *testing.T, m map[string]any) {
				if m["traceId"] != "tid" {
					t.Fatalf("%v", m)
				}
			},
		},
		{
			name:   "empty traceId string not injected",
			source: map[string]any{"traceId": "", "level": "INFO"},
			fields: []string{"level"},
			check: func(t *testing.T, m map[string]any) {
				if _, ok := m["traceId"]; ok {
					t.Fatalf("should not inject empty traceId: %v", m)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := []kibanaclient.SearchHit{{
				Index:  "idx",
				ID:     "1",
				Source: tt.source,
			}}
			out := FlattenSearchHits(hits, tt.fields)
			if len(out) != 1 {
				t.Fatal(len(out))
			}
			tt.check(t, out[0])
		})
	}
}
