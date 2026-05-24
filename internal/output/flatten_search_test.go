package output

import (
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

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
