package msgtrace

import "regexp"

// Trace IDs show up in log messages in several shapes across systems. We try an
// ordered set of patterns (first match wins) so the output enrichment can lift a
// structured traceId/spanId regardless of the logging framework. Every pattern is
// anchored or bounded to keep matching linear — no catastrophic backtracking.
var builtinPatterns = []*regexp.Regexp{
	// Spring Cloud Sleuth / Micrometer MDC prefix: [traceId, spanId], 32-hex each.
	regexp.MustCompile(`^\[([0-9a-fA-F]{32}),\s*([0-9a-fA-F]{32})\]`),
	// key=value / key:value, e.g. traceId=abc123, trace_id: 4bf92f..., trace-id="...".
	regexp.MustCompile(`(?i)\btrace[_-]?id["']?\s*[:=]\s*["']?([0-9a-fA-F][0-9a-fA-F-]{7,63})`),
	// Bare 32-hex token anywhere (low-priority fallback for ad-hoc formats).
	regexp.MustCompile(`\b([0-9a-fA-F]{32})\b`),
}

// ExtractTrace pulls a traceId (and spanId when the format carries one) out of a
// log message. `extra` patterns from field-map are tried before the built-ins so
// users can teach it a custom shape. A pattern's first capture group is the
// traceId; a second group, when present, is the spanId. Returns ok=false when no
// pattern matches.
func ExtractTrace(msg string, extra []*regexp.Regexp) (traceID, spanID string, ok bool) {
	if msg == "" {
		return "", "", false
	}
	for _, re := range extra {
		if re == nil {
			continue
		}
		if tid, sid, matched := applyPattern(re, msg); matched {
			return tid, sid, true
		}
	}
	for _, re := range builtinPatterns {
		if tid, sid, matched := applyPattern(re, msg); matched {
			return tid, sid, true
		}
	}
	return "", "", false
}

func applyPattern(re *regexp.Regexp, msg string) (traceID, spanID string, ok bool) {
	m := re.FindStringSubmatch(msg)
	if len(m) < 2 || m[1] == "" {
		return "", "", false
	}
	if len(m) >= 3 {
		return m[1], m[2], true
	}
	return m[1], "", true
}

// ParseBracketIDs extracts the MDC bracket-prefix traceId/spanId. Retained as a
// thin compatibility wrapper over ExtractTrace's first built-in pattern so older
// call sites keep their exact (bracket-only) semantics.
func ParseBracketIDs(msg string) (traceID, spanID string, ok bool) {
	return applyPattern(builtinPatterns[0], msg)
}

// CompilePatterns compiles user-supplied trace patterns from field-map, skipping
// any that fail to compile (reported via the returned bad list so callers can
// warn). It never returns an error — a bad custom pattern must not break a query.
func CompilePatterns(raw []string) (compiled []*regexp.Regexp, bad []string) {
	for _, p := range raw {
		if p == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			bad = append(bad, p)
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled, bad
}
