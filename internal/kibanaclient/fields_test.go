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
