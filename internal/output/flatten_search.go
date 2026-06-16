package output

import (
	"regexp"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/msgtrace"
)

// NormalizeSpec tells FlattenSearchHits how to map each index's heterogeneous
// field names onto a uniform output schema. The lists come from the resolved
// field-map (service/message/level/trace fields); TraceMsgPatterns are compiled
// user patterns for pulling a traceId out of the message body.
type NormalizeSpec struct {
	ServiceFields    []string
	MessageFields    []string
	LevelFields      []string
	TraceIDFields    []string
	TraceMsgPatterns []*regexp.Regexp
}

// Canonical alias keys added to every hit so an agent can read one field name
// regardless of which index a hit came from.
const (
	aliasService = "_service"
	aliasMessage = "_message"
	aliasLevel   = "_level"
)

// FlattenSearchHits converts search hits to token-efficient maps for --json
// output. Beyond copying _source, it adds canonical aliases (_service, _message,
// _level) and a unified traceId/spanId so cross-index result sets share a schema.
// Original fields are preserved (non-destructive).
func FlattenSearchHits(hits []kibanaclient.SearchHit, fields []string, spec NormalizeSpec) []map[string]any {
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
		normalizeHit(m, spec)
		m["_untrusted"] = untrustedFields(m)
		if len(fields) > 0 {
			m = filterSearchHit(m, fields)
		}
		out = append(out, m)
	}
	return out
}

// normalizeHit adds canonical aliases and a unified traceId without removing the
// original heterogeneous fields.
func normalizeHit(m map[string]any, spec NormalizeSpec) {
	if v := firstPresent(m, spec.ServiceFields); v != "" {
		m[aliasService] = v
	}
	msgValue := firstPresent(m, spec.MessageFields)
	if msgValue == "" {
		msgValue = firstPresent(m, []string{"message", "msg", "log.message"})
	}
	if msgValue != "" {
		m[aliasMessage] = msgValue
	}
	if v := firstPresent(m, spec.LevelFields); v != "" {
		m[aliasLevel] = v
	}
	enrichTrace(m, spec, msgValue)
}

// enrichTrace sets traceId (and spanId) from a real field when present, otherwise
// extracts it from the message body via the configured + built-in patterns.
func enrichTrace(m map[string]any, spec NormalizeSpec, msgValue string) {
	if _, has := m["traceId"]; has {
		return
	}
	if v := firstPresent(m, spec.TraceIDFields); v != "" {
		m["traceId"] = v
		return
	}
	if msgValue == "" {
		return
	}
	if tid, sid, ok := msgtrace.ExtractTrace(msgValue, spec.TraceMsgPatterns); ok {
		m["traceId"] = tid
		if sid != "" {
			m["spanId"] = sid
		}
	}
}

func firstPresent(m map[string]any, fields []string) string {
	for _, f := range fields {
		if v, ok := m[f].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func filterSearchHit(m map[string]any, fields []string) map[string]any {
	out := FilterMap(m, fields)
	// _index/_id (provenance — which index a hit came from) and the trace ids
	// always surface even when a caller narrows --fields: all small, and exactly
	// what you need when a query spans multiple indices. The bulkier
	// _service/_message/_level aliases appear via the default projection, so an
	// explicit --fields stays authoritative for those.
	for _, key := range []string{"_index", "_id", "traceId", "spanId"} {
		if _, ok := out[key]; ok {
			continue
		}
		if v, ok := m[key].(string); ok && v != "" {
			out[key] = v
		}
	}
	if tags, ok := m["_untrusted"].([]string); ok && len(tags) > 0 {
		out["_untrusted"] = filterUntrustedTags(out, tags)
	}
	return out
}

func untrustedFields(m map[string]any) []string {
	var fields []string
	for key := range m {
		if key == "_untrusted" || key == "_index" || key == "_id" {
			continue
		}
		fields = append(fields, key)
	}
	return fields
}

func filterUntrustedTags(projected map[string]any, tags []string) []string {
	var out []string
	for _, tag := range tags {
		if _, ok := projected[tag]; ok {
			out = append(out, tag)
		}
	}
	return out
}
