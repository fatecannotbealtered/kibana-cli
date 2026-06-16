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
	out := FlattenSearchHits(hits, []string{"level", "msg"}, NormalizeSpec{})
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
	out := FlattenSearchHits(hits, nil, NormalizeSpec{})
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
			out := FlattenSearchHits(hits, tt.fields, NormalizeSpec{})
			if len(out) != 1 {
				t.Fatal(len(out))
			}
			tt.check(t, out[0])
		})
	}
}

func TestFlattenSearchHits_untrustedMarkers(t *testing.T) {
	hits := []kibanaclient.SearchHit{{
		Index: "logs-1",
		ID:    "1",
		Source: map[string]any{
			"@timestamp": "2026-06-08T00:00:00Z",
			"msg":        "[aabbccdd00112233445566778899aabb, 00cfa11a2dfa446a920d59a76aa56df1] hello",
			"level":      "INFO",
		},
	}}
	out := FlattenSearchHits(hits, []string{"msg", "traceId"}, NormalizeSpec{})
	if len(out) != 1 {
		t.Fatal("missing hit")
	}
	tags, ok := out[0]["_untrusted"].([]string)
	if !ok || len(tags) == 0 {
		t.Fatalf("missing _untrusted tags: %#v", out[0])
	}
	if _, ok := out[0]["traceId"].(string); !ok {
		t.Fatalf("expected traceId enrichment: %#v", out[0])
	}
}

// TestFlattenSearchHits_CrossIndexNormalization is the core block② guarantee:
// two indices with different field names (msg/log_app vs message/service_name and
// different traceId formats) yield the same canonical _service/_message/traceId.
// TestFlattenSearchHits_IndexProvenancePreserved: _index/_id survive an explicit
// --fields narrowing, so multi-index results keep their provenance.
func TestFlattenSearchHits_IndexProvenancePreserved(t *testing.T) {
	hits := []kibanaclient.SearchHit{
		{Index: "a-2026", ID: "x1", Source: map[string]any{"level": "ERROR", "msg": "boom"}},
	}
	out := FlattenSearchHits(hits, []string{"level"}, NormalizeSpec{})
	if out[0]["_index"] != "a-2026" || out[0]["_id"] != "x1" {
		t.Fatalf("provenance lost under --fields: %v", out[0])
	}
	if out[0]["level"] != "ERROR" {
		t.Fatalf("requested field missing: %v", out[0])
	}
	if _, ok := out[0]["msg"]; ok {
		t.Fatalf("unrequested field leaked: %v", out[0])
	}
}

func TestFlattenSearchHits_CrossIndexNormalization(t *testing.T) {
	spec := NormalizeSpec{
		ServiceFields: []string{"log_app", "service_name"},
		MessageFields: []string{"msg", "message"},
		LevelFields:   []string{"level", "log.level"},
		TraceIDFields: []string{"traceId", "trace_id"},
	}
	hits := []kibanaclient.SearchHit{
		{Index: "a-*", ID: "1", Source: map[string]any{
			"log_app": "order-svc", "msg": "boom", "level": "ERROR",
			"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		}},
		{Index: "b-*", ID: "2", Source: map[string]any{
			"service_name": "pay-svc", "message": "traceId=80f198ee56343ba864fe8b2a57d3eff7 timeout",
			"log.level": "WARN",
		}},
	}
	out := FlattenSearchHits(hits, nil, spec)
	if out[0]["_service"] != "order-svc" || out[1]["_service"] != "pay-svc" {
		t.Fatalf("_service not unified: %v / %v", out[0]["_service"], out[1]["_service"])
	}
	if out[0]["_message"] != "boom" || out[1]["_message"] == nil {
		t.Fatalf("_message not unified: %v / %v", out[0]["_message"], out[1]["_message"])
	}
	if out[0]["_level"] != "ERROR" || out[1]["_level"] != "WARN" {
		t.Fatalf("_level not unified: %v / %v", out[0]["_level"], out[1]["_level"])
	}
	if out[0]["traceId"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("traceId from field: %v", out[0]["traceId"])
	}
	if out[1]["traceId"] != "80f198ee56343ba864fe8b2a57d3eff7" {
		t.Fatalf("traceId from msg: %v", out[1]["traceId"])
	}
}

func TestFlattenSearchHits_InjectedTraceRemainsUntrusted(t *testing.T) {
	hits := []kibanaclient.SearchHit{{
		Index: "logs-1",
		ID:    "1",
		Source: map[string]any{
			"msg": bracketMsg,
		},
	}}
	out := FlattenSearchHits(hits, []string{"msg"}, NormalizeSpec{})
	tags, ok := out[0]["_untrusted"].([]string)
	if !ok {
		t.Fatalf("missing _untrusted tags: %#v", out[0])
	}
	for _, tag := range tags {
		if tag == "traceId" {
			return
		}
	}
	t.Fatalf("injected traceId must remain untrusted: %#v", out[0])
}
