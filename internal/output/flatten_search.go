package output

import (
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/msgtrace"
)

// FlattenSearchHits converts search hits to token-efficient maps for --json output.
func FlattenSearchHits(hits []kibanaclient.SearchHit, fields []string) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		m := map[string]any{
			"_index": h.Index,
			"_id":    h.ID,
		}
		if h.Timestamp != "" {
			m["@timestamp"] = h.Timestamp
		}
		for k, v := range h.Source {
			m[k] = v
		}
		enrichTraceFromMsg(m)
		if len(fields) > 0 {
			m = filterSearchHit(m, fields)
		}
		out = append(out, m)
	}
	return out
}

func filterSearchHit(m map[string]any, fields []string) map[string]any {
	out := FilterMap(m, fields)
	if _, ok := out["traceId"]; ok {
		return out
	}
	if tid, ok := m["traceId"].(string); ok && tid != "" {
		out["traceId"] = tid
		if sid, ok := m["spanId"].(string); ok {
			out["spanId"] = sid
		}
	}
	return out
}

func enrichTraceFromMsg(m map[string]any) {
	if _, has := m["traceId"]; has {
		return
	}
	msg, _ := m["msg"].(string)
	if msg == "" {
		msg, _ = m["message"].(string)
	}
	if msg == "" {
		return
	}
	if tid, sid, ok := msgtrace.ParseBracketIDs(msg); ok {
		m["traceId"] = tid
		m["spanId"] = sid
	}
}
