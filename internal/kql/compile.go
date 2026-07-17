package kql

import (
	"strings"
	"unicode"
)

func compileNode(current *node, nestedPath string) (map[string]any, error) {
	switch current.kind {
	case nodeTerm:
		return compileTerm(current, nestedPath)
	case nodeRange:
		field := qualifyLiteral(*current.field, nestedPath).text
		return map[string]any{
			"range": map[string]any{
				field: map[string]any{current.op: current.value.value},
			},
		}, nil
	case nodeAnd:
		left, err := compileNode(current.left, nestedPath)
		if err != nil {
			return nil, err
		}
		right, err := compileNode(current.right, nestedPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"bool": map[string]any{"filter": []map[string]any{left, right}},
		}, nil
	case nodeOr:
		left, err := compileNode(current.left, nestedPath)
		if err != nil {
			return nil, err
		}
		right, err := compileNode(current.right, nestedPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"bool": map[string]any{
				"should":               []map[string]any{left, right},
				"minimum_should_match": 1,
			},
		}, nil
	case nodeNot:
		child, err := compileNode(current.child, nestedPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"bool": map[string]any{"must_not": child},
		}, nil
	case nodeNested:
		path := current.field.text
		if nestedPath != "" {
			path = nestedPath + "." + path
		}
		child, err := compileNode(current.child, path)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"nested": map[string]any{
				"path":       path,
				"query":      child,
				"score_mode": "none",
			},
		}, nil
	default:
		return nil, &syntaxError{position: current.pos, message: "unsupported expression"}
	}
}

func compileTerm(current *node, nestedPath string) (map[string]any, error) {
	value := current.value
	if value.wildcard && strings.IndexFunc(value.text, unicode.IsSpace) >= 0 {
		return nil, &syntaxError{
			position: value.pos,
			message:  "wildcard values containing whitespace are ambiguous; rewrite as explicit field boolean clauses",
		}
	}
	if current.field == nil {
		if nestedPath != "" {
			return nil, &syntaxError{
				position: current.pos,
				message:  "terms without a field are not supported inside nested queries",
			}
		}
		if value.wildcard {
			return map[string]any{
				"query_string": map[string]any{"query": value.queryStringValue()},
			}, nil
		}
		queryType := "best_fields"
		if value.quoted {
			queryType = "phrase"
		}
		return map[string]any{
			"multi_match": map[string]any{
				"query":   value.value,
				"type":    queryType,
				"lenient": true,
			},
		}, nil
	}

	if current.field.wildcard {
		if nestedPath == "" && current.field.isSingleWildcard() && value.isSingleWildcard() {
			return map[string]any{"match_all": map[string]any{}}, nil
		}
		return nil, &syntaxError{
			position: current.field.pos,
			message:  "wildcard field names require data-view field metadata; only *:* is supported without it",
		}
	}
	field := qualifyLiteral(*current.field, nestedPath)
	if value.isSingleWildcard() {
		if field.hasLiteralWildcard() {
			return nil, &syntaxError{
				position: current.field.pos,
				message:  "exists queries cannot distinguish a literal '*' in a field name",
			}
		}
		return map[string]any{
			"exists": map[string]any{"field": field.fieldPattern()},
		}, nil
	}
	if field.wildcard || value.wildcard {
		return map[string]any{
			"query_string": map[string]any{
				"fields": []string{field.fieldPattern()},
				"query":  value.queryStringValue(),
			},
		}, nil
	}
	if value.quoted {
		return map[string]any{
			"match_phrase": map[string]any{field.text: value.value},
		}, nil
	}
	return map[string]any{
		"match": map[string]any{field.text: value.value},
	}, nil
}

func qualifyLiteral(field literal, nestedPath string) literal {
	if nestedPath == "" {
		return field
	}
	field.text = nestedPath + "." + field.text
	if field.wildcard {
		field.encoded = nestedPath + "." + field.encoded
	} else {
		field.encoded = field.text
	}
	return field
}

func (value literal) isSingleWildcard() bool {
	return value.wildcard && value.encoded == wildcardMarker
}

func (value literal) fieldPattern() string {
	return strings.ReplaceAll(value.encoded, wildcardMarker, "*")
}

func (value literal) hasLiteralWildcard() bool {
	return strings.Contains(value.encoded, "*")
}

func (value literal) queryStringValue() string {
	if value.quoted {
		return `"` + escapeQueryStringReserved(value.text) + `"`
	}
	return escapeQueryStringPattern(value.encoded)
}

func escapeQueryStringPattern(value string) string {
	var escaped strings.Builder
	for i := 0; i < len(value); {
		if value[i] == wildcardMarker[0] {
			escaped.WriteByte('*')
			i++
			continue
		}
		char := rune(value[i])
		size := 1
		if char >= 0x80 {
			for size < len(value)-i && value[i+size]&0xc0 == 0x80 {
				size++
			}
			char = []rune(value[i : i+size])[0]
		}
		if strings.ContainsRune(`+-=&|><!(){}[]^"~*?:\/`, char) {
			escaped.WriteByte('\\')
		}
		escaped.WriteString(value[i : i+size])
		i += size
	}
	return escaped.String()
}

func escapeQueryStringReserved(value string) string {
	var escaped strings.Builder
	for _, char := range value {
		if strings.ContainsRune(`+-=&|><!(){}[]^"~*?:\/`, char) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(char)
	}
	return escaped.String()
}
