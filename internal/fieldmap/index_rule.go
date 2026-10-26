package fieldmap

// IndexRule applies field/trace settings when --index matches a glob (first match wins).
type IndexRule struct {
	Match           string   `yaml:"match,omitempty" json:"match,omitempty"`
	TimeField       string   `yaml:"time_field,omitempty" json:"time_field,omitempty"`
	ServiceFields   []string `yaml:"service_fields,omitempty" json:"service_fields,omitempty"`
	LevelFields     []string `yaml:"level_fields,omitempty" json:"level_fields,omitempty"`
	MessageFields   []string `yaml:"message_fields,omitempty" json:"message_fields,omitempty"`
	TraceIDFields   []string `yaml:"trace_id_fields,omitempty" json:"trace_id_fields,omitempty"`
	TraceMode       string   `yaml:"trace_mode,omitempty" json:"trace_mode,omitempty"`
	TraceMsgRegexes []string `yaml:"trace_msg_patterns,omitempty" json:"trace_msg_patterns,omitempty"`
}

// MatchIndexRule returns the first index_rules entry that matches index.
func (m *Map) MatchIndexRule(index string) (IndexRule, bool) {
	if m == nil {
		return IndexRule{}, false
	}
	for _, rule := range m.IndexRules {
		if IndexMatchesPattern(index, rule.Match) {
			return rule, true
		}
	}
	return IndexRule{}, false
}

func applyIndexRule(r *ResolvedSearch, rule IndexRule) {
	if rule.TimeField != "" {
		r.TimeField = rule.TimeField
	}
	if len(rule.ServiceFields) > 0 {
		r.ServiceFields = append([]string{}, rule.ServiceFields...)
	}
	if len(rule.LevelFields) > 0 {
		r.LevelFields = append([]string{}, rule.LevelFields...)
	}
	if len(rule.MessageFields) > 0 {
		r.MessageFields = append([]string{}, rule.MessageFields...)
	}
	if len(rule.TraceIDFields) > 0 {
		r.TraceIDFields = append([]string{}, rule.TraceIDFields...)
	}
	if rule.TraceMode != "" {
		r.TraceMode = NormalizeTraceMode(rule.TraceMode)
	}
	if len(rule.TraceMsgRegexes) > 0 {
		r.TraceMsgRegexes = append([]string{}, rule.TraceMsgRegexes...)
	}
}
