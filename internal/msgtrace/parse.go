package msgtrace

import "regexp"

// bracketPrefix matches Spring/Micrometer MDC style at the start of msg:
// [traceId, spanId] — 32-char hex IDs without hyphens.
var bracketPrefix = regexp.MustCompile(`^\[([0-9a-fA-F]{32}),\s*([0-9a-fA-F]{32})\]`)

// ParseBracketIDs extracts traceId and spanId from a log message prefix.
// Returns ok=false when the pattern is not present.
func ParseBracketIDs(msg string) (traceID, spanID string, ok bool) {
	m := bracketPrefix.FindStringSubmatch(msg)
	if len(m) != 3 {
		return "", "", false
	}
	return m[1], m[2], true
}
