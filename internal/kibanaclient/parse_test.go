package kibanaclient

import "testing"

func TestParseTotalHits_number(t *testing.T) {
	n := parseTotalHits([]byte(`42`))
	if n != 42 {
		t.Fatalf("got %d", n)
	}
}

func TestParseTotalHits_object(t *testing.T) {
	n := parseTotalHits([]byte(`{"value":99,"relation":"eq"}`))
	if n != 99 {
		t.Fatalf("got %d", n)
	}
}

func TestParseTermsAgg_missing(t *testing.T) {
	_, err := parseTermsAgg([]byte(`{"took":1,"hits":{"total":0}}`), "level")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseTermsAgg_emptyBuckets(t *testing.T) {
	data := []byte(`{"took":1,"hits":{"total":0},"aggregations":{"terms_agg":{"buckets":[]}}}`)
	res, err := parseTermsAgg(data, "level")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 0 {
		t.Fatalf("buckets=%v", res.Buckets)
	}
}
