package kibanaclient

import "testing"

func TestParseTotalHits_number(t *testing.T) {
	got := parseHitsTotal([]byte(`42`))
	if got.Value != 42 || got.Relation != "eq" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseTotalHits_object_eq(t *testing.T) {
	got := parseHitsTotal([]byte(`{"value":99,"relation":"eq"}`))
	if got.Value != 99 || got.Relation != "eq" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseTotalHits_object_gte(t *testing.T) {
	got := parseHitsTotal([]byte(`{"value":10000,"relation":"gte"}`))
	if got.Value != 10000 || got.Relation != "gte" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseSearchResponse_totalRelation_gte(t *testing.T) {
	data := []byte(`{
		"took": 5,
		"hits": {
			"total": {"value": 10000, "relation": "gte"},
			"hits": []
		}
	}`)
	res, err := parseSearchResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 10000 || res.TotalRelation != "gte" {
		t.Fatalf("total=%d totalRelation=%q", res.Total, res.TotalRelation)
	}
	if res.TookMs != 5 {
		t.Fatalf("tookMs=%d", res.TookMs)
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

func TestParseHitsTotal_emptyAndInvalid(t *testing.T) {
	if parseHitsTotal(nil).Value != 0 {
		t.Fatal("empty")
	}
	for _, raw := range [][]byte{[]byte(`{"x":1}`), []byte(`"nope"`)} {
		if parseHitsTotal(raw).Value != 0 {
			t.Fatalf("invalid %s", raw)
		}
	}
}

func TestParseSearchResponse_hitsAndError(t *testing.T) {
	data := []byte(`{
		"took": 2,
		"hits": {
			"total": {"value": 1, "relation": "eq"},
			"hits": [{
				"_index": "i",
				"_id": "1",
				"_source": {"@timestamp": "t", "k": true}
			}]
		}
	}`)
	res, err := parseSearchResponse(data)
	if err != nil || len(res.Hits) != 1 || res.Hits[0].Timestamp != "t" {
		t.Fatalf("%+v err=%v", res, err)
	}
	if _, err := parseSearchResponse([]byte(`{`)); err == nil {
		t.Fatal("bad json")
	}
}

func TestFmtKey(t *testing.T) {
	cases := []struct {
		in  any
		out string
	}{
		{"s", "s"},
		{float64(1.5), "1.5"},
		{int(7), "7"},
		{int64(9), "9"},
		{true, "true"},
	}
	for _, tc := range cases {
		if got := fmtKey(tc.in); got != tc.out {
			t.Fatalf("%#v => %q want %q", tc.in, got, tc.out)
		}
	}
}

func TestParseTermsAgg_missingTermsAggBody(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"took":1,"hits":{"total":0},"aggregations":{}}`),
		[]byte(`not json`),
	} {
		if _, err := parseTermsAgg(data, "f"); err == nil {
			t.Fatalf("expected error for %s", data)
		}
	}
}

func TestParseTermsAgg_keyTypes(t *testing.T) {
	data := []byte(`{"took":1,"hits":{"total":1},"aggregations":{"terms_agg":{"buckets":[{"key":1,"doc_count":2}]}}}`)
	res, err := parseTermsAgg(data, "n")
	if err != nil || res.Buckets[0].Key != "1" {
		t.Fatalf("%+v err=%v", res, err)
	}
}
