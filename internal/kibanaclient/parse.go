package kibanaclient

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// SearchHit is one log document.
type SearchHit struct {
	Index     string         `json:"index"`
	ID        string         `json:"id"`
	Timestamp string         `json:"@timestamp,omitempty"`
	Source    map[string]any `json:"source"`
	// Sort holds the ES sort values of this hit, used to build a search_after cursor.
	Sort []any `json:"-"`
}

// SearchResult is the CLI search envelope.
type SearchResult struct {
	TookMs        int         `json:"tookMs"`
	Total         int64       `json:"total"`
	TotalRelation string      `json:"totalRelation,omitempty"`
	Hits          []SearchHit `json:"hits"`
	// LastSort is the sort values of the final hit on this page; it becomes the
	// next_search_after cursor for stable, offset-free pagination.
	LastSort []any `json:"-"`
}

type hitsTotal struct {
	Value    int64
	Relation string
}

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
		return hitsTotal{Value: obj.Value, Relation: obj.Relation}
	}
	return hitsTotal{}
}

func parseTotalHits(raw json.RawMessage) int64 {
	return parseHitsTotal(raw).Value
}

func parseSearchResponse(data []byte) (*SearchResult, error) {
	var raw struct {
		Took int `json:"took"`
		Hits struct {
			Total json.RawMessage `json:"total"`
			Hits  []struct {
				Index  string         `json:"_index"`
				ID     string         `json:"_id"`
				Source map[string]any `json:"_source"`
				Sort   []any          `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	total := parseHitsTotal(raw.Hits.Total)
	out := &SearchResult{TookMs: raw.Took, Total: total.Value, TotalRelation: total.Relation}
	for _, h := range raw.Hits.Hits {
		hit := SearchHit{Index: h.Index, ID: h.ID, Source: h.Source, Sort: h.Sort}
		if ts, ok := h.Source["@timestamp"].(string); ok {
			hit.Timestamp = ts
		}
		out.Hits = append(out.Hits, hit)
	}
	if n := len(out.Hits); n > 0 {
		out.LastSort = out.Hits[n-1].Sort
	}
	return out, nil
}

// AggBucket is one aggregation bucket. Metric is set only when a metric
// sub-aggregation was requested (avg|sum|min|max); HasMetric distinguishes a
// real zero from "no metric".
type AggBucket struct {
	Key       string   `json:"key"`
	Count     int64    `json:"count"`
	Metric    *float64 `json:"metric,omitempty"`
	HasMetric bool     `json:"-"`
}

// AggResult is the CLI aggregation envelope.
type AggResult struct {
	TookMs  int         `json:"tookMs"`
	Field   string      `json:"field"`
	Total   int64       `json:"total"`
	Buckets []AggBucket `json:"buckets"`
	// AggType and Metric echo what was run, for the response envelope.
	AggType string `json:"aggType,omitempty"`
	Metric  string `json:"metric,omitempty"`
}

func parseTermsAgg(data []byte, field string) (*AggResult, error) {
	var raw struct {
		Took         int `json:"took"`
		Aggregations struct {
			TermsAgg struct {
				Buckets []struct {
					Key      any   `json:"key"`
					DocCount int64 `json:"doc_count"`
				} `json:"buckets"`
			} `json:"terms_agg"`
		} `json:"aggregations"`
		Hits struct {
			Total json.RawMessage `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var keyCheck struct {
		Aggregations struct {
			TermsAgg json.RawMessage `json:"terms_agg"`
		} `json:"aggregations"`
	}
	if json.Unmarshal(data, &keyCheck) != nil || len(keyCheck.Aggregations.TermsAgg) == 0 {
		return nil, fmt.Errorf("missing terms_agg in search response")
	}
	out := &AggResult{
		TookMs: raw.Took,
		Field:  field,
		Total:  parseTotalHits(raw.Hits.Total),
	}
	for _, b := range raw.Aggregations.TermsAgg.Buckets {
		out.Buckets = append(out.Buckets, AggBucket{Key: fmtKey(b.Key), Count: b.DocCount})
	}
	return out, nil
}

// parseBucketAgg parses a terms or date_histogram response that may carry a
// "metric" sub-aggregation per bucket. For date_histogram it prefers
// key_as_string (the formatted timestamp) over the raw epoch key.
func parseBucketAgg(data []byte, field, metric string) (*AggResult, error) {
	var raw struct {
		Took         int `json:"took"`
		Aggregations struct {
			TermsAgg struct {
				Buckets []struct {
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
		return nil, err
	}
	var keyCheck struct {
		Aggregations struct {
			TermsAgg json.RawMessage `json:"terms_agg"`
		} `json:"aggregations"`
	}
	if json.Unmarshal(data, &keyCheck) != nil || len(keyCheck.Aggregations.TermsAgg) == 0 {
		return nil, fmt.Errorf("missing terms_agg in search response")
	}
	wantMetric := normalizeMetric(metric)
	out := &AggResult{
		TookMs: raw.Took,
		Field:  field,
		Total:  parseTotalHits(raw.Hits.Total),
		Metric: wantMetric,
	}
	for _, b := range raw.Aggregations.TermsAgg.Buckets {
		key := b.KeyAsString
		if key == "" {
			key = fmtKey(b.Key)
		}
		bucket := AggBucket{Key: key, Count: b.DocCount}
		if wantMetric != "" && wantMetric != "count" && b.Metric != nil {
			bucket.HasMetric = true
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
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		b, _ := json.Marshal(k)
		return string(b)
	}
}
