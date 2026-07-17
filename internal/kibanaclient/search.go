package kibanaclient

import (
	"context"
	"net/http"
	"strings"
)

// BuildSearchBody returns the final Elasticsearch _search request body used by
// Search, including defaults and pagination state.
func BuildSearchBody(opts SearchOptions) map[string]any {
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
	usingCursor := len(opts.SearchAfter) > 0
	// search_after is offset-free: `from` is forbidden alongside it, so only
	// honor Offset in the classic paging path.
	if opts.Offset > 0 && !usingCursor {
		body["from"] = opts.Offset
	}
	// search_after needs a deterministic total order, so append an _id tiebreaker
	// to the descending time sort whenever a cursor is in play.
	if opts.SortDesc || usingCursor {
		sort := []any{map[string]any{opts.TimeField: map[string]string{"order": "desc"}}}
		if usingCursor {
			sort = append(sort, map[string]any{"_id": map[string]string{"order": "desc"}})
		}
		body["sort"] = sort
	}
	if usingCursor {
		body["search_after"] = opts.SearchAfter
	}
	return body
}

// Search runs a log search via Console Proxy.
func (c *Client) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	body := BuildSearchBody(opts)
	path := strings.Trim(strings.TrimPrefix(opts.Index, "/"), "/") + "/_search"
	data, err := c.Proxy(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(data)
}

// SearchRaw sends a caller-supplied Elasticsearch query DSL body directly to
// _search through the Console Proxy, bypassing the flag-based query builder. The
// body must already be a valid _search request object; callers validate the JSON.
func (c *Client) SearchRaw(ctx context.Context, index string, body map[string]any) (*SearchResult, error) {
	target := strings.Trim(strings.TrimPrefix(index, "/"), "/")
	if target == "" {
		target = "*"
	}
	path := target + "/_search"
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
