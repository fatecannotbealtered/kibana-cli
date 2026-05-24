package kibanaclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// IndexField describes one field on a Kibana index pattern.
type IndexField struct {
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	ESTypes           []string `json:"esTypes,omitempty"`
	Searchable        bool     `json:"searchable"`
	Aggregatable      bool     `json:"aggregatable"`
	ReadFromDocValues bool     `json:"readFromDocValues,omitempty"`
}

// ListFieldsForIndexPattern returns fields for an index pattern title via Kibana API.
func (c *Client) ListFieldsForIndexPattern(ctx context.Context, indexPattern string) ([]IndexField, error) {
	if err := c.EnsureVersion(ctx); err != nil {
		return nil, err
	}
	indexPattern = strings.TrimSpace(indexPattern)
	if indexPattern == "" {
		return nil, fmt.Errorf("index pattern is required")
	}
	q := url.Values{}
	q.Set("pattern", indexPattern)
	for _, meta := range []string{"_source", "_id", "_index", "_type", "_score"} {
		q.Add("meta_fields", meta)
	}
	u := c.baseURL + "/api/index_patterns/_fields_for_wildcard?" + q.Encode()
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
	return parseFieldsForWildcard(data)
}

func parseFieldsForWildcard(data []byte) ([]IndexField, error) {
	var raw struct {
		Fields json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse fields response: %w", err)
	}
	if len(raw.Fields) == 0 {
		return nil, nil
	}
	var list []IndexField
	if err := json.Unmarshal(raw.Fields, &list); err == nil {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		return list, nil
	}
	var byName map[string]IndexField
	if err := json.Unmarshal(raw.Fields, &byName); err != nil {
		return nil, fmt.Errorf("parse fields: %w", err)
	}
	list = make([]IndexField, 0, len(byName))
	for name, f := range byName {
		if f.Name == "" {
			f.Name = name
		}
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}
