package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ParseIndexPrefixes splits the allowed_index_prefixes setting (comma-separated)
// into its non-empty prefixes; nil means no allowlist.
func ParseIndexPrefixes(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// indexPart is one index or index pattern as Elasticsearch accepts it in a search
// path: letters, digits, '.', '_', '-', '+', '*' and ':' (a remote cluster). Every
// character that could end the path or start a query string ('/', '?', '#', '&',
// whitespace, quotes) is refused, so an index can only ever address <index>/_search.
var indexPart = regexp.MustCompile(`^[A-Za-z0-9._*+:-]+$`)

// ValidateIndexTarget checks an index or comma-separated list of index patterns
// before it reaches Elasticsearch: every part is a plain index pattern, none names
// a system index (leading '_') or an exclusion (leading '-'), none walks a path, and,
// when prefixes is not empty, every part starts with one of them. Each part is
// checked on its own, so "allowed-*,other-*" cannot slip "other-*" past the list.
func ValidateIndexTarget(index string, prefixes []string) error {
	index = strings.TrimSpace(index)
	if index == "" {
		return errors.New("index is required")
	}
	for _, part := range strings.Split(index, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
			return errors.New("the index list has an empty entry")
		case strings.Contains(part, ".."):
			return errors.New("an index pattern must not contain '..'")
		case !indexPart.MatchString(part):
			return errors.New("an index pattern may hold only letters, digits and the characters . _ - + * and colon")
		case strings.HasPrefix(part, "_"):
			return errors.New("system indices (a leading '_') are not searched")
		case strings.HasPrefix(part, "-"):
			return errors.New("exclusions (a leading '-') are not accepted; name the indices to search")
		}
		if len(prefixes) == 0 {
			continue
		}
		// A data stream's backing index (.ds-<stream>-<date>-<generation>, as a hit's
		// _index names it) is allowed when its stream's name is.
		stream := strings.TrimPrefix(part, ".ds-")
		allowed := false
		for _, p := range prefixes {
			if strings.HasPrefix(part, p) || strings.HasPrefix(stream, p) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("an index pattern is outside the allowed_index_prefixes setting (%s)", strings.Join(prefixes, ", "))
		}
	}
	return nil
}
