package kibanaclient

import "testing"

func TestParseHitsTotal(t *testing.T) {
	cases := map[string]hitsTotal{
		`42`:                               {42, "eq"},
		`{"value":99,"relation":"eq"}`:     {99, "eq"},
		`{"value":10000,"relation":"gte"}`: {10000, "gte"},
		`{"value":3}`:                      {3, "eq"},
		`"nope"`:                           {},
		``:                                 {},
	}
	for raw, want := range cases {
		if got := parseHitsTotal([]byte(raw)); got != want {
			t.Errorf("%s: %+v want %+v", raw, got, want)
		}
	}
}

func TestParseSearchResponse(t *testing.T) {
	data := []byte(`{"took":2,"timed_out":true,
		"_shards":{"total":3,"successful":2,"skipped":0,"failed":1,"failures":[{"reason":{"type":"x","reason":"No mapping found for [@timestamp]"}},{"reason":{"type":"x","reason":"No mapping found for [@timestamp]"}}]},
		"hits":{"total":{"value":10000,"relation":"gte"},"hits":[{"_index":"i","_id":"1","_source":{"@timestamp":"t","k":true}},{"_index":"i","_id":"2"}]}}`)
	res, err := parseSearchResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 10000 || res.TotalRelation != "gte" || res.TookMs != 2 || !res.TimedOut || len(res.Hits) != 2 ||
		res.Hits[0].Timestamp != "t" || res.Hits[1].Source == nil {
		t.Fatalf("%+v", res)
	}
	if res.Shards.Failed != 1 || res.Shards.Total != 3 || len(res.Shards.FailureReasons) != 1 {
		t.Fatalf("shards: %+v", res.Shards)
	}
	if _, err := parseSearchResponse([]byte(`{`)); err == nil {
		t.Fatal("bad json")
	}
	empty, err := parseSearchResponse([]byte(`{"hits":{"total":0,"hits":[]}}`))
	if err != nil || empty.Hits == nil || len(empty.Hits) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
}

func TestFmtKey(t *testing.T) {
	cases := []struct {
		in  any
		out string
	}{
		{"s", "s"}, {float64(1.5), "1.5"}, {float64(7), "7"}, {true, "true"}, {nil, ""}, {[]any{1}, "[1]"},
	}
	for _, tc := range cases {
		if got := fmtKey(tc.in); got != tc.out {
			t.Errorf("%#v => %q want %q", tc.in, got, tc.out)
		}
	}
}
