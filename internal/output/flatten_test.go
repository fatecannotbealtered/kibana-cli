package output

import "testing"

func TestFilterMap(t *testing.T) {
	m := map[string]any{"@timestamp": "t1", "level": "ERROR", "msg": "x"}
	got := FilterMap(m, []string{"level", "LEVEL"})
	if len(got) != 1 || got["level"] != "ERROR" {
		t.Fatalf("got %v", got)
	}
}
