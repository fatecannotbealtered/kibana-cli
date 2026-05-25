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
