package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthLoginStoresThePasswordInThe1xEntry: the documented path (--password-file
// -): the password is checked with Kibana, stored in the keyring entry 1.x used
// (kibana-cli / <host>|basic|<user>), the host and user go to the profile in
// settings.json, and later commands sign in from there; the password never reaches
// an output or a file (SEC-021, SEC-040).
func TestAuthLoginStoresThePasswordInThe1xEntry(t *testing.T) {
	e := newTestEnv(t)
	e.vars["KIBANA_CLI_PASSWORD"], e.vars["KIBANA_CLI_HOST"], e.vars["KIBANA_CLI_USER"] = "", "", ""
	e.stdin = bobPassword + "\n"
	args := []string{"auth", "login", "--host", e.kb.URL + "/", "--user", bobUser, "--password-file", "-", "--dangerous"}
	dry := e.run(append(args, "--dry-run")...)
	if dry.exit != 0 || dry.preview()["inputs"].(map[string]any)["password"] != "***" {
		t.Fatalf("dry-run: %s", dry.out)
	}
	r := e.confirm(dry.token(), args...)
	res := r.result()
	if r.exit != 0 || r.outcome()["verification"] != "passed" || res["account"] != bobUser || res["profile"] != "default" ||
		res["host"] != e.kb.URL || res["password_source"] != "provided" {
		t.Fatalf("login: %s", r.out)
	}
	if testKeyring.raw(e.kb.URL+"|basic|"+bobUser) != bobPassword {
		t.Fatal("the password is not in the 1.x keyring entry")
	}
	var doc map[string]any
	settings, _ := os.ReadFile(res["settings_file"].(string))
	if json.Unmarshal(settings, &doc) != nil || doc["default_profile"] != "default" ||
		doc["profiles"].(map[string]any)["default"].(map[string]any)["user"] != bobUser {
		t.Fatalf("settings.json: %s", settings)
	}
	st := e.run("auth", "status")
	if st.data()["account"] != bobUser || st.data()["credentials_source"] != "keyring" || st.data()["kibana_version"] != "9.5.2" ||
		st.data()["kibana_version_source"] != "elasticsearch" ||
		stringsOf(st.data()["roles"])[0] != "logs_reader" || st.data()["profile"] != "default" {
		t.Fatalf("auth status: %s", st.out)
	}
	ctx := e.run("context")
	if ctx.data()["account"] != bobUser {
		t.Fatalf("context: %s", ctx.out)
	}
	assertSecretAbsent(t, e, bobPassword, dry, r, st, ctx)
}

// TestAuthLoginReusesA1xPassword: a host and user with a 1.x saved password sign in
// without typing it again; nothing is written to the keyring.
func TestAuthLoginReusesA1xPassword(t *testing.T) {
	e := newTestEnv(t)
	e.signInFromKeyring(testUser, testPassword)
	writes := testKeyring.Writes()
	args := []string{"auth", "login", "--dangerous"}
	dry := e.run(append(args, "--dry-run")...)
	var note string
	for _, c := range listOf(dry.preview()["changes"]) {
		if m := c.(map[string]any); m["field"] == "password" {
			note, _ = m["note"].(string)
		}
	}
	if dry.exit != 0 || !strings.Contains(note, "already stored") {
		t.Fatalf("dry-run: %s", dry.out)
	}
	r := e.confirm(dry.token(), args...)
	if r.exit != 0 || r.result()["password_source"] != "keyring_existing" || testKeyring.Writes() != writes {
		t.Fatalf("reuse: %s", r.out)
	}
	// Neither a password nor an entry: ask the user, never the agent.
	testKeyring.reset()
	r = e.run("auth", "login", "--dangerous", "--dry-run")
	if r.code() != "E_USAGE" || r.details()["parameter"] != "--password-file" || !strings.Contains(r.details()["next_action"].(string), "own terminal") {
		t.Fatalf("no password: %s", r.out)
	}
}

func TestAuthLoginChecksThePasswordFirst(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("auth", "login", "--user", bobUser, "--password", "wrong", "--dangerous", "--dry-run")
	if r.code() != "E_AUTH" || testKeyring.Writes() != 0 {
		t.Fatalf("a wrong password is not stored: %s", r.out)
	}
	if r = e.run("auth", "login", "--password", "x", "--dry-run"); r.details()["problem"] != "dangerous_flag_required" {
		t.Fatalf("login is configure, so dangerous: %s", r.out)
	}
	for _, c := range []struct {
		args []string
		code string
	}{
		{[]string{"--host", "ftp://kibana.example.com"}, "E_VALIDATION"},
		{[]string{"--host", "https://kibana.example.com/app/discover"}, "E_VALIDATION"},
		{[]string{"--user", "a|b"}, "E_VALIDATION"},
		{[]string{"--password", ""}, "E_VALIDATION"},
		{[]string{"--password", "a", "--password-file", "f"}, "E_USAGE"},
	} {
		if r = e.run(append(append([]string{"auth", "login"}, c.args...), "--dangerous", "--dry-run")...); r.code() != c.code {
			t.Errorf("%v: want %s, got %s", c.args, c.code, r.out)
		}
	}
	e.vars["KIBANA_CLI_HOST"] = ""
	if r = e.run("auth", "login", "--password", "x", "--dangerous", "--dry-run"); r.code() != "E_USAGE" || r.details()["parameter"] != "--host" {
		t.Fatalf("no host: %s", r.out)
	}
}

// TestAuthLoginProfiles: login writes the profile it is asked for and refuses to
// guess between several (CLI-101); each profile signs in with its own host and user.
func TestAuthLoginProfiles(t *testing.T) {
	e := newTestEnv(t)
	e.writeSettings(`{"profiles": {"prod": {"host": "` + e.kb.URL + `", "user": "alice"}, "test": {"host": "https://kibana-test.example.com", "user": "alice"}}}`)
	e.vars["KIBANA_CLI_HOST"], e.vars["KIBANA_CLI_USER"], e.vars["KIBANA_CLI_PASSWORD"] = "", "", ""
	if r := e.run("auth", "login", "--password", testPassword, "--dangerous", "--dry-run"); r.code() != "E_USAGE" || r.details()["parameter"] != "--profile" {
		t.Fatalf("several profiles and none active: %s", r.out)
	}
	args := []string{"auth", "login", "--profile", "prod", "--password", testPassword, "--dangerous"}
	if r := e.confirm(e.dryRun(args...), args...); r.exit != 0 || r.result()["profile"] != "prod" {
		t.Fatalf("login to prod: %s", r.out)
	}
	if r := e.run("log", "search", "--index", "app-log-*", "--profile", "prod"); r.exit != 0 {
		t.Fatalf("prod signs in from the keyring: %s", r.out)
	}
	if r := e.run("log", "search", "--index", "app-log-*", "--profile", "test"); r.code() != "E_AUTH" {
		t.Fatalf("test has no password: %s", r.out)
	}
	sent := len(e.kb.Requests())
	if r := e.run("log", "search", "--index", "app-log-*", "--profile", "nope"); r.code() != "E_CONFIG" || len(e.kb.Requests()) != sent {
		t.Fatalf("an unknown profile: %s", r.out)
	}
	if r := e.run("log", "search", "--index", "app-log-*"); r.code() != "E_USAGE" || r.details()["parameter"] != "--profile" {
		t.Fatalf("no active profile: %s", r.out)
	}
	// A new profile is created by its first login.
	args = []string{"auth", "login", "--profile", "lab", "--host", e.kb.URL, "--user", bobUser, "--password", bobPassword, "--dangerous"}
	if r := e.confirm(e.dryRun(args...), args...); r.exit != 0 {
		t.Fatalf("login creating lab: %s", r.out)
	}
	if r := e.run("auth", "status", "--profile", "lab"); r.data()["account"] != bobUser || r.data()["profile"] != "lab" {
		t.Fatalf("lab signs in: %s", r.out)
	}
	if r := e.run("auth", "login", "--profile", "Bad Name", "--password", "x", "--dangerous", "--dry-run"); r.code() != "E_USAGE" && r.code() != "E_CONFIG" {
		t.Fatalf("bad profile name: %s", r.out)
	}
}

// TestAuthLoginApplyFailure: the keyring goes first, so a refused keyring leaves
// settings.json alone, and a settings.json that cannot be written puts the keyring
// entry back; there is no upstream write to refuse or lose.
func TestAuthLoginApplyFailure(t *testing.T) {
	t.Run("keyring refuses", func(t *testing.T) {
		e := newTestEnv(t)
		args := []string{"auth", "login", "--user", bobUser, "--password", bobPassword, "--dangerous"}
		tok := e.dryRun(args...)
		testKeyring.failSet = errors.New("no secret service")
		r := e.confirm(tok, args...)
		if r.code() != "E_IO" || r.details()["dispatch_state"] != "not_started" || r.outcome()["change"] != "not_applied" {
			t.Fatalf("a refused keyring: %s", r.out)
		}
		if _, err := os.Stat(filepath.Join(e.stateDir(), "settings.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("settings.json must not be written: %v", err)
		}
	})
	t.Run("settings not written", func(t *testing.T) {
		e := newTestEnv(t)
		e.vars["KIBANA_CLI_SETTINGS_FILE"] = filepath.Join(e.dir, "missing", "settings.json")
		testKeyring.seed(credentialKey(e.kb.URL, bobUser), "old-password")
		args := []string{"auth", "login", "--user", bobUser, "--password", bobPassword, "--dangerous"}
		r := e.confirm(e.dryRun(args...), args...)
		if r.code() != "E_IO" || r.details()["dispatch_state"] != "not_started" || testKeyring.raw(credentialKey(e.kb.URL, bobUser)) != "old-password" {
			t.Fatalf("an unwritable settings.json: %s", r.out)
		}
	})
}

func TestAuthStatus(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("auth", "status")
	d := r.data()
	if r.exit != 0 || d["account"] != testUser || d["credentials_source"] != "env" || d["name"] != "Alice" || d["profile"] != nil {
		t.Fatalf("auth status: %s", r.out)
	}
	e.vars["KIBANA_CLI_PASSWORD"] = "wrong"
	if r = e.run("auth", "status"); r.code() != "E_AUTH" {
		t.Fatalf("wrong password: %s", r.out)
	}
	e.vars["KIBANA_CLI_HOST"] = ""
	if r = e.run("auth", "status"); r.code() != "E_CONFIG" || !strings.Contains(r.details()["next_action"].(string), "auth login") {
		t.Fatalf("not configured: %s", r.out)
	}
}

func TestAuthLogout(t *testing.T) {
	e := newTestEnv(t)
	e.signInFromKeyring(testUser, testPassword)
	dry := e.run("auth", "logout", "--dangerous", "--dry-run")
	if ch := listOf(dry.preview()["changes"])[0].(map[string]any); ch["field"] != "password_stored" || ch["before"] != true {
		t.Fatalf("preview: %s", dry.out)
	}
	r := e.confirm(dry.token(), "auth", "logout", "--dangerous")
	if r.exit != 0 || r.outcome()["verification"] != "passed" || r.receipt()["observed"].(map[string]any)["password_stored"] != false {
		t.Fatalf("logout: %s", r.out)
	}
	if testKeyring.raw(credentialKey(e.kb.URL, testUser)) != "" || len(e.kb.Requests()) != 0 {
		t.Fatal("the entry is still there, or Kibana was contacted")
	}
	// Nothing stored: not found, and the env password named when it is the one in use.
	e.vars["KIBANA_CLI_PASSWORD"] = testPassword
	if r = e.run("auth", "logout", "--dangerous", "--dry-run"); r.code() != "E_NOT_FOUND" || !strings.Contains(r.details()["next_action"].(string), "KIBANA_CLI_PASSWORD") {
		t.Fatalf("nothing stored: %s", r.out)
	}
	if r = e.run("auth", "logout", "--dry-run"); r.details()["problem"] != "dangerous_flag_required" {
		t.Fatalf("logout is configure, so dangerous: %s", r.out)
	}
	// A 1.x entry with no profile: --host and --user name it.
	testKeyring.seed(credentialKey("https://old.example.com", "carol"), "pw")
	e.vars["KIBANA_CLI_HOST"], e.vars["KIBANA_CLI_USER"] = "", ""
	args := []string{"auth", "logout", "--host", "https://old.example.com", "--user", "carol", "--dangerous"}
	if r = e.confirm(e.dryRun(args...), args...); r.exit != 0 || testKeyring.raw(credentialKey("https://old.example.com", "carol")) != "" {
		t.Fatalf("logout of a 1.x entry: %s", r.out)
	}
	if r = e.run("auth", "logout", "--dangerous", "--dry-run"); r.code() != "E_USAGE" || r.details()["parameter"] != "--host" {
		t.Fatalf("nothing configured: %s", r.out)
	}
	t.Run("keyring refuses", func(t *testing.T) {
		e := newTestEnv(t)
		e.signInFromKeyring(testUser, testPassword)
		tok := e.dryRun("auth", "logout", "--dangerous")
		testKeyring.failSet = errors.New("locked")
		if r := e.confirm(tok, "auth", "logout", "--dangerous"); r.code() != "E_IO" || r.outcome()["change"] != "not_applied" {
			t.Fatalf("a locked keyring: %s", r.out)
		}
	})
}
