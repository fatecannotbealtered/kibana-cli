package discover

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The Discover app's path: Kibana 7.9 and later (8.x and 9.x included) serve
// Discover at /app/discover#/; Kibana 7.0 to 7.8 serve it inside the old Kibana app
// at /app/kibana#/discover. Both read the same _g and _a parameters, and Kibana 9
// rewrites _a's index into its dataSource itself.
const (
	PathModern = "/app/discover#/"
	PathLegacy = "/app/kibana#/discover"
)

// AppPath is the Discover path for a Kibana version (like 9.5.2 or 7.10.0). When
// the version is unknown or unreadable, savedObjectsOnly says which data view API
// Kibana answered: true when it had no data views API and the index-pattern saved
// objects answered (Kibana 7.x), false when the data views API answered (8.0 and
// later). Without a version, a 7.9 or 7.10 Kibana thus gets the legacy path.
func AppPath(version string, savedObjectsOnly bool) string {
	major, minor, ok := parseVersion(version)
	switch {
	case !ok && savedObjectsOnly:
		return PathLegacy
	case !ok:
		return PathModern
	case major < 7 || (major == 7 && minor < 9):
		return PathLegacy
	}
	return PathModern
}

// parseVersion reads major.minor from a version such as 9.5.2, v7.10 or 8.0.0-rc1.
func parseVersion(v string) (int, int, bool) {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(strings.SplitN(parts[1], "-", 2)[0])
	if err1 != nil || err2 != nil || major < 0 || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}

// LabelLimit is the longest filter label, in characters.
const LabelLimit = 120

// Label is the text Discover shows on the link's filter pill: "kibana-cli: " and
// the parts joined by ", ", cut to LabelLimit characters (the last one an ellipsis).
func Label(parts []string) string {
	s := "kibana-cli: " + strings.Join(parts, ", ")
	if utf8.RuneCountInString(s) <= LabelLimit {
		return s
	}
	r := []rune(s)
	return string(r[:LabelLimit-1]) + "…"
}

// Link is what a Discover page is to show: the documents a query matches in a
// data view over a time range.
type Link struct {
	// Host is the Kibana base URL, without a trailing slash.
	Host string
	// AppPath is PathModern or PathLegacy (AppPath picks it).
	AppPath string
	// DataView is the data view's id (the saved object id, not its title).
	DataView string
	// Since and Until are the time range as ISO 8601 times in UTC, as the CLI
	// searched it; they go into Discover's time picker (_g.time).
	Since, Until string
	// Query is the Elasticsearch query, without the time range: it goes into one
	// custom filter, so Discover matches what the CLI matched whatever the query
	// language was. Nil matches every document.
	Query any
	// Label is the filter pill's text (Label builds it).
	Label string
}

// URL is the link: <host><app path>?_g=(...)&_a=(...). _g holds the time range and
// no global filters; _a the data view, one custom filter carrying the query, and an
// empty query bar.
func (l Link) URL() (string, error) {
	if strings.TrimSpace(l.Host) == "" || strings.TrimSpace(l.DataView) == "" {
		return "", fmt.Errorf("discover: a link needs the Kibana host and a data view")
	}
	query := l.Query
	if query == nil {
		query = map[string]any{"match_all": map[string]any{}}
	}
	path := l.AppPath
	if path == "" {
		path = PathModern
	}
	g := Object{{"filters", []any{}}, {"time", Object{{"from", l.Since}, {"to", l.Until}}}}
	meta := Object{{"alias", l.Label}, {"disabled", false}, {"negate", false}, {"type", "custom"}, {"key", "query"}, {"index", l.DataView}}
	a := Object{
		{"index", l.DataView},
		{"filters", []any{Object{{"meta", meta}, {"query", query}}}},
		{"query", Object{{"language", "kuery"}, {"query", ""}}},
	}
	gs, err := Encode(g)
	if err != nil {
		return "", err
	}
	as, err := Encode(a)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(l.Host, "/") + path + "?_g=" + EscapeFragment(gs) + "&_a=" + EscapeFragment(as), nil
}

// EscapeFragment percent-encodes RISON text for a URL fragment's query: it keeps
// ASCII letters and digits and ( ) ! : , ' * ~ - _ . $ /, and encodes every other
// byte, UTF-8 bytes of non-ASCII text included. '+', '&', '=', '#', '%' and space are
// therefore encoded, so Kibana's query-string parsing reads the text back unchanged.
func EscapeFragment(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if keepInFragment(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func keepInFragment(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("()!:,'*~-_.$/", c) >= 0
}

// Parsed is a Discover link read back (Parse).
type Parsed struct {
	// Base is everything before the parameters: the host and the app path.
	Base string
	// G and A are the decoded _g and _a.
	G, A map[string]any
}

// Parse reads a link URL builds back into its decoded _g and _a, so the tests can
// check that a link holds what was searched.
func Parse(link string) (*Parsed, error) {
	i := strings.Index(link, "?_g=")
	if i < 0 {
		return nil, fmt.Errorf("discover: no _g in the link")
	}
	out := &Parsed{Base: link[:i]}
	for _, part := range strings.Split(link[i+1:], "&") {
		key, raw, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("discover: parameter %q has no value", part)
		}
		text, err := url.PathUnescape(raw)
		if err != nil {
			return nil, err
		}
		v, err := Decode(text)
		if err != nil {
			return nil, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("discover: %s is not an object", key)
		}
		switch key {
		case "_g":
			out.G = m
		case "_a":
			out.A = m
		default:
			return nil, fmt.Errorf("discover: unexpected parameter %s", key)
		}
	}
	if out.G == nil || out.A == nil {
		return nil, fmt.Errorf("discover: the link lacks _g or _a")
	}
	return out, nil
}

// FilterQuery is the query of a parsed link's single custom filter.
func (p *Parsed) FilterQuery() (any, error) {
	filters, _ := p.A["filters"].([]any)
	if len(filters) != 1 {
		return nil, fmt.Errorf("discover: %d filters, want 1", len(filters))
	}
	f, _ := filters[0].(map[string]any)
	q, ok := f["query"]
	if !ok {
		return nil, fmt.Errorf("discover: the filter has no query")
	}
	return q, nil
}
