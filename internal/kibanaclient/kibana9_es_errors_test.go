package kibanaclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Kibana 9.5.2's Console Proxy answers 200 when Elasticsearch refused the request
// and carries Elasticsearch's error and status in the body, pretty-printed (live
// check, 2026-10). These are the two answers it gave.
const (
	kibana9TextFieldAgg = `{
  "error" : {
    "root_cause" : [
      {
        "type" : "illegal_argument_exception",
        "reason" : "Fielddata is disabled on [level] in [svc-log-2026.10.25]. Text fields are not optimised for operations that require per-document field data like aggregations and sorting, so these operations are disabled by default. Please use a keyword field instead."
      }
    ],
    "type" : "search_phase_execution_exception",
    "reason" : "all shards failed",
    "phase" : "query",
    "grouped" : true
  },
  "status" : 400
}`
	kibana9NoSuchIndex = `{"error":{"root_cause":[{"type":"index_not_found_exception","reason":"no such index [iot-no-such-index]"}],"type":"index_not_found_exception","reason":"no such index [iot-no-such-index]"},"status":404}`
)

// TestProxyReadsTheStatusKibana9PutsInTheBody: a 200 that carries an Elasticsearch
// error is that error, with its status and the root cause as the message.
func TestProxyReadsTheStatusKibana9PutsInTheBody(t *testing.T) {
	for _, tc := range []struct {
		body    string
		status  int
		message string
	}{
		{kibana9TextFieldAgg, 400, "Fielddata is disabled on [level]"},
		{kibana9NoSuchIndex, 404, "no such index [iot-no-such-index]"},
	} {
		var rc recorder
		srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = w.Write([]byte(tc.body)) })
		_, err := testClient(srv.URL).Proxy(context.Background(), http.MethodPost, "x/_search", map[string]any{"size": 0})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status || !strings.HasPrefix(apiErr.Message, tc.message) {
			t.Fatalf("%d: got %v", tc.status, err)
		}
	}
}

// TestAggregateRetriesKeywordOnKibana9: the text-field refusal arrives as a 200 on
// Kibana 9, and the terms aggregation is still asked again on <field>.keyword.
func TestAggregateRetriesKeywordOnKibana9(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			_, _ = w.Write([]byte(kibana9TextFieldAgg))
			return
		}
		_, _ = w.Write(aggAnswer([]map[string]any{{"key": "ERROR", "doc_count": 4}}, 0))
	})
	res, err := testClient(srv.URL).Aggregate(context.Background(), AggOptions{Index: "svc-log-*", TermsField: "level"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Field != "level.keyword" || len(rc.reqs) != 2 || res.Buckets[0].Key != "ERROR" {
		t.Fatalf("%+v (%d requests)", res, len(rc.reqs))
	}
}

// TestProxyKeepsAnswersThatMentionError: log content containing "error" is not an
// Elasticsearch error.
func TestProxyKeepsAnswersThatMentionError(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"hits": map[string]any{"total": map[string]any{"value": 1},
		"hits": []map[string]any{{"_index": "a", "_id": "1", "_source": map[string]any{"error": map[string]any{"code": 7}, "msg": `"error"`}}}}})
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = w.Write(body) })
	if _, err := testClient(srv.URL).Proxy(context.Background(), http.MethodPost, "a/_search", map[string]any{}); err != nil {
		t.Fatal(err)
	}
}
