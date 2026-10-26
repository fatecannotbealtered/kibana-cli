package cmd

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
)

// coverageFile records every command the tests execute (VER-003): each App the
// tests build gets KIBANA_CLI_COVERAGE_FILE, and TestMain runs aicli.CheckCoverage
// (R06) on it after a full run.
var coverageFile string

// releaseAPI stands in for GitHub's release API: doctor refreshes the update notice
// from it, so no test touches the network.
var releaseAPI = httptest.NewServer(http.NotFoundHandler())

// testKeyring is the credential store of every test (keyringBackend): no test
// touches the OS keyring, which holds real passwords.
var testKeyring = &memKeyring{}

// conformanceOutputDir is where the field-map conformance samples write their files
// (Harness.Scope outputs and roots); TestMain creates and removes it.
var conformanceOutputDir = filepath.Join(os.TempDir(), fmt.Sprintf("kibana-cli-conformance-%d", os.Getpid()))

func TestMain(m *testing.M) {
	keyring.MockInit() // belt and braces: nothing reaches the OS keyring even through go-keyring
	keyringBackend = testKeyring
	dir, err := os.MkdirTemp("", "kibana-cli-coverage-")
	if err != nil {
		panic(err)
	}
	coverageFile = filepath.Join(dir, "coverage.jsonl")
	_ = os.MkdirAll(conformanceOutputDir, 0o700)
	code := m.Run()
	if code == 0 && fullRun() {
		tb := &mainTB{}
		h := conformanceHarness(nil)
		h.CoverageFile = coverageFile
		aicli.CheckCoverage(tb, h)
		if tb.failed {
			code = 1
		}
	}
	releaseAPI.Close()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(conformanceOutputDir)
	os.Exit(code)
}

// fullRun is false when -run or -skip selects a subset of the tests; R06 needs every
// test to have run.
func fullRun() bool {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return true
}

// mainTB reports R06 failures from TestMain.
type mainTB struct{ failed bool }

func (m *mainTB) Helper() {}
func (m *mainTB) Errorf(format string, args ...any) {
	m.failed = true
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
}
func (m *mainTB) Fatalf(format string, args ...any) { m.Errorf(format, args...) }
func (m *mainTB) Logf(string, ...any)               {}

// memKeyring is an in-memory credential store that counts its writes and can be
// told to fail.
type memKeyring struct {
	mu       sync.Mutex
	entries  map[string]string
	writes   int
	failSet  error // Set and Delete fail with it when set
	failRead error // Get fails with it when set
}

func (k *memKeyring) key(service, user string) string { return service + "\x00" + user }

func (k *memKeyring) Get(service, user string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failRead != nil {
		return "", k.failRead
	}
	v, ok := k.entries[k.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (k *memKeyring) Set(service, user, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failSet != nil {
		return k.failSet
	}
	if k.entries == nil {
		k.entries = map[string]string{}
	}
	k.entries[k.key(service, user)] = secret
	k.writes++
	return nil
}

func (k *memKeyring) Delete(service, user string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failSet != nil {
		return k.failSet
	}
	if _, ok := k.entries[k.key(service, user)]; !ok {
		return keyring.ErrNotFound
	}
	delete(k.entries, k.key(service, user))
	k.writes++
	return nil
}

// Writes counts the keyring writes (Harness.Writes adds them to Kibana's).
func (k *memKeyring) Writes() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.writes
}

// reset empties the store and clears the failures.
func (k *memKeyring) reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.entries, k.writes, k.failSet, k.failRead = map[string]string{}, 0, nil, nil
}

// seed stores an entry without counting it as a write (test setup).
func (k *memKeyring) seed(account, secret string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.entries == nil {
		k.entries = map[string]string{}
	}
	k.entries[k.key(keyringService, account)] = secret
}

// raw returns an entry's stored value ("" when absent).
func (k *memKeyring) raw(account string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.entries[k.key(keyringService, account)]
}
