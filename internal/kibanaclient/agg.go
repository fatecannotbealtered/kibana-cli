package kibanaclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// AggOptions configures a terms aggregation on logs.
type AggOptions struct {
	Index         string
	From          string
	To            string
	TimeField     string
	TermsField    string
	BucketSize    int
	ServiceValue  string
	ServiceFields []string
	LevelValue    string
	LevelFields   []string
	Query         string
	MsgOnly       bool
	MessageField  string
}

// Terms runs a terms aggregation (retries with .keyword on text-field errors).
func (c *Client) Terms(ctx context.Context, opts AggOptions) (*AggResult, error) {
	if opts.TimeField == "" {
		opts.TimeField = "@timestamp"
	}
	if opts.BucketSize <= 0 {
		opts.BucketSize = 10
	}
	field := strings.TrimSpace(opts.TermsField)
	result, err := c.termsOnce(ctx, opts, field)
	if err == nil {
		return result, nil
	}
	var apiErr *APIError
	if field != "" && !strings.HasSuffix(field, ".keyword") && errors.As(err, &apiErr) && isTextFieldAggError(apiErr) {
		if r2, err2 := c.termsOnce(ctx, opts, field+".keyword"); err2 == nil {
			r2.Field = field + " (.keyword)"
			return r2, nil
		}
	}
	return nil, err
}

func (c *Client) termsOnce(ctx context.Context, opts AggOptions, field string) (*AggResult, error) {
	searchOpts := SearchOptions{
		Index:         opts.Index,
		From:          opts.From,
		To:            opts.To,
		TimeField:     opts.TimeField,
		Query:         opts.Query,
		MsgOnly:       opts.MsgOnly,
		MessageField:  opts.MessageField,
		ServiceValue:  opts.ServiceValue,
		ServiceFields: opts.ServiceFields,
		LevelValue:    opts.LevelValue,
		LevelFields:   opts.LevelFields,
	}
	body := map[string]any{
		"size":  0,
		"query": buildQuery(searchOpts),
		"aggs": map[string]any{
			"terms_agg": map[string]any{
				"terms": map[string]any{
					"field": field,
					"size":  opts.BucketSize,
				},
			},
		},
	}
	path := strings.Trim(strings.TrimPrefix(opts.Index, "/"), "/") + "/_search"
	data, err := c.Proxy(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	return parseTermsAgg(data, field)
}

func isTextFieldAggError(e *APIError) bool {
	if e == nil || e.StatusCode != 400 {
		return false
	}
	msg := strings.ToLower(e.Message + " " + e.Body)
	return strings.Contains(msg, "fielddata") ||
		strings.Contains(msg, "text fields") ||
		strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "keyword")
}
