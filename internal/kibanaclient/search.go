package kibanaclient

import (
	"context"
	"net/http"
	"strings"
)

// BuildSearchBody returns the Elasticsearch _search body Search sends: the query, the
// page (size and from), exact totals, and newest first on the time field. The sort
// carries unmapped_type, as Kibana's Discover does, so an index of the pattern that
// lacks the time field does not fail its shards.
func BuildSearchBody(opts SearchOptions) map[string]any {
	if opts.TimeField == "" {
		opts.TimeField = "@timestamp"
	}
	if opts.Size <= 0 {
		opts.Size = 20
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
		body["sort"] = []any{map[string]any{opts.TimeField: map[string]any{"order": "desc", "unmapped_type": "boolean"}}}
	}
	return body
}

// searchTarget is the Elasticsearch path of a search on index.
func searchTarget(index string) string {
	target := strings.Trim(strings.TrimPrefix(strings.TrimSpace(index), "/"), "/")
	if target == "" {
		target = "*"
	}
	return target + "/_search"
}

// Search runs a log search through the Console Proxy.
func (c *Client) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	data, err := c.Proxy(ctx, http.MethodPost, searchTarget(opts.Index), BuildSearchBody(opts))
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(data)
}

// SearchRaw sends a caller-supplied _search body unchanged. It is still a search:
// the path is <index>/_search, which proxyAllowed enforces.
func (c *Client) SearchRaw(ctx context.Context, index string, body map[string]any) (*SearchResult, error) {
	data, err := c.Proxy(ctx, http.MethodPost, searchTarget(index), body)
	if err != nil {
		return nil, err
	}
	return parseSearchResponse(data)
}

// Count returns the exact number of documents opts matches (size 0).
func (c *Client) Count(ctx context.Context, opts SearchOptions) (int64, error) {
	if opts.TimeField == "" {
		opts.TimeField = "@timestamp"
	}
	body := map[string]any{"query": buildQuery(opts), "size": 0, "track_total_hits": true}
	data, err := c.Proxy(ctx, http.MethodPost, searchTarget(opts.Index), body)
	if err != nil {
		return 0, err
	}
	r, err := parseSearchResponse(data)
	if err != nil {
		return 0, err
	}
	return r.Total, nil
}

// GetDoc reads one document by index and _id, with an ids query through _search (the
// Console Proxy path stays a search). It returns nil when no document matches.
func (c *Client) GetDoc(ctx context.Context, index, id string) (*SearchHit, error) {
	body := map[string]any{"query": map[string]any{"ids": map[string]any{"values": []string{id}}}, "size": 2}
	data, err := c.Proxy(ctx, http.MethodPost, searchTarget(index), body)
	if err != nil {
		return nil, err
	}
	r, err := parseSearchResponse(data)
	if err != nil {
		return nil, err
	}
	for i := range r.Hits {
		if r.Hits[i].ID == id {
			return &r.Hits[i], nil
		}
	}
	return nil, nil
}
