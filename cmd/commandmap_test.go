package cmd

import (
	"os"
	"strings"
	"testing"
)

// TestSkillCommandMap keeps every copy of the command map equal to App.CommandMap(),
// the registry's own rendering (SKL-002; the spec's template/INSTANTIATE.md, step 8):
// the Skill and the README's Capabilities section carry it between the markers, and
// the text between them is exactly the map. Run it once with UPDATE_SKILL_MAP=1 to
// write each block; never edit one by hand. A file without the markers is skipped; a
// file with one marker and not the other, or with the end before the start, fails.
func TestSkillCommandMap(t *testing.T) {
	const start, end = "<!-- aicli:command-map:start -->\n", "<!-- aicli:command-map:end -->"
	want := newTestApp(map[string]string{}).CommandMap()
	for _, path := range []string{"../README.md", "../README_zh.md", "../skills/kibana-cli/SKILL.md"} {
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			t.Logf("%s: missing; skipped", path)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		i, j := strings.Index(text, start), strings.Index(text, end)
		if i < 0 && j < 0 {
			t.Logf("%s: no command-map markers; skipped", path)
			continue
		}
		if i < 0 || j < i {
			t.Fatalf("%s: the command-map markers are incomplete or out of order", path)
		}
		if got := text[i+len(start) : j]; got != want {
			if os.Getenv("UPDATE_SKILL_MAP") == "1" {
				if err := os.WriteFile(path, []byte(text[:i+len(start)]+want+text[j:]), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			t.Errorf("%s: command map is stale; run UPDATE_SKILL_MAP=1 go test ./cmd -run TestSkillCommandMap", path)
		}
	}
}
