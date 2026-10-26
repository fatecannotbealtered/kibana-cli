package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
)

// testEnv is one test's world: a fake Kibana with its seed data, a private state
// directory, a scope granting every write, an empty test keyring, and the only
// environment the App sees (vars; nothing comes from the process environment, so
// no test reads a real KIBANA_CLI_* variable, the real keyring or the home
// directory). Every run validates its envelope and checks the exit code and
// retryable against contract.json (VER-002, VER-004).
type testEnv struct {
	t     *testing.T
	kb    *fakeKibana
	dir   string
	vars  map[string]string
	stdin string
	now   time.Time
}

// testNow is the tests' clock: just after the seed documents.
var testNow = time.Date(2026, 10, 25, 8, 10, 0, 0, time.UTC)

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	testKeyring.reset()
	kb := newFakeKibana(t)
	dir := t.TempDir()
	e := &testEnv{t: t, kb: kb, dir: dir, vars: baseVars(kb.URL, filepath.Join(dir, "state")), now: testNow}
	e.setScope(fullScope(filepath.Join(dir, "state")))
	return e
}

// baseVars is the environment of a test App: the fake Kibana, alice's password in
// the environment, a short timeout, the coverage recorder.
func baseVars(host, stateDir string) map[string]string {
	return map[string]string{
		"KIBANA_CLI_HOST": host, "KIBANA_CLI_USER": testUser, "KIBANA_CLI_PASSWORD": testPassword, "KIBANA_CLI_TIMEOUT": "5s",
		"KIBANA_CLI_STATE_DIR": stateDir, "KIBANA_CLI_COVERAGE_FILE": coverageFile,
	}
}

// newTestApp is NewApp for tests: the environment from vars only, the release
// source on releaseAPI, no Skill directories.
func newTestApp(vars map[string]string) *aicli.App {
	app := NewApp(func(k string) string { return vars[k] })
	app.Update.APIBase = releaseAPI.URL
	app.SkillDirs = []string{}
	return app
}

// fullScope grants every effect the commands declare, dangerous included, on every
// target, and files under dir (SEC-010, SEC-012).
func fullScope(dir string) map[string]any {
	return map[string]any{"scope_id": "scp_tests", "tool": "kibana-cli", "granted_by": "kibana-cli tests",
		"granted_at": "2026-10-12T00:00:00Z", "expires_at": nil, "effects": []string{"artifact", "configure", "persist"}, "dangerous": true,
		"outputs": []string{dir}, "roots": []string{dir}}
}

// setScope writes the scope file; nil removes the grant.
func (e *testEnv) setScope(scope map[string]any) {
	e.t.Helper()
	if scope == nil {
		e.vars["KIBANA_CLI_SCOPE_FILE"] = filepath.Join(e.dir, "no-scope.json")
		return
	}
	b, _ := json.Marshal(scope)
	path := filepath.Join(e.dir, "scope.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.vars["KIBANA_CLI_SCOPE_FILE"] = path
}

// stateDir is the test's state directory.
func (e *testEnv) stateDir() string { return e.vars["KIBANA_CLI_STATE_DIR"] }

// writeSettings writes the settings file the App reads (<state dir>/settings.json).
func (e *testEnv) writeSettings(content string) string {
	e.t.Helper()
	path := filepath.Join(e.stateDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// writeFieldMap writes the field map at its default place (<state dir>/field-map.yaml).
func (e *testEnv) writeFieldMap(content string) string {
	e.t.Helper()
	path := filepath.Join(e.stateDir(), "field-map.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// signInFromKeyring removes the password from the environment and stores it in the
// test keyring under the 1.x key, as a 1.x login left it.
func (e *testEnv) signInFromKeyring(user, password string) {
	e.t.Helper()
	e.vars["KIBANA_CLI_PASSWORD"] = ""
	if err := writePassword(e.kb.URL, user, password); err != nil {
		e.t.Fatal(err)
	}
}

// cliResult is one run.
type cliResult struct {
	exit        int
	out, errOut string
	doc         map[string]any
}

func (r cliResult) data() map[string]any {
	d, _ := r.doc["data"].(map[string]any)
	if d == nil {
		d = map[string]any{}
	}
	return d
}

func (r cliResult) meta() map[string]any {
	m, _ := r.doc["meta"].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func (r cliResult) errObj() map[string]any {
	e, _ := r.doc["error"].(map[string]any)
	if e == nil {
		e = map[string]any{}
	}
	return e
}

func (r cliResult) details() map[string]any {
	d, _ := r.errObj()["details"].(map[string]any)
	if d == nil {
		d = map[string]any{}
	}
	return d
}

func (r cliResult) code() string {
	c, _ := r.errObj()["code"].(string)
	return c
}

func (r cliResult) message() string {
	m, _ := r.errObj()["message"].(string)
	return m
}

func (r cliResult) token() string {
	if t, ok := r.data()["confirm_token"].(string); ok {
		return t
	}
	t, _ := r.details()["confirm_token"].(string)
	return t
}

func (r cliResult) items() []map[string]any {
	var out []map[string]any
	for _, it := range listOf(r.data()["items"]) {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (r cliResult) scope() map[string]any {
	s, _ := r.data()["scope"].(map[string]any)
	if s == nil {
		s = map[string]any{}
	}
	return s
}

// notices are the types of meta.notices.
func (r cliResult) notices() []string {
	var out []string
	for _, n := range listOf(r.meta()["notices"]) {
		if m, ok := n.(map[string]any); ok {
			out = append(out, m["type"].(string))
		}
	}
	return out
}

func (r cliResult) untrusted() []string { return stringsOf(r.meta()["untrusted"]) }

// receipt is the receipt of a confirmed write, in data or in error.details.
func (r cliResult) receipt() map[string]any {
	if rc, ok := r.data()["receipt"].(map[string]any); ok {
		return rc
	}
	rc, _ := r.details()["receipt"].(map[string]any)
	if rc == nil {
		rc = map[string]any{}
	}
	return rc
}

func (r cliResult) outcome() map[string]any {
	o, _ := r.receipt()["outcome"].(map[string]any)
	if o == nil {
		o = map[string]any{}
	}
	return o
}

func (r cliResult) result() map[string]any {
	res, _ := r.data()["result"].(map[string]any)
	if res == nil {
		res = map[string]any{}
	}
	return res
}

func (r cliResult) preview() map[string]any {
	if p, ok := r.data()["preview"].(map[string]any); ok {
		return p
	}
	p, _ := r.details()["preview"].(map[string]any)
	if p == nil {
		p = map[string]any{}
	}
	return p
}

// app builds the App of the next run.
func (e *testEnv) app() *aicli.App {
	app := newTestApp(e.vars)
	now := e.now
	app.Now = func() time.Time { return now }
	return app
}

// run executes one command line and checks its output: one envelope (raw output
// is returned as it is).
func (e *testEnv) run(args ...string) cliResult {
	e.t.Helper()
	app := e.app()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr = &out, &errb
	app.Stdin = strings.NewReader(e.stdin)
	e.kb.sync()
	r := cliResult{exit: app.Execute(args), out: out.String(), errOut: errb.String()}
	e.kb.sync()
	if !strings.HasPrefix(strings.TrimSpace(r.out), "{\"ok\"") {
		return r
	}
	if err := aicli.ValidateEnvelope(out.Bytes()); err != nil {
		e.t.Errorf("%v: invalid envelope: %v\n%s", args, err, r.out)
	}
	dec := json.NewDecoder(strings.NewReader(r.out))
	dec.UseNumber()
	_ = dec.Decode(&r.doc)
	checkExitCode(e.t, args, r)
	return r
}

// dryRun runs args with --dry-run and returns the confirm token.
func (e *testEnv) dryRun(args ...string) string {
	e.t.Helper()
	r := e.run(append(append([]string{}, args...), "--dry-run")...)
	if r.exit != 0 || r.token() == "" {
		e.t.Fatalf("dry-run %v: exit %d %s", args, r.exit, r.out)
	}
	return r.token()
}

// confirm runs args with --confirm token.
func (e *testEnv) confirm(token string, args ...string) cliResult {
	e.t.Helper()
	return e.run(append(append([]string{}, args...), "--confirm", token)...)
}

// contractCodes is contract.json's error code table: code -> exit, retryable.
var contractCodes = sync.OnceValue(func() map[string]struct {
	Exit      int  `json:"exit"`
	Retryable bool `json:"retryable"`
} {
	raw, err := os.ReadFile(filepath.Join("..", "contract", "contract.json"))
	if err != nil {
		panic(err)
	}
	var c struct {
		ErrorCodes struct {
			Core map[string]struct {
				Exit      int  `json:"exit"`
				Retryable bool `json:"retryable"`
			} `json:"core"`
		} `json:"error_codes"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		panic(err)
	}
	return c.ErrorCodes.Core
})

// checkExitCode asserts that an envelope's exit code and retryable are the ones
// contract.json gives its code (CLI-010).
func checkExitCode(t *testing.T, args []string, r cliResult) {
	t.Helper()
	if ok, _ := r.doc["ok"].(bool); ok {
		if r.exit != 0 {
			t.Errorf("%v: ok envelope with exit %d", args, r.exit)
		}
		return
	}
	spec, known := contractCodes()[r.code()]
	if !known {
		t.Errorf("%v: code %q is not in contract.json", args, r.code())
		return
	}
	if r.exit != spec.Exit {
		t.Errorf("%v: %s exits %d, contract.json says %d", args, r.code(), r.exit, spec.Exit)
	}
	if retry, _ := r.errObj()["retryable"].(bool); retry != spec.Retryable {
		t.Errorf("%v: %s retryable %v, contract.json says %v", args, r.code(), retry, spec.Retryable)
	}
}

// listOf is a JSON array (nil when v is none).
func listOf(v any) []any {
	l, _ := v.([]any)
	return l
}

// stringsOf converts a JSON array to strings.
func stringsOf(x any) []string {
	var out []string
	for _, v := range listOf(x) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// num is a JSON number as an int.
func num(v any) int { return intOf(v) }

// assertSecretAbsent searches outputs and the state directory for a secret and its
// unsalted SHA-256 (SEC-021).
func assertSecretAbsent(t *testing.T, e *testEnv, secret string, runs ...cliResult) {
	t.Helper()
	sum := sha256.Sum256([]byte(secret))
	needles := []string{secret, hex.EncodeToString(sum[:])}
	for _, r := range runs {
		for _, n := range needles {
			if strings.Contains(r.out+r.errOut, n) {
				t.Fatalf("the secret (or its hash) appeared in an output: %s", r.out)
			}
		}
	}
	_ = filepath.Walk(e.stateDir(), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			for _, n := range needles {
				if strings.Contains(string(b), n) {
					t.Errorf("%s holds the secret (or its hash)", p)
				}
			}
		}
		return nil
	})
}
