package kibanaclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// SavedObject is a Kibana saved object with its title/description surfaced.
// Title and Description are externally-authored content; callers tag them
// _untrusted so agents treat them as data, never instructions.
type SavedObject struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// FindSavedObjectsOptions configures a saved-objects _find query.
type FindSavedObjectsOptions struct {
	Type    string
	Search  string
	Page    int
	PerPage int
}

// FindSavedObjects lists saved objects of a type via /api/saved_objects/_find,
// optionally filtered by a free-text --search term.
func (c *Client) FindSavedObjects(ctx context.Context, opts FindSavedObjectsOptions) ([]SavedObject, int, error) {
	if err := c.EnsureVersion(ctx); err != nil {
		return nil, 0, err
	}
	if opts.PerPage <= 0 {
		opts.PerPage = 100
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	q := url.Values{}
	q.Set("type", opts.Type)
	q.Set("per_page", strconv.Itoa(opts.PerPage))
	q.Set("page", strconv.Itoa(opts.Page))
	if s := strings.TrimSpace(opts.Search); s != "" {
		q.Set("search", s)
		q.Set("search_fields", "title")
	}
	u := fmt.Sprintf("%s/api/saved_objects/_find?%s", c.baseURL, q.Encode())
	data, err := c.kibanaGET(ctx, u)
	if err != nil {
		return nil, 0, err
	}
	var raw struct {
		Total        int `json:"total"`
		SavedObjects []struct {
			ID         string `json:"id"`
			Type       string `json:"type"`
			UpdatedAt  string `json:"updated_at"`
			Attributes struct {
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"attributes"`
		} `json:"saved_objects"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, 0, err
	}
	out := make([]SavedObject, 0, len(raw.SavedObjects))
	for _, o := range raw.SavedObjects {
		out = append(out, SavedObject{
			ID:          o.ID,
			Type:        o.Type,
			Title:       o.Attributes.Title,
			Description: o.Attributes.Description,
			UpdatedAt:   o.UpdatedAt,
		})
	}
	return out, raw.Total, nil
}

// GetSavedObject fetches one saved object via /api/saved_objects/<type>/<id>.
// The returned map is the raw Kibana object; Title/Description are surfaced
// separately for the _untrusted contract.
func (c *Client) GetSavedObject(ctx context.Context, objType, id string) (*SavedObject, map[string]any, error) {
	if err := c.EnsureVersion(ctx); err != nil {
		return nil, nil, err
	}
	u := fmt.Sprintf("%s/api/saved_objects/%s/%s",
		c.baseURL, url.PathEscape(objType), url.PathEscape(id))
	data, err := c.kibanaGET(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	var raw struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		UpdatedAt  string `json:"updated_at"`
		Attributes struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	var full map[string]any
	_ = json.Unmarshal(data, &full)
	obj := &SavedObject{
		ID:          raw.ID,
		Type:        raw.Type,
		Title:       raw.Attributes.Title,
		Description: raw.Attributes.Description,
		UpdatedAt:   raw.UpdatedAt,
	}
	return obj, full, nil
}

// kibanaGET issues an authenticated GET to a Kibana API URL (no Console Proxy).
func (c *Client) kibanaGET(ctx context.Context, u string) ([]byte, error) {
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
	return data, nil
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
