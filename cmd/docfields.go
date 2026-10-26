package cmd

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/msgtrace"
)

// Elasticsearch documents and saved-object attributes have keys that are not
// snake_case (@timestamp, log.level, panelsJSON), and CLI-007 allows only snake_case
// JSON keys. So a document's fields are output as a list of {name, value}: nested
// objects become dotted names, arrays of objects become JSON text, and long text
// values are cut in lists (truncated: true on the entry). --format raw gives the
// upstream JSON unchanged.

// docField is one field of a document: its dotted name and its value.
type docField struct {
	Name      string `json:"name"`
	Value     any    `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
}

// flattenDoc lists a document's fields sorted by name. only, when not empty, keeps
// the fields named there and the fields under them (only "kubernetes" keeps
// kubernetes.labels.app). maxValue > 0 cuts longer text values.
func flattenDoc(src map[string]any, only []string, maxValue int) []docField {
	out := []docField{}
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for _, k := range sortedMapKeys(m) {
			name := k
			if prefix != "" {
				name = prefix + "." + k
			}
			switch v := m[k].(type) {
			case map[string]any:
				if len(v) == 0 {
					out = append(out, docField{Name: name, Value: map[string]any{}})
					continue
				}
				walk(name, v)
			case []any:
				if holdsObject(v) {
					b, _ := json.Marshal(v)
					out = append(out, cutField(name, string(b), maxValue))
				} else {
					out = append(out, docField{Name: name, Value: v})
				}
			case string:
				out = append(out, cutField(name, v, maxValue))
			default:
				out = append(out, docField{Name: name, Value: v})
			}
		}
	}
	walk("", src)
	if len(only) == 0 {
		return out
	}
	kept := []docField{}
	for _, f := range out {
		for _, want := range only {
			want = strings.TrimSpace(want)
			if want != "" && (f.Name == want || strings.HasPrefix(f.Name, want+".")) {
				kept = append(kept, f)
				break
			}
		}
	}
	return kept
}

// holdsObject reports whether a JSON array holds an object, at any depth.
func holdsObject(list []any) bool {
	for _, x := range list {
		switch v := x.(type) {
		case map[string]any:
			return true
		case []any:
			if holdsObject(v) {
				return true
			}
		}
	}
	return false
}

func cutField(name, value string, max int) docField {
	if max > 0 {
		if r := []rune(value); len(r) > max {
			return docField{Name: name, Value: string(r[:max]), Truncated: true}
		}
	}
	return docField{Name: name, Value: value}
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// lookupPath finds a field in a document by its dotted name, as a flat key
// ("log.level": ...) or nested ({"log": {"level": ...}}), or a mix of both.
func lookupPath(m map[string]any, path string) (any, bool) {
	if v, ok := m[path]; ok {
		return v, true
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '.' {
			continue
		}
		if child, ok := m[path[:i]].(map[string]any); ok {
			if v, ok := lookupPath(child, path[i+1:]); ok {
				return v, true
			}
		}
	}
	return nil, false
}

// textOf is a scalar field value as text; "" for anything else.
func textOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// firstText is the first of fields with a non-empty scalar value.
func firstText(src map[string]any, fields []string) string {
	for _, f := range fields {
		if v, ok := lookupPath(src, strings.TrimSpace(f)); ok {
			if s := textOf(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// utcTime is an upstream time as output shows it (CLI-007): UTC with Z. Epoch
// milliseconds become a time; text that is not a time stays as it is.
func utcTime(v any) any {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil
		}
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t.UTC().Format(time.RFC3339Nano)
		}
		return x
	case float64:
		return time.UnixMilli(int64(x)).UTC().Format(time.RFC3339Nano)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return time.UnixMilli(n).UTC().Format(time.RFC3339Nano)
		}
	}
	return nil
}

// hitView says how a search hit is shown: the field map's names for the logical
// fields, the trace patterns, how long a message may be, and which source fields go
// in the hit.
type hitView struct {
	resolved     fieldmap.ResolvedSearch
	patterns     []*regexp.Regexp
	maxMessage   int  // 0: the whole message
	maxValue     int  // 0: whole source values
	withSource   bool // the hit carries source
	sourceFields []string
}

func newHitView(r fieldmap.ResolvedSearch) hitView {
	v := hitView{resolved: r}
	v.patterns, _ = msgtrace.CompilePatterns(r.TraceMsgRegexes)
	return v
}

// logHit is one search hit as output shows it: ref (<index>/<_id>), the time, the
// field map's logical fields (service, level, message, trace and span id) and,
// when asked for, its source fields.
func (v hitView) logHit(h kibanaclient.SearchHit) (map[string]any, bool) {
	r := v.resolved
	out := map[string]any{"ref": h.Index + "/" + h.ID, "index": h.Index, "id": h.ID}
	ts, ok := lookupPath(h.Source, r.TimeField)
	if !ok && h.Timestamp != "" {
		ts = h.Timestamp
	}
	out["timestamp"] = utcTime(ts)
	if s := firstText(h.Source, r.ServiceFields); s != "" {
		out["service"] = s
	}
	if s := firstText(h.Source, r.LevelFields); s != "" {
		out["level"] = s
	}
	msg := firstText(h.Source, r.MessageFields)
	if msg == "" {
		msg = firstText(h.Source, defaultMessageFields)
	}
	traceID, spanID := firstText(h.Source, r.TraceIDFields), ""
	if traceID == "" {
		traceID = firstText(h.Source, []string{"traceId", "trace_id", "trace.id"})
	}
	if traceID == "" && msg != "" {
		if t, s, ok := msgtrace.ExtractTrace(msg, v.patterns); ok {
			traceID, spanID = t, s
		}
	}
	if spanID == "" {
		spanID = firstText(h.Source, []string{"spanId", "span_id", "span.id"})
	}
	if traceID != "" {
		out["trace_id"] = traceID
	}
	if spanID != "" {
		out["span_id"] = spanID
	}
	truncated := false
	if rs := []rune(msg); v.maxMessage > 0 && len(rs) > v.maxMessage {
		msg, truncated = string(rs[:v.maxMessage]), true
		out["message_truncated"] = true
	}
	out["message"] = msg
	if v.withSource {
		out["source"] = flattenDoc(h.Source, v.sourceFields, v.maxValue)
	}
	return out, truncated
}
