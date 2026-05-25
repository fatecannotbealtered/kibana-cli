package output

import "testing"

func TestFilterMap(t *testing.T) {
	m := map[string]any{"@timestamp": "t1", "level": "ERROR", "msg": "x"}
	got := FilterMap(m, []string{"level", "LEVEL"})
	if len(got) != 1 || got["level"] != "ERROR" {
		t.Fatalf("got %v", got)
	}
}

func TestFilterMap_emptyFields(t *testing.T) {
	m := map[string]any{"a": 1}
	got := FilterMap(m, nil)
	if len(got) != 1 || got["a"] != 1 {
		t.Fatalf("got %v", got)
	}
	got = FilterMap(m, []string{})
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestFilterMap_trimAndMissing(t *testing.T) {
	m := map[string]any{"Foo": "bar"}
	got := FilterMap(m, []string{"  foo  ", "missing"})
	if len(got) != 1 || got["Foo"] != "bar" {
		t.Fatalf("got %v", got)
	}
}
