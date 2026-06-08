package kibanaclient

import (
	"context"
	"net/http"
	"strings"
)

// Search runs a log search via Console Proxy.
func (c *Client) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	if opts.TimeField == "" {
		opts.TimeField = "@timestamp"
	}
	if opts.Size <= 0 {
		opts.Size = 50
	}
	body := map[string]any{
		"query":            buildQuery(opts),
		"size":             opts.Size,
		"track_total_hits": true,
	}
	if opts.Offset > 0 {
		body["from"] = opts.Offset
	}
	if opts.SortDesc {
		body["sort"] = []any{map[string]any{opts.TimeField: map[string]string{"order": "desc"}}}
	}
	path := strings.Trim(strings.TrimPrefix(opts.Index, "/"), "/") + "/_search"
	data, err := c.Proxy(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(data)
}

// Count returns only the total hit count (size:0) for the given options — used for zero-result probes.
func (c *Client) Count(ctx context.Context, opts SearchOptions) (int64, error) {
	if opts.TimeField == "" {
		opts.TimeField = "@timestamp"
	}
	body := map[string]any{
		"query":            buildQuery(opts),
		"size":             0,
		"track_total_hits": true,
	}
	path := strings.Trim(strings.TrimPrefix(opts.Index, "/"), "/") + "/_search"
	data, err := c.Proxy(ctx, http.MethodPost, path, body)
	if err != nil {
		return 0, err
	}
	r, err := parseSearchResponse(data)
	if err != nil {
		return 0, err
	}
	return r.Total, nil
}
