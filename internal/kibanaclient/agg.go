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
	// QueryClause is a precompiled Elasticsearch query and takes precedence over Query.
	QueryClause   map[string]any
	MsgOnly       bool
	MessageField  string
	MessageFields []string
	// AggType selects the bucketing: "terms" (default) or "date_histogram".
	AggType string
	// Interval is the calendar/fixed interval for date_histogram (e.g. 1h, 1d).
	Interval string
	// Metric is an optional sub-aggregation per bucket: avg|sum|min|max|count.
	// Empty means buckets carry only the doc_count.
	Metric string
	// MetricField is the numeric field the metric is computed over (not needed for count).
	MetricField string
}

// AggType constants.
const (
	AggTypeTerms         = "terms"
	AggTypeDateHistogram = "date_histogram"
)

// Aggregate runs a terms or date_histogram aggregation, optionally with a metric
// sub-aggregation per bucket. It is the general entry point; Terms remains for the
// plain count-by-terms case and its .keyword retry.
func (c *Client) Aggregate(ctx context.Context, opts AggOptions) (*AggResult, error) {
	opts = normalizeAggOptions(opts)
	switch normalizeAggType(opts.AggType) {
	case AggTypeDateHistogram:
		r, err := c.dateHistogram(ctx, opts)
		if err == nil {
			r.AggType = AggTypeDateHistogram
		}
		return r, err
	default:
		// terms: reuse Terms for the text-field .keyword retry, then layer metrics.
		var (
			r   *AggResult
			err error
		)
		if !hasMetric(opts.Metric) {
			r, err = c.Terms(ctx, opts)
		} else {
			r, err = c.termsWithMetric(ctx, opts)
		}
		if err == nil {
			r.AggType = AggTypeTerms
		}
		return r, err
	}
}

func (c *Client) dateHistogram(ctx context.Context, opts AggOptions) (*AggResult, error) {
	body := BuildAggBody(opts)
	data, err := c.proxyAgg(ctx, opts.Index, body)
	if err != nil {
		return nil, err
	}
	return parseBucketAgg(data, opts.TimeField, opts.Metric)
}

func (c *Client) termsWithMetric(ctx context.Context, opts AggOptions) (*AggResult, error) {
	field := strings.TrimSpace(opts.TermsField)
	result, err := c.termsMetricOnce(ctx, opts, field)
	if err == nil {
		return result, nil
	}
	var apiErr *APIError
	if field != "" && !strings.HasSuffix(field, ".keyword") && errors.As(err, &apiErr) && isTextFieldAggError(apiErr) {
		if r2, err2 := c.termsMetricOnce(ctx, opts, field+".keyword"); err2 == nil {
			r2.Field = field + " (.keyword)"
			return r2, nil
		}
	}
	return nil, err
}

func (c *Client) termsMetricOnce(ctx context.Context, opts AggOptions, field string) (*AggResult, error) {
	opts.AggType = AggTypeTerms
	opts.TermsField = field
	body := BuildAggBody(opts)
	data, err := c.proxyAgg(ctx, opts.Index, body)
	if err != nil {
		return nil, err
	}
	return parseBucketAgg(data, field, opts.Metric)
}

// BuildAggBody returns the first Elasticsearch _search request body used by
// Aggregate. A text-field .keyword retry is a later request and is not included.
func BuildAggBody(opts AggOptions) map[string]any {
	opts = normalizeAggOptions(opts)
	var agg map[string]any
	if normalizeAggType(opts.AggType) == AggTypeDateHistogram {
		agg = map[string]any{
			"date_histogram": map[string]any{
				"field":          opts.TimeField,
				"fixed_interval": opts.Interval,
				"min_doc_count":  0,
				"format":         "strict_date_optional_time",
			},
		}
	} else {
		agg = map[string]any{
			"terms": map[string]any{
				"field": strings.TrimSpace(opts.TermsField),
				"size":  opts.BucketSize,
			},
		}
	}
	if metric := metricSubAgg(opts); metric != nil {
		agg["aggs"] = map[string]any{"metric": metric}
	}

	searchOpts := SearchOptions{
		Index:         opts.Index,
		From:          opts.From,
		To:            opts.To,
		TimeField:     opts.TimeField,
		Query:         opts.Query,
		QueryClause:   opts.QueryClause,
		MsgOnly:       opts.MsgOnly,
		MessageField:  opts.MessageField,
		MessageFields: opts.MessageFields,
		ServiceValue:  opts.ServiceValue,
		ServiceFields: opts.ServiceFields,
		LevelValue:    opts.LevelValue,
		LevelFields:   opts.LevelFields,
	}
	return map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query":            buildQuery(searchOpts),
		"aggs":             map[string]any{"terms_agg": agg},
	}
}

func normalizeAggOptions(opts AggOptions) AggOptions {
	if opts.TimeField == "" {
		opts.TimeField = "@timestamp"
	}
	if opts.BucketSize <= 0 {
		opts.BucketSize = 10
	}
	opts.Interval = strings.TrimSpace(opts.Interval)
	if opts.Interval == "" {
		opts.Interval = "1h"
	}
	return opts
}

func (c *Client) proxyAgg(ctx context.Context, index string, body map[string]any) ([]byte, error) {
	path := strings.Trim(strings.TrimPrefix(index, "/"), "/") + "/_search"
	return c.Proxy(ctx, http.MethodPost, path, body)
}

// metricSubAgg builds the ES metric sub-aggregation, or nil for count/none.
func metricSubAgg(opts AggOptions) map[string]any {
	m := normalizeMetric(opts.Metric)
	if m == "" || m == "count" {
		return nil
	}
	return map[string]any{m: map[string]any{"field": strings.TrimSpace(opts.MetricField)}}
}

func normalizeAggType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case AggTypeDateHistogram, "date-histogram", "histogram":
		return AggTypeDateHistogram
	default:
		return AggTypeTerms
	}
}

func normalizeMetric(m string) string {
	return strings.ToLower(strings.TrimSpace(m))
}

func hasMetric(m string) bool {
	v := normalizeMetric(m)
	return v != "" && v != "count"
}

// Terms runs a terms aggregation (retries with .keyword on text-field errors).
func (c *Client) Terms(ctx context.Context, opts AggOptions) (*AggResult, error) {
	opts = normalizeAggOptions(opts)
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
	opts.AggType = AggTypeTerms
	opts.TermsField = field
	body := BuildAggBody(opts)
	data, err := c.proxyAgg(ctx, opts.Index, body)
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
