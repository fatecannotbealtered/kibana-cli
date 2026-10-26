package fieldmap

import (
	"errors"
	"strings"
	"testing"
)

func TestAddProfileKeepsCommentsAndOtherProfiles(t *testing.T) {
	out, err := AddProfile([]byte(ExampleYAML), "sys-a", Profile{IndexPatterns: []string{"sys-a-*"}, MessageFields: []string{"msg"}, TraceMode: TraceModeMsg})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "# Ops: add/adjust rules when index naming changes") {
		t.Fatalf("comments lost:\n%s", text)
	}
	m, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Profiles["java-app"]; !ok {
		t.Fatal("existing profile lost")
	}
	p, ok := m.Profiles["sys-a"]
	if !ok || p.TraceMode != TraceModeMsg || p.IndexPatterns[0] != "sys-a-*" || p.TimeField != "" {
		t.Fatalf("new profile: %+v", p)
	}
	if strings.Contains(text, "time_field: \"\"") {
		t.Fatalf("empty fields are left out:\n%s", text)
	}
	if _, err := AddProfile(out, "sys-a", Profile{}); !errors.Is(err, ErrProfileExists) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestAddProfileToNoFileOrNoProfiles(t *testing.T) {
	for _, raw := range []string{"", "version: 1\n", "version: 1\nprofiles:\n"} {
		out, err := AddProfile([]byte(raw), "x", Profile{IndexPatterns: []string{"x-*"}})
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		m, err := Parse(out)
		if err != nil || m.Profiles["x"].IndexPatterns[0] != "x-*" || m.Version != 1 {
			t.Fatalf("%q: %v %s", raw, err, out)
		}
	}
	for _, raw := range []string{"- a\n- b\n", "profiles: [1]\n", "a: [\n"} {
		if _, err := AddProfile([]byte(raw), "x", Profile{}); err == nil {
			t.Errorf("%q should be refused", raw)
		}
	}
	if _, err := AddProfile(nil, " ", Profile{}); err == nil {
		t.Error("an empty name should be refused")
	}
}

func TestParseEmptyIsNil(t *testing.T) {
	if m, err := Parse([]byte("  \n")); m != nil || err != nil {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := Parse([]byte("a: [\n")); err == nil {
		t.Fatal("bad yaml")
	}
}
