package output

import "strings"

// FilterMap returns a copy of m containing only the keys in fieldNames (case-insensitive lookup).
func FilterMap(m map[string]any, fieldNames []string) map[string]any {
	if len(fieldNames) == 0 {
		return m
	}
	index := make(map[string]string, len(m))
	for k := range m {
		index[strings.ToLower(k)] = k
	}
	result := make(map[string]any, len(fieldNames))
	for _, name := range fieldNames {
		wanted := strings.TrimSpace(strings.ToLower(name))
		if origKey, ok := index[wanted]; ok {
			result[origKey] = m[origKey]
		}
	}
	return result
}
