package kibanaclient

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// SearchHit is one document of a search.
type SearchHit struct {
	Index     string
	ID        string
	Timestamp string // the source's @timestamp, when it has one as a string
	Source    map[string]any
}

// Shards is a search's shard summary. Failed or TimedOut mean the hits and the
// total may be incomplete.
type Shards struct {
	Total, Successful, Skipped, Failed int
	// FailureReasons are the distinct reasons of the failed shards (external text).
	FailureReasons []string
}

// SearchResult is one page of a search.
type SearchResult struct {
	TookMs        int
	Total         int64
	TotalRelation string // eq, or gte when Total is a lower bound
	Hits          []SearchHit
	Shards        Shards
	TimedOut      bool
}

type hitsTotal struct {
	Value    int64
	Relation string
}

// parseHitsTotal reads hits.total, a number (6.x) or {value, relation} (7.x+).
func parseHitsTotal(raw json.RawMessage) hitsTotal {
	if len(raw) == 0 {
		return hitsTotal{}
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return hitsTotal{Value: n, Relation: "eq"}
	}
	var obj struct {
		Value    int64  `json:"value"`
		Relation string `json:"relation"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Relation == "" {
			obj.Relation = "eq"
		}
		return hitsTotal{Value: obj.Value, Relation: obj.Relation}
	}
	return hitsTotal{}
}

type shardsJSON struct {
	Total      int `json:"total"`
	Successful int `json:"successful"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
	Failures   []struct {
		Reason struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"reason"`
	} `json:"failures"`
}

func (s shardsJSON) shards() Shards {
	out := Shards{Total: s.Total, Successful: s.Successful, Skipped: s.Skipped, Failed: s.Failed}
	seen := map[string]bool{}
	for _, f := range s.Failures {
		r := f.Reason.Reason
		if r == "" {
			r = f.Reason.Type
		}
		if r != "" && !seen[r] && len(out.FailureReasons) < 3 {
			seen[r] = true
			out.FailureReasons = append(out.FailureReasons, r)
		}
	}
	return out
}

func parseSearchResponse(data []byte) (*SearchResult, error) {
	var raw struct {
		Took     int        `json:"took"`
		TimedOut bool       `json:"timed_out"`
		Shards   shardsJSON `json:"_shards"`
		Hits     struct {
			Total json.RawMessage `json:"total"`
			Hits  []struct {
				Index  string         `json:"_index"`
				ID     string         `json:"_id"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing the search response: %w", err)
	}
	total := parseHitsTotal(raw.Hits.Total)
	out := &SearchResult{TookMs: raw.Took, Total: total.Value, TotalRelation: total.Relation,
		Shards: raw.Shards.shards(), TimedOut: raw.TimedOut, Hits: []SearchHit{}}
	for _, h := range raw.Hits.Hits {
		hit := SearchHit{Index: h.Index, ID: h.ID, Source: h.Source}
		if hit.Source == nil {
			hit.Source = map[string]any{}
		}
		if ts, ok := hit.Source["@timestamp"].(string); ok {
			hit.Timestamp = ts
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

// AggBucket is one aggregation bucket. Metric is set only when a metric
// sub-aggregation was requested (avg|sum|min|max) and Elasticsearch gave a value.
type AggBucket struct {
	Key    string
	Count  int64
	Metric *float64
}

// AggResult is one aggregation.
type AggResult struct {
	TookMs int
	Field  string // the field the buckets are of (with .keyword after the retry)
	Total  int64  // documents the query matched
	// TotalRelation is eq, or gte when Total is a lower bound.
	TotalRelation string
	Buckets       []AggBucket
	// OtherDocs is sum_other_doc_count of a terms aggregation: documents in buckets
	// beyond the requested size. More than 0 means more distinct values exist.
	OtherDocs int64
	AggType   string
	Metric    string
	Shards    Shards
	TimedOut  bool
}

// parseBucketAgg parses a terms or date_histogram response, with an optional
// "metric" sub-aggregation per bucket. date_histogram keys are key_as_string.
func parseBucketAgg(data []byte, field, metric string) (*AggResult, error) {
	var raw struct {
		Took         int        `json:"took"`
		TimedOut     bool       `json:"timed_out"`
		Shards       shardsJSON `json:"_shards"`
		Aggregations struct {
			TermsAgg *struct {
				SumOtherDocCount int64 `json:"sum_other_doc_count"`
				Buckets          []struct {
					Key         any    `json:"key"`
					KeyAsString string `json:"key_as_string"`
					DocCount    int64  `json:"doc_count"`
					Metric      *struct {
						Value *float64 `json:"value"`
					} `json:"metric"`
				} `json:"buckets"`
			} `json:"terms_agg"`
		} `json:"aggregations"`
		Hits struct {
			Total json.RawMessage `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing the aggregation response: %w", err)
	}
	if raw.Aggregations.TermsAgg == nil {
		return nil, fmt.Errorf("the aggregation response has no terms_agg")
	}
	total := parseHitsTotal(raw.Hits.Total)
	wantMetric := normalizeMetric(metric)
	out := &AggResult{TookMs: raw.Took, Field: field, Total: total.Value, TotalRelation: total.Relation, Metric: wantMetric,
		OtherDocs: raw.Aggregations.TermsAgg.SumOtherDocCount, Shards: raw.Shards.shards(), TimedOut: raw.TimedOut,
		Buckets: []AggBucket{}}
	for _, b := range raw.Aggregations.TermsAgg.Buckets {
		key := b.KeyAsString
		if key == "" {
			key = fmtKey(b.Key)
		}
		bucket := AggBucket{Key: key, Count: b.DocCount}
		if hasMetric(wantMetric) && b.Metric != nil {
			bucket.Metric = b.Metric.Value
		}
		out.Buckets = append(out.Buckets, bucket)
	}
	return out, nil
}

func fmtKey(k any) string {
	switch v := k.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(k)
		return string(b)
	}
}
