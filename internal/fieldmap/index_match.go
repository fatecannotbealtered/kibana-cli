package fieldmap

import (
	"path/filepath"
	"strings"
)

// ProfileIndexPatterns returns all index globs for a profile (index_patterns or legacy index).
func ProfileIndexPatterns(p Profile) []string {
	if len(p.IndexPatterns) > 0 {
		return p.IndexPatterns
	}
	if strings.TrimSpace(p.Index) != "" {
		return []string{strings.TrimSpace(p.Index)}
	}
	return nil
}

// IndexMatchesPattern reports whether index matches a glob (* and ? via filepath.Match).
func IndexMatchesPattern(index, pattern string) bool {
	index = strings.TrimSpace(index)
	pattern = strings.TrimSpace(pattern)
	if index == "" || pattern == "" {
		return false
	}
	if strings.ContainsAny(pattern, "*?[") {
		ok, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(index))
		return err == nil && ok
	}
	return strings.EqualFold(index, pattern)
}

// patternSpecificity scores a glob; higher wins when multiple profiles match.
func patternSpecificity(pattern string) int {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return 0
	}
	score := len(pattern) * 2
	score -= strings.Count(pattern, "*") * 3
	score -= strings.Count(pattern, "?")
	return score
}
