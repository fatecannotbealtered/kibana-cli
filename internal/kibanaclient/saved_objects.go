package kibanaclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// IndexPattern is a Kibana saved index-pattern.
type IndexPattern struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ListIndexPatterns returns saved index-pattern objects (paginated).
func (c *Client) ListIndexPatterns(ctx context.Context) ([]IndexPattern, error) {
	if err := c.EnsureVersion(ctx); err != nil {
		return nil, err
	}
	const perPage = 100
	var out []IndexPattern
	for page := 1; page <= 50; page++ {
		u := fmt.Sprintf("%s/api/saved_objects/_find?type=index-pattern&per_page=%d&page=%d",
			c.baseURL, perPage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header = c.proxyHeaders()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode >= 400 {
			return nil, parseAPIError(resp.StatusCode, data)
		}
		var raw struct {
			SavedObjects []struct {
				ID         string `json:"id"`
				Attributes struct {
					Title string `json:"title"`
				} `json:"attributes"`
			} `json:"saved_objects"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		if len(raw.SavedObjects) == 0 {
			break
		}
		for _, o := range raw.SavedObjects {
			out = append(out, IndexPattern{ID: o.ID, Title: o.Attributes.Title})
		}
		if len(raw.SavedObjects) < perPage {
			break
		}
	}
	return out, nil
}

// ResolveIndexPattern returns the index pattern title for a Kibana data view id.
func (c *Client) ResolveIndexPattern(ctx context.Context, id string) (string, error) {
	if err := c.EnsureVersion(ctx); err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/api/saved_objects/index-pattern/%s", c.baseURL, url.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header = c.proxyHeaders()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode >= 400 {
		return "", parseAPIError(resp.StatusCode, data)
	}
	var raw struct {
		Attributes struct {
			Title string `json:"title"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", err
	}
	if raw.Attributes.Title == "" {
		return "", fmt.Errorf("index-pattern %s has no title", id)
	}
	return raw.Attributes.Title, nil
}
