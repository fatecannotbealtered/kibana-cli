package fieldmap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"gopkg.in/yaml.v3"
)

// FileName is the local field mapping config file.
const FileName = "field-map.yaml"

// Map is ~/.kibana-cli/field-map.yaml — maps logical names to heterogeneous log index fields.
type Map struct {
	Version    int                `yaml:"version" json:"version"`
	Defaults   Defaults           `yaml:"defaults" json:"defaults"`
	IndexRules []IndexRule        `yaml:"index_rules" json:"indexRules"`
	Profiles   map[string]Profile `yaml:"profiles" json:"profiles"`
	Services   map[string]Service `yaml:"services" json:"services"`
}

// Trace lookup modes (per index / profile).
const (
	TraceModeField = "field" // standard traceId / log_traceId fields on the document
	TraceModeMsg   = "msg"   // traceId embedded in log line prefix [traceId, spanId]
)

// Defaults applies when a profile omits a field list.
type Defaults struct {
	Index           string   `yaml:"index" json:"index"`
	TimeField       string   `yaml:"time_field" json:"timeField"`
	ServiceFields   []string `yaml:"service_fields" json:"serviceFields"`
	LevelFields     []string `yaml:"level_fields" json:"levelFields"`
	MessageFields   []string `yaml:"message_fields" json:"messageFields"`
	TraceIDFields   []string `yaml:"trace_id_fields" json:"traceIdFields"`
	TraceMode       string   `yaml:"trace_mode" json:"traceMode"`
	TraceMsgRegexes []string `yaml:"trace_msg_patterns" json:"traceMsgPatterns,omitempty"`
}

// Profile describes one index pattern and its field names.
type Profile struct {
	Index           string   `yaml:"index" json:"index,omitempty"`
	IndexPatterns   []string `yaml:"index_patterns" json:"indexPatterns,omitempty"`
	TimeField       string   `yaml:"time_field" json:"timeField,omitempty"`
	ServiceFields   []string `yaml:"service_fields" json:"serviceFields,omitempty"`
	LevelFields     []string `yaml:"level_fields" json:"levelFields,omitempty"`
	MessageFields   []string `yaml:"message_fields" json:"messageFields,omitempty"`
	TraceIDFields   []string `yaml:"trace_id_fields" json:"traceIdFields,omitempty"`
	TraceMode       string   `yaml:"trace_mode" json:"traceMode,omitempty"`
	TraceMsgRegexes []string `yaml:"trace_msg_patterns" json:"traceMsgPatterns,omitempty"`
}

// Service maps a logical service name to optional profile scope and extra match fields.
type Service struct {
	Profiles    []string `yaml:"profiles" json:"profiles,omitempty"`
	MatchFields []string `yaml:"match_fields" json:"matchFields,omitempty"`
	Index       string   `yaml:"index" json:"index,omitempty"`
}

// FilePath returns ~/.kibana-cli/field-map.yaml
func FilePath() string {
	return filepath.Join(config.Dir(), FileName)
}

// Load reads the global field-map.yaml; missing file returns nil map without error.
func Load() (*Map, error) {
	return LoadFile(FilePath())
}

// LoadFile reads a field-map from an explicit path (per-context override);
// a missing file returns nil map without error.
func LoadFile(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading field map: %w", err)
	}
	var m Map
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing field map: %w", err)
	}
	if m.Version == 0 {
		m.Version = 1
	}
	return &m, nil
}

// ResolvedSearch bundles index, time field, and field name lists for a query.
type ResolvedSearch struct {
	Index         string
	TimeField     string
	ServiceFields []string
	LevelFields   []string
	MessageFields []string
	TraceIDFields []string
	TraceMode     string
	// TraceMsgRegexes are raw user patterns for extracting a traceId out of the
	// message body (compiled at the output boundary via msgtrace.CompilePatterns).
	TraceMsgRegexes []string
	Profile         string
}

// ResolveSearchOptions merges CLI flags with field-map.yaml.
func ResolveSearchOptions(m *Map, profileName, indexFlag, serviceName, level string) (ResolvedSearch, error) {
	r := ResolvedSearch{TimeField: "@timestamp"}
	if m == nil {
		m = &Map{}
	}
	r.Index = firstNonEmpty(indexFlag, m.Defaults.Index)
	r.TimeField = firstNonEmpty(m.Defaults.TimeField, "@timestamp")
	r.ServiceFields = append([]string{}, m.Defaults.ServiceFields...)
	r.LevelFields = append([]string{}, m.Defaults.LevelFields...)
	r.MessageFields = append([]string{}, m.Defaults.MessageFields...)
	r.TraceIDFields = append([]string{}, m.Defaults.TraceIDFields...)
	r.TraceMode = NormalizeTraceMode(m.Defaults.TraceMode)
	r.TraceMsgRegexes = append([]string{}, m.Defaults.TraceMsgRegexes...)

	if profileName != "" {
		p, ok := m.Profiles[profileName]
		if !ok {
			return r, fmt.Errorf("unknown profile %q (check field-map.yaml)", profileName)
		}
		applyProfile(&r, profileName, p, indexFlag)
	}

	if serviceName != "" {
		svc, ok := m.Services[serviceName]
		if !ok {
			return r, fmt.Errorf("unknown service %q (add it to field-map.yaml services)", serviceName)
		}
		if svc.Index != "" && indexFlag == "" && profileName == "" {
			r.Index = svc.Index
		}
		if len(svc.MatchFields) > 0 {
			r.ServiceFields = uniqueStrings(svc.MatchFields)
		}
		if len(svc.Profiles) == 1 && profileName == "" && indexFlag == "" {
			if p, ok := m.Profiles[svc.Profiles[0]]; ok && p.Index != "" {
				r.Index = p.Index
				r.Profile = svc.Profiles[0]
			}
		}
	}

	if indexFlag != "" {
		r.Index = indexFlag
	}
	if profileName == "" && indexFlag != "" {
		if rule, ok := m.MatchIndexRule(indexFlag); ok {
			r.Profile = "index_rule:" + rule.Match
			applyIndexRule(&r, rule)
		} else if name, p, ok := m.MatchProfileByIndex(indexFlag); ok {
			applyProfile(&r, name, p, indexFlag)
		}
	}
	if r.Index == "" {
		return r, errors.New("index is required: use --index, --profile, or set defaults.index in field-map.yaml")
	}
	_ = level
	return r, nil
}

func applyProfile(r *ResolvedSearch, name string, p Profile, indexFlag string) {
	r.Profile = name
	if indexFlag == "" && p.Index != "" {
		r.Index = p.Index
	}
	if p.TimeField != "" {
		r.TimeField = p.TimeField
	}
	if len(p.ServiceFields) > 0 {
		r.ServiceFields = p.ServiceFields
	}
	if len(p.LevelFields) > 0 {
		r.LevelFields = p.LevelFields
	}
	if len(p.MessageFields) > 0 {
		r.MessageFields = p.MessageFields
	}
	if len(p.TraceIDFields) > 0 {
		r.TraceIDFields = p.TraceIDFields
	}
	if p.TraceMode != "" {
		r.TraceMode = NormalizeTraceMode(p.TraceMode)
	}
	if len(p.TraceMsgRegexes) > 0 {
		r.TraceMsgRegexes = append([]string{}, p.TraceMsgRegexes...)
	}
}

// MatchProfileByIndex picks the profile whose index pattern best matches the CLI --index value.
func (m *Map) MatchProfileByIndex(index string) (name string, p Profile, ok bool) {
	if m == nil || len(m.Profiles) == 0 {
		return "", Profile{}, false
	}
	bestScore := -1
	names := make([]string, 0, len(m.Profiles))
	for n := range m.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		prof := m.Profiles[n]
		for _, pattern := range ProfileIndexPatterns(prof) {
			if !IndexMatchesPattern(index, pattern) {
				continue
			}
			score := patternSpecificity(pattern)
			if score > bestScore {
				bestScore = score
				name, p, ok = n, prof, true
			}
		}
	}
	return name, p, ok
}

// NormalizeTraceMode returns field or msg; empty defaults to field (standard ELK).
func NormalizeTraceMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case TraceModeMsg:
		return TraceModeMsg
	default:
		return TraceModeField
	}
}

// PrimaryMessageField returns the first configured message field or msg.
func (r ResolvedSearch) PrimaryMessageField() string {
	if len(r.MessageFields) > 0 && strings.TrimSpace(r.MessageFields[0]) != "" {
		return strings.TrimSpace(r.MessageFields[0])
	}
	return "msg"
}

// BuildValueORQuery builds a query_string OR across fields for one value (service, level, traceId).
func BuildValueORQuery(fields []string, value string) map[string]any {
	fields = uniqueStrings(fields)
	if len(fields) == 0 || strings.TrimSpace(value) == "" {
		return nil
	}
	parts := make([]string, 0, len(fields)*2)
	quoted := quoteQueryValue(value)
	for _, f := range fields {
		f = strings.TrimSpace(f)
		parts = append(parts, fmt.Sprintf("%s:%s", quoteField(f), quoted))
		if !strings.HasSuffix(f, ".keyword") {
			parts = append(parts, fmt.Sprintf("%s.keyword:%s", quoteField(f), quoted))
		}
	}
	return map[string]any{
		"query_string": map[string]any{
			"query":            strings.Join(parts, " OR "),
			"lenient":          true,
			"analyze_wildcard": true,
		},
	}
}

func quoteField(f string) string {
	if strings.ContainsAny(f, ".-/@") {
		return f
	}
	return f
}

func quoteQueryValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// UniqueStrings deduplicates trimmed non-empty strings preserving order.
func UniqueStrings(in []string) []string {
	return uniqueStrings(in)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ExampleYAML is the template written by `kibana-cli config init`.
const ExampleYAML = `# kibana-cli field map — logical names vs heterogeneous log index fields.
# Copy to: ~/.kibana-cli/field-map.yaml
version: 1

defaults:
  index: "logs-*"
  time_field: "@timestamp"
  service_fields:
    - log_app
    - service_name
    - service
    - service.name
    - app_name
    - kubernetes.labels.app
  level_fields:
    - level
    - log.level
    - severity
  message_fields:
    - message
    - msg
    - log.message
  trace_id_fields:
    - traceId
    - trace_id
    - trace.id
  trace_mode: field

# Ops: add/adjust rules when index naming changes (first match wins).
index_rules:
  - match: "*v3*log*"
    trace_mode: msg
    message_fields: [msg]
  - match: "*legacy*log*"
    trace_mode: field
    trace_id_fields: [log_traceId, traceId]
    message_fields: [msg]

profiles:
  trace-in-msg:
    index_patterns: ["*v3*log*", "*mdc*"]
    message_fields: [msg]
    trace_mode: msg
    # Optional custom traceId-in-message regexes (first group = traceId, second =
    # spanId). Built-ins cover MDC [trace, span], traceId=..., and bare 32-hex.
    # trace_msg_patterns: ['reqId=([0-9a-f]{16,32})']

  trace-in-field:
    index_patterns: ["*legacy*log*", "*-elk-*"]
    message_fields: [msg]
    trace_id_fields: [log_traceId, traceId, trace_id]
    trace_mode: field

  java-app:
    index_patterns: ["java-app-*"]
    service_fields: [log_app, application]
    level_fields: [level]
    message_fields: [msg]

services:
  order-svc:
    match_fields: [log_app, service_name]
    profiles: [java-app]
`
