package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
)

// The conformance suite (VER-001): G00-G10 on every write command, R01-R05 once. A
// sample is one valid invocation after the command path, without gate flags, that
// the fake Kibana's seed data accepts.

// conformanceSamples are the writes the suite runs. The field-map samples write
// under conformanceOutputDir, which the scope grants as outputs and roots.
func conformanceSamples() map[string][]string {
	return map[string][]string{
		"field-map init":        {"--field-map-file", filepath.Join(conformanceOutputDir, "init.yaml")},
		"field-map add-profile": {"--name", "app", "--index", "app-log-*", "--service-field", "log_app", "--message-field", "msg", "--field-map-file", filepath.Join(conformanceOutputDir, "add.yaml")},
		// The host is KIBANA_CLI_HOST (the fake); bob is the fake's second account.
		"auth login": {"--user", bobUser, "--password", bobPassword},
		// Removes alice's entry, which Reset puts back.
		"auth logout": {},
	}
}

// conformanceHarness connects the suite to kibana-cli and the fake Kibana. kb is nil
// for CheckCoverage, which only builds Apps.
func conformanceHarness(kb *fakeKibana) aicli.Harness {
	host := "http://127.0.0.1:1"
	if kb != nil {
		host = kb.URL
	}
	scope := map[string]any{"scope_id": "scp_conformance", "tool": "kibana-cli", "granted_by": "kibana-cli conformance",
		"granted_at": "2026-10-12T00:00:00Z", "expires_at": nil,
		"effects": []string{"artifact", "configure", "persist"}, "dangerous": true,
		"outputs": []string{conformanceOutputDir}, "roots": []string{conformanceOutputDir}}
	reset := func() {
		testKeyring.reset()
		// auth logout removes alice's entry: put it back.
		testKeyring.seed(credentialKey(host, testUser), testPassword)
		_ = os.Remove(filepath.Join(conformanceOutputDir, "init.yaml"))
		_ = os.Remove(filepath.Join(conformanceOutputDir, "add.yaml"))
		if kb != nil {
			kb.Reset()
		}
	}
	return aicli.Harness{
		NewApp: func(env map[string]string) *aicli.App {
			vars := baseVars(host, "")
			delete(vars, "KIBANA_CLI_STATE_DIR")
			for k, v := range env {
				vars[k] = v
			}
			return newTestApp(vars)
		},
		// Kibana never sees a write; auth login and logout write the keyring, the
		// field-map commands a local file (G04 accepts their receipts, P7). Counting
		// the keyring keeps "a refusal writes nothing" checked for auth.
		Writes: func() int {
			n := testKeyring.Writes()
			if kb != nil {
				n += kb.Writes()
			}
			return n
		},
		Reset:   reset,
		Samples: conformanceSamples(),
		Scope:   scope,
	}
}

func TestConformance(t *testing.T) {
	kb := newFakeKibana(t)
	h := conformanceHarness(kb)
	h.Reset()
	aicli.RunConformance(t, h)
}
