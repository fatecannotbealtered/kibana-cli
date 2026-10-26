package kibanaclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// DataView is a Kibana data view (an index-pattern saved object before 8.0): its id,
// the index pattern it searches (title), its display name and its time field.
type DataView struct {
	ID            string
	Title         string
	Name          string
	TimeFieldName string
}

// routeMissing reports Kibana's bare 404 for an API it does not have.
func routeMissing(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.RouteMissing
}

// ListDataViews returns every data view. Kibana 8.0 and later answer GET
// /api/data_views; 7.x has only the saved objects API (GET
// /api/saved_objects/_find?type=index-pattern), which 8.x deprecated. The client
// asks the API that fits the known version first and falls back to the other when
// Kibana has no route for it.
func (c *Client) ListDataViews(ctx context.Context) ([]DataView, error) {
	if c.major() >= 8 || c.major() == 0 {
		views, err := c.listDataViewsAPI(ctx)
		if err == nil || !routeMissing(err) {
			return views, err
		}
	}
	views, err := c.listIndexPatternObjects(ctx)
	if err != nil && routeMissing(err) && c.major() > 0 && c.major() < 8 {
		return c.listDataViewsAPI(ctx)
	}
	return views, err
}

func (c *Client) listDataViewsAPI(ctx context.Context) ([]DataView, error) {
	data, err := c.get(ctx, EndpointDataViews, "/api/data_views", nil, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		DataView []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Name  string `json:"name"`
		} `json:"data_view"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing /api/data_views: %w", err)
	}
	out := make([]DataView, 0, len(raw.DataView))
	for _, v := range raw.DataView {
		out = append(out, DataView{ID: v.ID, Title: v.Title, Name: v.Name})
	}
	return out, nil
}

// listIndexPatternObjects pages through the index-pattern saved objects, asking only
// for the attributes it reads (a 7.x index pattern also stores its whole field list).
func (c *Client) listIndexPatternObjects(ctx context.Context) ([]DataView, error) {
	const perPage = 100
	var out []DataView
	for page := 1; page <= 100; page++ {
		q := url.Values{}
		q.Set("type", "index-pattern")
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))
		q.Add("fields", "title")
		q.Add("fields", "timeFieldName")
		q.Add("fields", "name")
		data, err := c.get(ctx, EndpointSavedObjects, "/api/saved_objects/_find", q, nil)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Total        int `json:"total"`
			SavedObjects []struct {
				ID         string `json:"id"`
				Attributes struct {
					Title         string `json:"title"`
					Name          string `json:"name"`
					TimeFieldName string `json:"timeFieldName"`
				} `json:"attributes"`
			} `json:"saved_objects"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parsing the saved objects: %w", err)
		}
		for _, o := range raw.SavedObjects {
			out = append(out, DataView{ID: o.ID, Title: o.Attributes.Title, Name: o.Attributes.Name, TimeFieldName: o.Attributes.TimeFieldName})
		}
		if len(raw.SavedObjects) < perPage || len(out) >= raw.Total {
			break
		}
	}
	return out, nil
}

// ErrNoDataView is a data view id Kibana does not know (or this account cannot see).
var ErrNoDataView = errors.New("no such data view")

// ResolveDataView returns a data view's index pattern and time field. It reads the
// index-pattern saved object (GET /api/saved_objects/index-pattern/<id>, 7.x and
// 8.x) and, when Kibana has no such route, the data views API (GET
// /api/data_views/data_view/<id>, 8.0 and later); on 8.0 and later, and when the
// version is unknown, it asks the data views API first. A 404 about the object
// itself is ErrNoDataView.
func (c *Client) ResolveDataView(ctx context.Context, id string) (*DataView, error) {
	first, second := c.dataViewByAPI, c.dataViewBySavedObject
	if m := c.major(); m > 0 && m < 8 {
		first, second = second, first
	}
	v, err := first(ctx, id)
	if err != nil && routeMissing(err) {
		v, err = second(ctx, id)
	}
	var ae *APIError
	if err != nil && errors.As(err, &ae) && ae.StatusCode == 404 && !ae.RouteMissing {
		return nil, ErrNoDataView
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(v.Title) == "" {
		return nil, fmt.Errorf("data view %s has no index pattern (title)", id)
	}
	if v.ID == "" {
		v.ID = id
	}
	return v, nil
}

func (c *Client) dataViewBySavedObject(ctx context.Context, id string) (*DataView, error) {
	data, err := c.get(ctx, EndpointSavedObjects, "/api/saved_objects/index-pattern/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		ID         string `json:"id"`
		Attributes struct {
			Title         string `json:"title"`
			Name          string `json:"name"`
			TimeFieldName string `json:"timeFieldName"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing the index pattern: %w", err)
	}
	return &DataView{ID: raw.ID, Title: raw.Attributes.Title, Name: raw.Attributes.Name, TimeFieldName: raw.Attributes.TimeFieldName}, nil
}

func (c *Client) dataViewByAPI(ctx context.Context, id string) (*DataView, error) {
	data, err := c.get(ctx, EndpointDataViews, "/api/data_views/data_view/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		DataView struct {
			ID            string `json:"id"`
			Title         string `json:"title"`
			Name          string `json:"name"`
			TimeFieldName string `json:"timeFieldName"`
		} `json:"data_view"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing the data view: %w", err)
	}
	v := raw.DataView
	return &DataView{ID: v.ID, Title: v.Title, Name: v.Name, TimeFieldName: v.TimeFieldName}, nil
}

// SavedObject is a Kibana saved object. Title, Description and Attributes are
// external text: callers mark them untrusted.
type SavedObject struct {
	ID          string
	Type        string
	Title       string
	Description string
	UpdatedAt   string
	// Attributes is the object's attributes object as Kibana returned it.
	Attributes json.RawMessage
	References []Reference
}

// Reference is one reference of a saved object to another.
type Reference struct{ Type, ID, Name string }

type savedObjectJSON struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	UpdatedAt  string          `json:"updated_at"`
	Attributes json.RawMessage `json:"attributes"`
	References []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"references"`
}

func (o savedObjectJSON) object() SavedObject {
	var attrs struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(o.Attributes, &attrs)
	out := SavedObject{ID: o.ID, Type: o.Type, Title: attrs.Title, Description: attrs.Description, UpdatedAt: o.UpdatedAt,
		Attributes: o.Attributes, References: []Reference{}}
	for _, r := range o.References {
		out.References = append(out.References, Reference{Type: r.Type, ID: r.ID, Name: r.Name})
	}
	return out
}

// FindSavedObjectsOptions is one page of a saved objects search.
type FindSavedObjectsOptions struct {
	Type    string
	Search  string
	Page    int
	PerPage int
}

// FindSavedObjects lists saved objects of one type (GET /api/saved_objects/_find),
// filtered by title when Search is set, and returns Kibana's total.
func (c *Client) FindSavedObjects(ctx context.Context, opts FindSavedObjectsOptions) ([]SavedObject, int, error) {
	if opts.PerPage <= 0 {
		opts.PerPage = 20
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
	data, err := c.get(ctx, EndpointSavedObjects, "/api/saved_objects/_find", q, nil)
	if err != nil {
		return nil, 0, err
	}
	var raw struct {
		Total        int               `json:"total"`
		SavedObjects []savedObjectJSON `json:"saved_objects"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, 0, fmt.Errorf("parsing the saved objects: %w", err)
	}
	out := make([]SavedObject, 0, len(raw.SavedObjects))
	for _, o := range raw.SavedObjects {
		out = append(out, o.object())
	}
	return out, raw.Total, nil
}

// GetSavedObject reads one saved object (GET /api/saved_objects/<type>/<id>).
func (c *Client) GetSavedObject(ctx context.Context, objType, id string) (*SavedObject, error) {
	data, err := c.get(ctx, EndpointSavedObjects, "/api/saved_objects/"+url.PathEscape(objType)+"/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	var raw savedObjectJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing the saved object: %w", err)
	}
	o := raw.object()
	return &o, nil
}
