package kibanaclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// AggOptions configures one aggregation over log documents.
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
	TraceID       string
	TraceFields   []string
	TraceMode     string
	Fields        map[string]string
	Query         string
	// QueryClause is a precompiled Elasticsearch query and takes precedence over Query.
	QueryClause   map[string]any
	MsgOnly       bool
	MessageField  string
	MessageFields []string
	// AggType selects the bucketing: "terms" (default) or "date_histogram".
	AggType string
	// Interval is the fixed interval of a date_histogram (30m, 1h, 1d).
	Interval string
	// BoundsMin and BoundsMax (epoch milliseconds) extend a date_histogram to the
	// whole time range, so hours without documents show as 0.
	BoundsMin, BoundsMax *int64
	// Metric is an optional sub-aggregation per bucket: avg|sum|min|max|count.
	// Empty or count means buckets carry only their document count.
	Metric string
	// MetricField is the numeric field the metric is computed over.
	MetricField string
}

// AggType constants.
const (
	AggTypeTerms         = "terms"
	AggTypeDateHistogram = "date_histogram"
)

// Aggregate runs a terms or date_histogram aggregation, with an optional metric per
// bucket. A terms aggregation on a text field that Elasticsearch refuses (400) is
// asked again on <field>.keyword, a second read.
func (c *Client) Aggregate(ctx context.Context, opts AggOptions) (*AggResult, error) {
	opts = normalizeAggOptions(opts)
	if opts.AggType == AggTypeDateHistogram {
		r, err := c.aggOnce(ctx, opts, opts.TimeField)
		if err == nil {
			r.AggType = AggTypeDateHistogram
		}
		return r, err
	}
	field := strings.TrimSpace(opts.TermsField)
	r, err := c.aggOnce(ctx, opts, field)
	var apiErr *APIError
	if err != nil && field != "" && !strings.HasSuffix(field, ".keyword") && errors.As(err, &apiErr) && isTextFieldAggError(apiErr) {
		opts.TermsField = field + ".keyword"
		if r2, err2 := c.aggOnce(ctx, opts, opts.TermsField); err2 == nil {
			r, err = r2, nil
		}
	}
	if err == nil {
		r.AggType = AggTypeTerms
	}
	return r, err
}

func (c *Client) aggOnce(ctx context.Context, opts AggOptions, field string) (*AggResult, error) {
	data, err := c.Proxy(ctx, http.MethodPost, searchTarget(opts.Index), BuildAggBody(opts))
	if err != nil {
		return nil, err
	}
	return parseBucketAgg(data, field, opts.Metric)
}

// BuildAggBody returns the _search body Aggregate sends first (the .keyword retry is
// a second request).
func BuildAggBody(opts AggOptions) map[string]any {
	opts = normalizeAggOptions(opts)
	var agg map[string]any
	if opts.AggType == AggTypeDateHistogram {
		dh := map[string]any{
			"field":          opts.TimeField,
			"fixed_interval": opts.Interval,
			"min_doc_count":  0,
			"format":         "strict_date_optional_time",
		}
		if opts.BoundsMin != nil && opts.BoundsMax != nil {
			dh["extended_bounds"] = map[string]any{"min": *opts.BoundsMin, "max": *opts.BoundsMax}
		}
		agg = map[string]any{"date_histogram": dh}
	} else {
		agg = map[string]any{"terms": map[string]any{"field": strings.TrimSpace(opts.TermsField), "size": opts.BucketSize}}
	}
	if metric := metricSubAgg(opts); metric != nil {
		agg["aggs"] = map[string]any{"metric": metric}
	}
	q := SearchOptions{Index: opts.Index, From: opts.From, To: opts.To, TimeField: opts.TimeField, Query: opts.Query,
		QueryClause: opts.QueryClause, MsgOnly: opts.MsgOnly, MessageField: opts.MessageField, MessageFields: opts.MessageFields,
		ServiceValue: opts.ServiceValue, ServiceFields: opts.ServiceFields, LevelValue: opts.LevelValue, LevelFields: opts.LevelFields,
		TraceID: opts.TraceID, TraceFields: opts.TraceFields, TraceMode: opts.TraceMode, Fields: opts.Fields}
	return map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query":            buildQuery(q),
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
	if strings.ToLower(strings.TrimSpace(opts.AggType)) == AggTypeDateHistogram {
		opts.AggType = AggTypeDateHistogram
	} else {
		opts.AggType = AggTypeTerms
	}
	return opts
}

// metricSubAgg builds the metric sub-aggregation, or nil for count and none.
func metricSubAgg(opts AggOptions) map[string]any {
	m := normalizeMetric(opts.Metric)
	if !hasMetric(m) {
		return nil
	}
	return map[string]any{m: map[string]any{"field": strings.TrimSpace(opts.MetricField)}}
}

func normalizeMetric(m string) string {
	return strings.ToLower(strings.TrimSpace(m))
}

func hasMetric(m string) bool {
	v := normalizeMetric(m)
	return v != "" && v != "count"
}

// isTextFieldAggError recognises Elasticsearch's refusal to aggregate a text field
// (fielddata disabled).
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
