package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// builtinWords are aicli's built-in commands, whose maturity the library sets from
// its own tests (VER-005).
var builtinWords = map[string]bool{"reference": true, "context": true, "doctor": true, "changelog": true, "update": true,
	"operation": true, "help": true, "version": true}

var evidenceRef = regexp.MustCompile(`^(simulated|live|eval):(\S+)$`)

// TestEvidenceReferencesResolve is VER-005's check: every evidence reference of the
// tool's own commands is <kind>:<path>[#<anchor>] and resolves; verified commands
// carry a live record and stable ones live and eval; no command claims more than
// its evidence supports.
func TestEvidenceReferencesResolve(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("reference")
	cmds := listOf(r.data()["commands"])
	if len(cmds) == 0 {
		t.Fatalf("reference: %s", r.out)
	}
	for _, c := range cmds {
		m := c.(map[string]any)
		path, _ := m["path"].(string)
		if builtinWords[strings.Fields(path)[0]] {
			continue
		}
		kinds := map[string]bool{}
		for _, ev := range stringsOf(m["evidence"]) {
			parts := evidenceRef.FindStringSubmatch(ev)
			if parts == nil {
				t.Errorf("%s: evidence %q is not <kind>:<path>[#<anchor>]", path, ev)
				continue
			}
			kinds[parts[1]] = true
			file := strings.SplitN(parts[2], "#", 2)[0]
			if strings.HasPrefix(file, "https://") {
				continue
			}
			if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(file))); err != nil {
				t.Errorf("%s: evidence %q does not resolve: %v", path, ev, err)
			}
		}
		switch m["maturity"] {
		case "experimental":
			if !kinds["simulated"] {
				t.Errorf("%s: experimental without simulated evidence", path)
			}
		case "verified":
			if !kinds["live"] {
				t.Errorf("%s: verified without a live record", path)
			}
		case "stable":
			if !kinds["live"] || !kinds["eval"] {
				t.Errorf("%s: stable without live and eval records", path)
			}
		}
	}
}
