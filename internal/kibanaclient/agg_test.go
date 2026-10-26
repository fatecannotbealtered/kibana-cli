package kibanaclient

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func aggAnswer(buckets []map[string]any, other int) []byte {
	b, _ := json.Marshal(map[string]any{
		"took": 2, "hits": map[string]any{"total": map[string]any{"value": 12, "relation": "eq"}},
		"_shards":      map[string]any{"total": 2, "successful": 2, "skipped": 0, "failed": 0},
		"aggregations": map[string]any{"terms_agg": map[string]any{"sum_other_doc_count": other, "buckets": buckets}},
	})
	return b
}

func TestAggregateTerms(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = w.Write(aggAnswer([]map[string]any{{"key": "ERROR", "doc_count": 3}, {"key": 42, "doc_count": 1}, {"key": true, "doc_count": 1}}, 7))
	})
	res, err := testClient(srv.URL).Aggregate(context.Background(), AggOptions{Index: "logs-*", TermsField: "level", BucketSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.AggType != AggTypeTerms || res.Field != "level" || res.Total != 12 || res.OtherDocs != 7 || len(res.Buckets) != 3 ||
		res.Buckets[1].Key != "42" || res.Buckets[2].Key != "true" || res.Buckets[0].Metric != nil {
		t.Fatalf("%+v", res)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rc.body[0]), &body)
	terms := body["aggs"].(map[string]any)["terms_agg"].(map[string]any)["terms"].(map[string]any)
	if terms["field"] != "level" || terms["size"] != float64(3) || body["size"] != float64(0) || body["track_total_hits"] != true {
		t.Fatalf("body: %s", rc.body[0])
	}
}

// TestAggregateRetriesTextFieldsOnKeyword: a text field Elasticsearch refuses to
// aggregate is asked again as <field>.keyword; the first request is BuildAggBody's.
func TestAggregateRetriesTextFieldsOnKeyword(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"illegal_argument_exception","reason":"Text fields are not optimised for operations that require per-document field data"},"status":400}`))
			return
		}
		_, _ = w.Write(aggAnswer([]map[string]any{{"key": "order-svc", "doc_count": 2, "metric": map[string]any{"value": 7.0}}}, 0))
	})
	opts := AggOptions{Index: "logs-*", TermsField: "service", Metric: "sum", MetricField: "took_ms",
		QueryClause: map[string]any{"term": map[string]any{"msg.keyword": "exact"}}, From: "2026-10-23T04:00:00Z", To: "2026-10-23T05:00:00Z"}
	res, err := testClient(srv.URL).Aggregate(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Field != "service.keyword" || len(rc.reqs) != 2 || res.Buckets[0].Metric == nil || *res.Buckets[0].Metric != 7 {
		t.Fatalf("%+v (%d requests)", res, len(rc.reqs))
	}
	want, _ := json.Marshal(BuildAggBody(opts))
	var a, b map[string]any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal([]byte(rc.body[0]), &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("the first request is BuildAggBody's:\n%s\n%s", want, rc.body[0])
	}
	var retry map[string]any
	_ = json.Unmarshal([]byte(rc.body[1]), &retry)
	if f := retry["aggs"].(map[string]any)["terms_agg"].(map[string]any)["terms"].(map[string]any)["field"]; f != "service.keyword" {
		t.Fatalf("retry field %v", f)
	}

	// A .keyword field is not retried.
	var rc2 recorder
	srv2 := rc2.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"reason":"text fields aggregation"}}`))
	})
	if _, err := testClient(srv2.URL).Aggregate(context.Background(), AggOptions{Index: "x", TermsField: "level.keyword"}); err == nil || len(rc2.reqs) != 1 {
		t.Fatalf("%v, %d requests", err, len(rc2.reqs))
	}
}

func TestAggregateDateHistogramWithMetricAndBounds(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = w.Write(aggAnswer([]map[string]any{
			{"key": 1000, "key_as_string": "2026-10-23T04:00:00.000Z", "doc_count": 6, "metric": map[string]any{"value": 12.5}},
			{"key": 2000, "key_as_string": "2026-10-23T05:00:00.000Z", "doc_count": 0, "metric": map[string]any{"value": nil}},
		}, 0))
	})
	lo, hi := int64(1000), int64(2000)
	res, err := testClient(srv.URL).Aggregate(context.Background(), AggOptions{Index: "logs-*", AggType: "date_histogram",
		Interval: " 1h ", Metric: "AVG", MetricField: "took_ms", BoundsMin: &lo, BoundsMax: &hi})
	if err != nil {
		t.Fatal(err)
	}
	if res.AggType != AggTypeDateHistogram || res.Metric != "avg" || res.Field != "@timestamp" || len(res.Buckets) != 2 ||
		res.Buckets[0].Key != "2026-10-23T04:00:00.000Z" || *res.Buckets[0].Metric != 12.5 || res.Buckets[1].Metric != nil {
		t.Fatalf("%+v", res)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rc.body[0]), &body)
	agg := body["aggs"].(map[string]any)["terms_agg"].(map[string]any)
	dh := agg["date_histogram"].(map[string]any)
	if dh["fixed_interval"] != "1h" || dh["extended_bounds"].(map[string]any)["min"] != float64(1000) {
		t.Fatalf("date_histogram: %v", dh)
	}
	if _, ok := agg["aggs"].(map[string]any)["metric"].(map[string]any)["avg"]; !ok {
		t.Fatalf("metric: %v", agg)
	}
}

func TestAggregateResponseErrors(t *testing.T) {
	if _, err := parseBucketAgg([]byte(`{"hits":{"total":1}}`), "f", ""); err == nil {
		t.Fatal("no terms_agg")
	}
	if _, err := parseBucketAgg([]byte(`{`), "f", ""); err == nil {
		t.Fatal("bad json")
	}
	if isTextFieldAggError(nil) || isTextFieldAggError(&APIError{StatusCode: 500, Message: "fielddata"}) {
		t.Fatal("only a 400 about text fields")
	}
}
