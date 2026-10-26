package discover

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestLinkURLGolden: the whole URL for one query, character by character.
func TestLinkURLGolden(t *testing.T) {
	l := Link{Host: "https://kibana.example.com", AppPath: PathModern, DataView: "dv-app",
		Since: "2026-10-25T07:10:00Z", Until: "2026-10-25T08:10:00Z",
		Query: map[string]any{"bool": map[string]any{"filter": []map[string]any{{"term": map[string]any{"level": "ERROR"}}}}},
		Label: Label([]string{"level ERROR"})}
	got, err := l.URL()
	want := "https://kibana.example.com/app/discover#/?_g=(filters:!(),time:(from:'2026-10-25T07:10:00Z',to:'2026-10-25T08:10:00Z'))" +
		"&_a=(index:dv-app,filters:!((meta:(alias:'kibana-cli:%20level%20ERROR',disabled:!f,negate:!f,type:custom,key:query,index:dv-app)," +
		"query:(bool:(filter:!((term:(level:ERROR)))))))," +
		"query:(language:kuery,query:''))"
	if err != nil || got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// A data view id that is a UUID starts with a digit: quoted.
	l.DataView, l.AppPath, l.Query = "17f4cc60-8c5a-11ee-a1b2-0242ac120002", PathLegacy, nil
	got, _ = l.URL()
	if !strings.HasPrefix(got, "https://kibana.example.com/app/kibana#/discover?_g=") ||
		!strings.Contains(got, "_a=(index:'17f4cc60-8c5a-11ee-a1b2-0242ac120002',") || !strings.Contains(got, "query:(match_all:())") {
		t.Fatalf("legacy link: %s", got)
	}
}

// TestLinkURLRoundTrip: Parse reads back the time range, the data view and the
// query exactly, with quotes, '!', Chinese text and a nested bool in the query.
func TestLinkURLRoundTrip(t *testing.T) {
	query := map[string]any{"bool": map[string]any{
		"must": []any{
			map[string]any{"query_string": map[string]any{"query": `msg:"can't pay!" AND 支付超时 & x=1 + y #z %20`, "default_field": "*", "lenient": true}},
			map[string]any{"bool": map[string]any{"should": []any{map[string]any{"match_phrase": map[string]any{"msg": "[4bf92f35] timeout"}}}, "minimum_should_match": 1.0}},
		},
		"filter": []any{},
	}}
	l := Link{Host: "http://127.0.0.1:5601/", DataView: "dv 1!", Since: "2026-10-25T07:10:00Z", Until: "2026-10-25T08:10:00Z",
		Query: query, Label: Label([]string{`query "支付超时"`})}
	link, err := l.URL()
	if err != nil {
		t.Fatal(err)
	}
	params := strings.Replace(strings.Replace(link[strings.Index(link, "#")+1:], "?_g=", "", 1), "&_a=", "", 1)
	if strings.ContainsAny(params, " \"&+=#?") {
		t.Fatalf("unescaped characters: %s", link)
	}
	p, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse(%s): %v", link, err)
	}
	if p.Base != "http://127.0.0.1:5601/app/discover#/" {
		t.Fatalf("base: %s", p.Base)
	}
	if !reflect.DeepEqual(p.G, map[string]any{"filters": []any{}, "time": map[string]any{"from": "2026-10-25T07:10:00Z", "to": "2026-10-25T08:10:00Z"}}) {
		t.Fatalf("_g: %#v", p.G)
	}
	if p.A["index"] != "dv 1!" || !reflect.DeepEqual(p.A["query"], map[string]any{"language": "kuery", "query": ""}) {
		t.Fatalf("_a: %#v", p.A)
	}
	got, err := p.FilterQuery()
	if err != nil || !reflect.DeepEqual(got, query) {
		t.Fatalf("filter query: %#v %v", got, err)
	}
	meta := p.A["filters"].([]any)[0].(map[string]any)["meta"].(map[string]any)
	if meta["alias"] != `kibana-cli: query "支付超时"` || meta["index"] != "dv 1!" || meta["type"] != "custom" || meta["disabled"] != false || meta["negate"] != false {
		t.Fatalf("meta: %#v", meta)
	}
	if _, err := (Link{Host: "", DataView: "x"}).URL(); err == nil {
		t.Fatal("a link without a host")
	}
}

func TestAppPath(t *testing.T) {
	cases := []struct {
		version   string
		savedOnly bool
		want      string
	}{
		{"9.5.2", false, PathModern},
		{"8.0.0", true, PathModern},
		{"8.19.3", false, PathModern},
		{"7.17.0", true, PathModern},
		{"7.10.0", true, PathModern},
		{"7.9.0", true, PathModern},
		{"7.8.1", true, PathLegacy},
		{"7.7.1", false, PathLegacy},
		{"7.0.0", true, PathLegacy},
		{"6.8.0", true, PathLegacy},
		{"v7.10", true, PathModern},
		{"8.0.0-rc1", false, PathModern},
		{"", false, PathModern},
		{"", true, PathLegacy},
		{"unknown", true, PathLegacy},
		{"9", false, PathModern},
	}
	for _, c := range cases {
		if got := AppPath(c.version, c.savedOnly); got != c.want {
			t.Errorf("AppPath(%q, %v) = %s, want %s", c.version, c.savedOnly, got, c.want)
		}
	}
}

func TestLabel(t *testing.T) {
	if got := Label([]string{"service order-svc", "level ERROR"}); got != "kibana-cli: service order-svc, level ERROR" {
		t.Fatalf("label: %s", got)
	}
	long := Label([]string{strings.Repeat("超", 200)})
	if utf8.RuneCountInString(long) != LabelLimit || !strings.HasSuffix(long, "…") || !strings.HasPrefix(long, "kibana-cli: 超") {
		t.Fatalf("long label: %d %s", utf8.RuneCountInString(long), long)
	}
	exact := Label([]string{strings.Repeat("x", LabelLimit-len("kibana-cli: "))})
	if utf8.RuneCountInString(exact) != LabelLimit || strings.HasSuffix(exact, "…") {
		t.Fatalf("a label of exactly the limit is kept: %s", exact)
	}
}

func TestParseRefusesOtherLinks(t *testing.T) {
	for _, s := range []string{"https://k/app/discover#/", "https://k/app/discover#/?_g=(a:1)", "https://k/app/discover#/?_g=(a:1)&_a=!(1)",
		"https://k/app/discover#/?_g=(a:1)&_x=(b:1)", "https://k/app/discover#/?_g=(a:1)&_a"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted it", s)
		}
	}
}
