package discover

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestEncodeGolden: the encodings of rison.js's own examples and of what a Discover
// link carries.
func TestEncodeGolden(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "!n"},
		{true, "!t"},
		{false, "!f"},
		{"", "''"},
		{"a", "a"},
		{"now-15m", "now-15m"},
		{"a-z.b_c/d~e", "a-z.b_c/d~e"},
		{"-h", "'-h'"},
		{"23skidoo", "'23skidoo'"},
		{"wow!", "'wow!!'"},
		{"can't", "'can!'t'"},
		{"don't panic!", "'don!'t panic!!'"},
		{"a b", "'a b'"},
		{"@timestamp", "'@timestamp'"},
		{"$money", "'$money'"},
		{"x:y", "'x:y'"},
		{"f(x)", "'f(x)'"},
		{"*", "'*'"},
		{"line\nbreak", "'line\nbreak'"},
		{`say "hi"`, `'say "hi"'`},
		{"超时", "超时"},
		{"支付 超时", "'支付 超时'"},
		{"2026-10-25T07:10:00Z", "'2026-10-25T07:10:00Z'"},
		{0.0, "0"},
		{504.0, "504"},
		{-3, "-3"},
		{1.5, "1.5"},
		{1e21, "1e21"},
		{2.5e-7, "2.5e-07"},
		{json.Number("1E+5"), "1e5"},
		{json.Number("12"), "12"},
		{[]any{}, "!()"},
		{[]any{1, "a", true, nil}, "!(1,a,!t,!n)"},
		{[]string{"a b", "c"}, "!('a b',c)"},
		{map[string]any{}, "()"},
		{map[string]any{"b": "foo", "a": 0, "c": "23skidoo"}, "(a:0,b:foo,c:'23skidoo')"},
		{map[string]any{"match_all": map[string]any{}}, "(match_all:())"},
		{Object{{"z", 1}, {"a", 2}}, "(z:1,a:2)"},
		{map[string]any{"log.level": "ERROR", "@timestamp": "x"}, "('@timestamp':x,log.level:ERROR)"},
		{struct {
			A int    `json:"a"`
			B string `json:"b"`
		}{1, "x y"}, "(a:1,b:'x y')"},
	}
	for _, c := range cases {
		got, err := Encode(c.in)
		if err != nil || got != c.want {
			t.Errorf("Encode(%#v) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// TestEncodeNestedBool: a bool query as buildQuery makes it, with a should list of
// query_string clauses, keeps its structure.
func TestEncodeNestedBool(t *testing.T) {
	q := map[string]any{"bool": map[string]any{
		"must": []map[string]any{
			{"bool": map[string]any{"minimum_should_match": 1, "should": []map[string]any{
				{"query_string": map[string]any{"query": `service.name:"order-svc"`, "lenient": true}},
			}}},
		},
		"filter": []map[string]any{{"term": map[string]any{"http.status": "504"}}},
	}}
	got, err := Encode(q)
	want := `(bool:(filter:!((term:(http.status:'504'))),must:!((bool:(minimum_should_match:1,should:!((query_string:(lenient:!t,query:'service.name:"order-svc"'))))))))`
	if err != nil || got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestEncodeRefusesNonFiniteNumbers(t *testing.T) {
	inf := 1.0
	for i := 0; i < 2000; i++ {
		inf *= 10
	}
	if _, err := Encode(map[string]any{"x": inf}); err == nil {
		t.Fatal("an infinite number was encoded")
	}
	if _, err := Encode(json.Number("1e")); err == nil {
		t.Fatal("a malformed number was encoded")
	}
}

// TestDecodeRoundTrip: what Encode writes, Decode reads back unchanged (numbers as
// float64, as encoding/json reads them).
func TestDecodeRoundTrip(t *testing.T) {
	raw := `{"bool":{"must":[{"query_string":{"query":"timeout AND \"can't\" !important (x:y)","default_field":"*","lenient":true}},
		{"match_phrase":{"msg":"支付超时 [4bf92f35]"}}],"filter":[{"term":{"level":"ERROR"}},{"range":{"n":{"gte":-1.5,"lte":2e10}}}],
		"should":[],"minimum_should_match":0},"empty":{},"null":null,"t":true,"f":false,"bang":"!!'''","id-like":"abc",
		"digits":"0123","dash":"-x","space":" ","e":"","":"empty key"}`
	var want any
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	enc, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode(%s): %v", enc, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n%s\ngot  %#v\nwant %#v", enc, got, want)
	}
}

func TestDecodeRefusesBadText(t *testing.T) {
	for _, s := range []string{"", "(a:1", "!(1,2", "'open", "'bad!x'", "!x", "(a 1)", "(a:1)x", "(:1)", "-", "!"} {
		if v, err := Decode(s); err == nil {
			t.Errorf("Decode(%q) = %#v, want an error", s, v)
		}
	}
	if v, err := Decode("(a:!(1,'b c',!n),d:())"); err != nil || !reflect.DeepEqual(v, map[string]any{"a": []any{1.0, "b c", nil}, "d": map[string]any{}}) {
		t.Fatalf("Decode: %#v %v", v, err)
	}
}

func TestEscapeFragment(t *testing.T) {
	cases := map[string]string{
		"(time:(from:'2026-10-25T07:10:00Z'))": "(time:(from:'2026-10-25T07:10:00Z'))",
		"!()*~-_.$/,":                          "!()*~-_.$/,",
		`'a b"c'`:                              "'a%20b%22c'",
		"%#&+?=[]{}\\":                         "%25%23%26%2B%3F%3D%5B%5D%7B%7D%5C",
		"@<>|^`;":                              "%40%3C%3E%7C%5E%60%3B",
		"tab\tnl\n":                            "tab%09nl%0A",
		"超时":                                   "%E8%B6%85%E6%97%B6",
		"kibana-cli: service order-svc, level E…": "kibana-cli:%20service%20order-svc,%20level%20E%E2%80%A6",
	}
	for in, want := range cases {
		got := EscapeFragment(in)
		if got != want {
			t.Errorf("EscapeFragment(%q) = %q, want %q", in, got, want)
		}
		for _, c := range []byte(got) {
			if c >= 0x80 || c < 0x20 || strings.IndexByte(" \"#&+?=[]{}\\", c) >= 0 {
				t.Errorf("EscapeFragment(%q) kept %q", in, c)
			}
		}
	}
}
