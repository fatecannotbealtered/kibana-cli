package fieldmap

import (
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/msgtrace"
	"gopkg.in/yaml.v3"
)

// Alias dictionaries, ordered by preference. Infer keeps only the names that
// actually exist on the index, preserving this order so the first entry is the
// primary field.
var (
	dictService = []string{"log_app", "service_name", "service", "service.name", "app_name", "application", "kubernetes.labels.app"}
	dictLevel   = []string{"level", "log.level", "severity"}
	dictMessage = []string{"message", "msg", "log.message"}
	dictTrace   = []string{"traceId", "trace_id", "trace.id", "log_traceId"}
)

// InferResult is the outcome of probing one index's fields: the field-name groups
// it matched, the detected trace mode, and human-facing confidence notes.
type InferResult struct {
	Index         string   `json:"index"`
	ServiceFields []string `json:"serviceFields,omitempty"`
	LevelFields   []string `json:"levelFields,omitempty"`
	MessageFields []string `json:"messageFields,omitempty"`
	TraceIDFields []string `json:"traceIdFields,omitempty"`
	TraceMode     string   `json:"traceMode"`
	Notes         []string `json:"notes,omitempty"`
}

// Infer maps an index's real field names onto logical groups using the alias
// dictionaries, then decides the trace mode: a present trace field means
// trace_mode=field; otherwise sampleMessages are probed for an embedded traceId
// to detect trace_mode=msg. sampleMessages may be empty (no probe).
func Infer(index string, fieldNames, sampleMessages []string) InferResult {
	present := make(map[string]bool, len(fieldNames))
	for _, n := range fieldNames {
		present[strings.TrimSpace(n)] = true
	}
	r := InferResult{Index: strings.TrimSpace(index)}
	r.ServiceFields = matchDict(dictService, present)
	r.LevelFields = matchDict(dictLevel, present)
	r.MessageFields = matchDict(dictMessage, present)
	r.TraceIDFields = matchDict(dictTrace, present)

	switch {
	case len(r.TraceIDFields) > 0:
		r.TraceMode = TraceModeField
	case traceInSamples(sampleMessages):
		r.TraceMode = TraceModeMsg
		r.Notes = append(r.Notes, "no trace field found; detected a traceId embedded in message bodies → trace_mode=msg")
	default:
		r.TraceMode = TraceModeField
		if len(sampleMessages) > 0 {
			r.Notes = append(r.Notes, "no trace field and no traceId pattern in sampled messages; defaulted trace_mode=field — verify manually")
		} else {
			r.Notes = append(r.Notes, "no trace field found and no messages sampled; defaulted trace_mode=field — verify manually")
		}
	}
	if len(r.ServiceFields) == 0 {
		r.Notes = append(r.Notes, "no known service field matched; set service_fields manually if needed")
	}
	if len(r.MessageFields) == 0 {
		r.Notes = append(r.Notes, "no known message field matched; set message_fields manually if needed")
	}
	return r
}

func matchDict(dict []string, present map[string]bool) []string {
	var out []string
	for _, name := range dict {
		if present[name] {
			out = append(out, name)
		}
	}
	return out
}

func traceInSamples(messages []string) bool {
	for _, m := range messages {
		if _, _, ok := msgtrace.ExtractTrace(m, nil); ok {
			return true
		}
	}
	return false
}

// Profile projects the inference into a field-map Profile bound to the index glob.
func (r InferResult) Profile() Profile {
	p := Profile{
		IndexPatterns: []string{r.Index},
		ServiceFields: r.ServiceFields,
		LevelFields:   r.LevelFields,
		MessageFields: r.MessageFields,
		TraceMode:     r.TraceMode,
	}
	if r.TraceMode == TraceModeField {
		p.TraceIDFields = r.TraceIDFields
	}
	return p
}

// YAMLSnippet renders a paste-ready `profiles:` block for the inferred profile.
func (r InferResult) YAMLSnippet(profileName string) (string, error) {
	doc := map[string]any{
		"profiles": map[string]any{
			profileName: r.Profile(),
		},
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SuggestProfileName derives a stable profile name from an index glob, e.g.
// "sysA-app-*" → "sysA-app".
func SuggestProfileName(index string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(index) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "inferred"
	}
	return name
}
