package cmd

import (
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/kql"
	"github.com/spf13/cobra"
)

const (
	queryLanguageLucene = "lucene"
	queryLanguageKQL    = "kql"
)

func compileFlagQuery(cmd *cobra.Command) (string, map[string]any, error) {
	language, _ := cmd.Flags().GetString("query-language")
	language = strings.ToLower(strings.TrimSpace(language))
	switch language {
	case queryLanguageLucene, queryLanguageKQL:
	default:
		return "", nil, failValidation("--query-language must be one of: lucene, kql")
	}

	query, _ := cmd.Flags().GetString("query")
	precise, _ := cmd.Flags().GetBool("precise")
	if language == queryLanguageKQL {
		if precise {
			return "", nil, failValidation("--precise cannot be combined with --query-language kql; express the field/phrase in KQL")
		}
		clause, err := kql.Parse(query)
		if err != nil {
			return "", nil, failValidation(err.Error())
		}
		if strings.TrimSpace(query) == "" {
			return language, nil, nil
		}
		return language, clause, nil
	}

	if !precise && ambiguousLuceneBoolean(query) && !cmd.Flags().Changed("query-language") {
		return "", nil, failValidation("--query contains KQL-style lowercase boolean operators; use --query-language kql, or use uppercase AND/OR/NOT for Lucene, and quote the whole expression as one shell argument")
	}
	return language, nil, nil
}

func validateRawDSLQueryFlags(cmd *cobra.Command) error {
	query, _ := cmd.Flags().GetString("query")
	precise, _ := cmd.Flags().GetBool("precise")
	if strings.TrimSpace(query) != "" || precise || cmd.Flags().Changed("query-language") {
		return failValidation("--dsl cannot be combined with --query, --precise, or --query-language")
	}
	return nil
}

func ambiguousLuceneBoolean(query string) bool {
	visible := outsideQuotedText(query)
	for i := 0; i < len(visible); i++ {
		if i > 0 && !booleanBoundary(visible[i-1]) {
			continue
		}
		for _, keyword := range []string{"and", "or", "not"} {
			end := i + len(keyword)
			if end > len(visible) || !strings.EqualFold(visible[i:end], keyword) {
				continue
			}
			if end < len(visible) && !booleanBoundary(visible[end]) {
				continue
			}
			if visible[i:end] != strings.ToUpper(visible[i:end]) {
				return true
			}
		}
	}
	return false
}

func booleanBoundary(value byte) bool {
	return value == '(' || value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func outsideQuotedText(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	inQuote := false
	escaped := false
	for _, r := range value {
		if escaped {
			out.WriteByte(' ')
			escaped = false
			continue
		}
		if r == '\\' {
			out.WriteByte(' ')
			escaped = true
			continue
		}
		if r == '"' {
			out.WriteByte(' ')
			inQuote = !inQuote
			continue
		}
		if inQuote {
			out.WriteByte(' ')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
