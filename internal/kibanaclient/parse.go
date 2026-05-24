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
}

// SearchResult is the CLI search envelope.
type SearchResult struct {
	TookMs int         `json:"tookMs"`
	Total  int64       `json:"total"`
	Hits   []SearchHit `json:"hits"`
}

func parseTotalHits(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var obj struct {
		Value int64 `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Value
	}
	return 0
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
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := &SearchResult{TookMs: raw.Took, Total: parseTotalHits(raw.Hits.Total)}
	for _, h := range raw.Hits.Hits {
		hit := SearchHit{Index: h.Index, ID: h.ID, Source: h.Source}
		if ts, ok := h.Source["@timestamp"].(string); ok {
			hit.Timestamp = ts
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

// AggBucket is one terms bucket.
type AggBucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// AggResult is the CLI aggregation envelope.
type AggResult struct {
	TookMs  int         `json:"tookMs"`
	Field   string      `json:"field"`
	Total   int64       `json:"total"`
	Buckets []AggBucket `json:"buckets"`
}

func parseTermsAgg(data []byte, field string) (*AggResult, error) {
	var raw struct {
		Took int `json:"took"`
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
