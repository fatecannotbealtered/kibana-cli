package kibanaclient

import "testing"

func TestParseFieldsForWildcard_array(t *testing.T) {
	data := []byte(`{"fields":[{"name":"msg","type":"string","searchable":true,"aggregatable":false},{"name":"@timestamp","type":"date","searchable":true,"aggregatable":true}]}`)
	list, err := parseFieldsForWildcard(data)
	if err != nil || len(list) != 2 {
		t.Fatalf("err=%v len=%d", err, len(list))
	}
	if list[0].Name != "@timestamp" {
		t.Fatalf("sort: first=%s", list[0].Name)
	}
}

func TestParseFieldsForWildcard_map(t *testing.T) {
	data := []byte(`{"fields":{"level":{"name":"level","type":"string","searchable":true,"aggregatable":true}}}`)
	list, err := parseFieldsForWildcard(data)
	if err != nil || len(list) != 1 || list[0].Name != "level" {
		t.Fatalf("got %+v err=%v", list, err)
	}
}

func TestParseFieldsForWildcard_mapSort(t *testing.T) {
	data := []byte(`{"fields":{"b":{"type":"string"},"a":{"type":"string"}}}`)
	list, err := parseFieldsForWildcard(data)
	if err != nil || len(list) != 2 || list[0].Name != "a" {
		t.Fatalf("got %+v err=%v", list, err)
	}
}

func TestParseFieldsForWildcard_errors(t *testing.T) {
	if _, err := parseFieldsForWildcard([]byte(`{`)); err == nil {
		t.Fatal("invalid json")
	}
	if list, err := parseFieldsForWildcard([]byte(`{"fields":[]}`)); err != nil || len(list) != 0 {
		t.Fatalf("empty fields: list=%v err=%v", list, err)
	}
	if list, err := parseFieldsForWildcard([]byte(`{}`)); err != nil || list != nil {
		t.Fatalf("missing fields: list=%v err=%v", list, err)
	}
	data := []byte(`{"fields":{"x":{"type":"string","searchable":true}}}`)
	list, err := parseFieldsForWildcard(data)
	if err != nil || list[0].Name != "x" {
		t.Fatalf("map name fill: %+v err=%v", list, err)
	}
	if _, err := parseFieldsForWildcard([]byte(`{"fields":"bad"}`)); err == nil {
		t.Fatal("unparseable fields")
	}
	if _, err := parseFieldsForWildcard([]byte(`{"fields":{`)); err == nil {
		t.Fatal("truncated fields map")
	}
	if _, err := parseFieldsForWildcard([]byte(`{"fields":[1,2]}`)); err == nil {
		t.Fatal("invalid fields array element")
	}
}
