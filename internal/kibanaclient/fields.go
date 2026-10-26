package kibanaclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// IndexField is one field of an index pattern.
type IndexField struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	ESTypes      []string `json:"esTypes,omitempty"`
	Searchable   bool     `json:"searchable"`
	Aggregatable bool     `json:"aggregatable"`
}

// ListFields returns the fields of an index pattern as Kibana sees them. Kibana 7.x
// answers GET /api/index_patterns/_fields_for_wildcard; 8.x moved it to the internal
// GET /internal/data_views/_fields_for_wildcard (a versioned internal route, so it
// gets elastic-api-version: 1 beside x-elastic-internal-origin). The client asks the
// route that fits the known version first (8.x's when the version is unknown, the
// common case today) and the other when Kibana has no route for the first.
func (c *Client) ListFields(ctx context.Context, pattern string) ([]IndexField, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("an index pattern is required")
	}
	q := url.Values{}
	q.Set("pattern", pattern)
	for _, meta := range []string{"_source", "_id", "_index", "_score"} {
		q.Add("meta_fields", meta)
	}
	legacy := func() ([]byte, error) {
		return c.get(ctx, EndpointFields, "/api/index_patterns/_fields_for_wildcard", q, nil)
	}
	internal := func() ([]byte, error) {
		return c.get(ctx, EndpointFields, "/internal/data_views/_fields_for_wildcard", q, http.Header{"Elastic-Api-Version": {"1"}})
	}
	first, second := internal, legacy
	if m := c.major(); m > 0 && m < 8 {
		first, second = legacy, internal
	}
	data, err := first()
	if err != nil && routeMissing(err) {
		data, err = second()
	}
	if err != nil {
		return nil, err
	}
	return parseFieldsForWildcard(data)
}

func parseFieldsForWildcard(data []byte) ([]IndexField, error) {
	var raw struct {
		Fields json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing the fields: %w", err)
	}
	if len(raw.Fields) == 0 || string(raw.Fields) == "null" {
		return []IndexField{}, nil
	}
	var list []IndexField
	if err := json.Unmarshal(raw.Fields, &list); err != nil {
		var byName map[string]IndexField
		if err := json.Unmarshal(raw.Fields, &byName); err != nil {
			return nil, fmt.Errorf("parsing the fields: %w", err)
		}
		for name, f := range byName {
			if f.Name == "" {
				f.Name = name
			}
			list = append(list, f)
		}
	}
	if list == nil {
		list = []IndexField{}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}
