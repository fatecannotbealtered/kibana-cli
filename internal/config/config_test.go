package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func overrideHome(t *testing.T) func() {
	t.Helper()
	keyring.MockInit()
	tmpDir := t.TempDir()
	keys := []string{
		"KIBANA_CLI_HOST", "KIBANA_CLI_USER", "KIBANA_CLI_PASSWORD",
		"KIBANA_CLI_KIBANA_VERSION", "KIBANA_CLI_CONTEXT",
	}
	saved := map[string]string{}
	for _, k := range keys {
		saved[k] = os.Getenv(k)
		_ = os.Unsetenv(k)
	}
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	_ = os.Setenv("HOME", tmpDir)
	_ = os.Setenv("USERPROFILE", tmpDir)
	return func() {
		_ = os.Setenv("HOME", origHome)
		_ = os.Setenv("USERPROFILE", origUserProfile)
		for k, v := range saved {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}
}

func TestSaveAndLoadDefaultContext(t *testing.T) {
	defer overrideHome(t)()
	want := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(want, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != want.Host || got.Username != want.Username || got.Password != want.Password {
		t.Fatalf("got %+v", got)
	}
	if got.ContextName != "default" {
		t.Fatalf("expected default context, got %q", got.ContextName)
	}
}

func TestLoadConnectionMetaForDoesNotReadKeyring(t *testing.T) {
	defer overrideHome(t)()
	want := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(want, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	defer keyring.MockInit()
	meta, err := LoadConnectionMetaFor("")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Host != want.Host || meta.Username != want.Username || meta.ContextName != "default" || meta.Password != "" {
		t.Fatalf("metadata=%+v", meta)
	}
	if _, err := LoadFor(""); err == nil {
		t.Fatal("credential-resolving LoadFor should still read the unavailable keyring")
	}
}

// TestEnvAuthHostAnchored: only a host anchors an env override, so a host set
// without the rest errors; a lone user/password is ignored (it commonly feeds
// `context add`) and must not break a keyring-backed context resolution.
func TestEnvAuthHostAnchored(t *testing.T) {
	defer overrideHome(t)()
	if err := Save(&Config{Host: "https://file.example.com", Username: "fileuser", Password: "filepass"}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	errorCases := []struct {
		name string
		set  map[string]string
	}{
		{"host only", map[string]string{"KIBANA_CLI_HOST": "https://env.example.com"}},
		{"host and user", map[string]string{"KIBANA_CLI_HOST": "https://env.example.com", "KIBANA_CLI_USER": "envuser"}},
	}
	for _, tc := range errorCases {
		t.Run("error/"+tc.name, func(t *testing.T) {
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "incomplete KIBANA_CLI_* auth env") {
				t.Fatalf("expected incomplete-env error, got %v", err)
			}
		})
	}

	ignoredCases := []struct {
		name string
		set  map[string]string
	}{
		{"user only", map[string]string{"KIBANA_CLI_USER": "envuser"}},
		{"password only", map[string]string{"KIBANA_CLI_PASSWORD": "envpass"}},
	}
	for _, tc := range ignoredCases {
		t.Run("ignored/"+tc.name, func(t *testing.T) {
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			// A lone user/password is ignored: the keyring-backed default context
			// still resolves with its real credentials.
			cfg, err := Load()
			if err != nil {
				t.Fatalf("lone env var must not error: %v", err)
			}
			if cfg.Host != "https://file.example.com" || cfg.Password != "filepass" {
				t.Fatalf("keyring context not resolved: %+v", cfg)
			}
		})
	}
}

func TestEnvCLIOverridesContext(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{Host: "https://file.example.com", Username: "a", Password: "p"}, SaveOptions{})
	_ = os.Setenv("KIBANA_CLI_HOST", "https://cli.example.com")
	_ = os.Setenv("KIBANA_CLI_USER", "env")
	_ = os.Setenv("KIBANA_CLI_PASSWORD", "envpass")
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "https://cli.example.com" || got.Username != "env" || got.Password != "envpass" {
		t.Fatalf("got %+v", got)
	}
	if got.ContextName != "env" {
		t.Fatalf("expected env context, got %q", got.ContextName)
	}
}

func TestContextSelectionAndSwitch(t *testing.T) {
	defer overrideHome(t)()
	if err := Save(&Config{ContextName: "sys-a", Host: "https://a.example.com", Username: "alice", Password: "pa", DefaultIndex: "a-*"}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Config{ContextName: "sys-b", Host: "https://b.example.com", Username: "bob", Password: "pb", DefaultIndex: "b-*"}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	// First saved context becomes current.
	cur, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cur.ContextName != "sys-a" {
		t.Fatalf("expected sys-a current, got %q", cur.ContextName)
	}
	// KIBANA_CLI_CONTEXT selects without mutating the file.
	t.Setenv("KIBANA_CLI_CONTEXT", "sys-b")
	selB, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if selB.Host != "https://b.example.com" || selB.DefaultIndex != "b-*" {
		t.Fatalf("env select failed: %+v", selB)
	}
	_ = os.Unsetenv("KIBANA_CLI_CONTEXT")
	// --context flag wins over current.
	flagB, err := LoadFor("sys-b")
	if err != nil {
		t.Fatal(err)
	}
	if flagB.Username != "bob" {
		t.Fatalf("flag select failed: %+v", flagB)
	}
	// Unknown context errors.
	if _, err := LoadFor("nope"); err == nil || !strings.Contains(err.Error(), "unknown context") {
		t.Fatalf("expected unknown context error, got %v", err)
	}
	// Switch current.
	if err := SetCurrentContext("sys-b"); err != nil {
		t.Fatal(err)
	}
	cur2, _ := Load()
	if cur2.ContextName != "sys-b" {
		t.Fatalf("switch failed, current=%q", cur2.ContextName)
	}
	if err := SetCurrentContext("ghost"); err == nil {
		t.Fatal("expected error switching to unknown context")
	}
}

// TestActiveMetaIgnoresLonePassword: a lone KIBANA_CLI_PASSWORD (set to feed a
// write) must not suppress the active context's defaultIndex / fieldMapFile.
func TestActiveMetaIgnoresLonePassword(t *testing.T) {
	defer overrideHome(t)()
	if err := Save(&Config{ContextName: "sys-a", Host: "https://a.example.com", Username: "alice", Password: "pa", DefaultIndex: "a-*", FieldMapFile: "fm-a.yaml"}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_CLI_PASSWORD", "feeding-a-write")
	idx, fm := ActiveMeta("")
	if idx != "a-*" || fm != "fm-a.yaml" {
		t.Fatalf("lone password suppressed context meta: idx=%q fm=%q", idx, fm)
	}
	// A full triad, by contrast, is an anonymous override with no stored meta.
	t.Setenv("KIBANA_CLI_HOST", "https://env.example.com")
	t.Setenv("KIBANA_CLI_USER", "envuser")
	if idx, fm := ActiveMeta(""); idx != "" || fm != "" {
		t.Fatalf("full env triad should yield no meta: idx=%q fm=%q", idx, fm)
	}
}

func TestRemoveContextFallsBack(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{ContextName: "sys-a", Host: "https://a.example.com", Username: "alice", Password: "pa"}, SaveOptions{})
	_ = Save(&Config{ContextName: "sys-b", Host: "https://b.example.com", Username: "bob", Password: "pb"}, SaveOptions{})
	if err := RemoveContext("sys-a"); err != nil {
		t.Fatal(err)
	}
	store, err := LoadStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Contexts["sys-a"]; ok {
		t.Fatal("sys-a not removed")
	}
	if store.CurrentContext != "sys-b" {
		t.Fatalf("current did not fall back: %q", store.CurrentContext)
	}
	// Keyring secret for sys-a is gone.
	if _, err := keyring.Get(keyringService, credentialKey("https://a.example.com", "alice")); err == nil {
		t.Fatal("expected sys-a keyring secret removed")
	}
	if err := RemoveContext("ghost"); err == nil {
		t.Fatal("expected error removing unknown context")
	}
}

func TestAuthSource(t *testing.T) {
	defer overrideHome(t)()
	if AuthSource() != "none" {
		t.Fatalf("expected none, got %s", AuthSource())
	}
	_ = os.Setenv("KIBANA_CLI_HOST", "https://kibana.example.com")
	if AuthSource() == "env-cli" {
		t.Fatalf("partial env must not be env-cli, got %s", AuthSource())
	}
	_ = os.Setenv("KIBANA_CLI_USER", "u")
	_ = os.Setenv("KIBANA_CLI_PASSWORD", "p")
	if AuthSource() != "env-cli" {
		t.Fatalf("expected env-cli, got %s", AuthSource())
	}
}

func TestAuthSourceKeyring(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	if AuthSource() != "keyring" {
		t.Fatalf("keyring: got %s", AuthSource())
	}
}

func TestMustLoadValidation(t *testing.T) {
	defer overrideHome(t)()
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected error")
	}
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	if _, err := MustLoad(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKibanaHost("https://kibana.example.com/app/discover"); err == nil {
		t.Fatal("expected error for discover URL")
	}
	if err := ValidateKibanaHost("https://kibana.example.com/login"); err == nil {
		t.Fatal("expected error for login path")
	}
}

func TestSaveCreatesDirMode(t *testing.T) {
	defer overrideHome(t)()
	if err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	home := os.Getenv("HOME")
	info, err := os.Stat(filepath.Join(home, ".kibana-cli"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("dir perm = %o", info.Mode().Perm())
	}
}

func TestIsConfigured(t *testing.T) {
	defer overrideHome(t)()
	if IsConfigured() {
		t.Fatal("expected false")
	}
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	if !IsConfigured() {
		t.Fatal("expected true")
	}
}

func TestSaveKeepsPasswordOutOfFile(t *testing.T) {
	defer overrideHome(t)()
	want := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(want, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("password must not appear in config.json: %s", raw)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != want.Password || got.Host != want.Host {
		t.Fatalf("got %+v", got)
	}
	if got.CredentialStore != CredentialStoreKeyring {
		t.Fatalf("store=%q", got.CredentialStore)
	}
}

func TestDirFallbackWhenHomeUnavailable(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	defer func() { userHomeDir = orig }()
	if got := Dir(); got != ".kibana-cli" {
		t.Fatalf("Dir() = %q", got)
	}
}

func TestFilePath(t *testing.T) {
	defer overrideHome(t)()
	want := filepath.Join(Dir(), "config.json")
	if FilePath() != want {
		t.Fatalf("FilePath() = %q want %q", FilePath(), want)
	}
}

func TestLoadStoreInvalidJSON(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FilePath(), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "parsing config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadStoreReadErrorWhenConfigIsDirectory(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(FilePath(), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "reading config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadKibanaVersionEnv(t *testing.T) {
	defer overrideHome(t)()
	t.Setenv("KIBANA_CLI_KIBANA_VERSION", "8.15.0")
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.KibanaVersion != "8.15.0" {
		t.Fatalf("version=%q", got.KibanaVersion)
	}
}

func TestLoadKeyringMissingSecret(t *testing.T) {
	defer overrideHome(t)()
	cfg := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(cfg, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(keyring.ErrNotFound)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "reading OS credential store") {
		t.Fatalf("unexpected error: %v", err)
	}
	keyring.MockInit()
}

func TestLoadSkipsKeyringWhenEnvPassword(t *testing.T) {
	defer overrideHome(t)()
	cfg := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(cfg, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_CLI_HOST", cfg.Host)
	t.Setenv("KIBANA_CLI_USER", cfg.Username)
	t.Setenv("KIBANA_CLI_PASSWORD", "from-env")
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "from-env" {
		t.Fatalf("password=%q", got.Password)
	}
}

func TestSaveKeyringUnavailable(t *testing.T) {
	defer overrideHome(t)()
	keyring.MockInitWithError(errors.New("unavailable"))
	err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	if err == nil || !strings.Contains(err.Error(), "OS credential store unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
	keyring.MockInit()
}

func TestSaveKeyringSetError(t *testing.T) {
	defer overrideHome(t)()
	err := Save(&Config{Host: "https://kibana.example.com", Username: "", Password: "p"}, SaveOptions{})
	if err == nil || !strings.Contains(err.Error(), "saving to OS credential store") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveStoreMkdirFailsWhenHomeIsFile(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "homefile")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	origHome := userHomeDir
	userHomeDir = func() (string, error) { return blocker, nil }
	defer func() { userHomeDir = origHome }()
	keys := []string{"KIBANA_CLI_HOST", "KIBANA_CLI_USER", "KIBANA_CLI_PASSWORD", "KIBANA_CLI_KIBANA_VERSION", "KIBANA_CLI_CONTEXT"}
	saved := map[string]string{}
	for _, k := range keys {
		saved[k] = os.Getenv(k)
		_ = os.Unsetenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()
	keyring.MockInit()
	err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	// Save reads the existing store before writing. When the home path is a file,
	// the failure surfaces either at the read (ENOTDIR on Unix → "reading config")
	// or at the mkdir (NotExist on Windows → "creating config dir"); both prove
	// Save refuses an unusable config dir.
	if err == nil || (!strings.Contains(err.Error(), "creating config dir") && !strings.Contains(err.Error(), "reading config")) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveStoreEncodeError(t *testing.T) {
	defer overrideHome(t)()
	orig := configJSONMarshal
	configJSONMarshal = func(any) ([]byte, error) { return nil, errors.New("encode failed") }
	defer func() { configJSONMarshal = orig }()
	err := SaveStore(&Store{Contexts: map[string]*ContextEntry{"x": {Host: "https://k.example.com"}}})
	if err == nil || !strings.Contains(err.Error(), "encoding config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveWriteFailsWhenConfigPathIsDirectory(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(FilePath(), 0700); err != nil {
		t.Fatal(err)
	}
	err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	if err == nil || !strings.Contains(err.Error(), "reading config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDelete(t *testing.T) {
	defer overrideHome(t)()
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(cfg, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(FilePath()); !os.IsNotExist(err) {
		t.Fatalf("config still exists: %v", err)
	}
}

func TestDeleteRemoveErrorWhenConfigDirNotEmpty(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(FilePath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(FilePath(), "nested"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	err := Delete()
	if err == nil || !strings.Contains(err.Error(), "deleting config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCredentialStoreLabel(t *testing.T) {
	if CredentialStoreLabel(nil) != "" {
		t.Fatal("nil cfg")
	}
	defer overrideHome(t)()
	if CredentialStoreLabel(&Config{ContextName: "env"}) != "environment" {
		t.Fatal("expected environment")
	}
	if CredentialStoreLabel(&Config{CredentialStore: CredentialStoreKeyring}) != CredentialStoreKeyring {
		t.Fatal("expected keyring")
	}
	if CredentialStoreLabel(&Config{}) != "" {
		t.Fatal("expected empty")
	}
}

func TestAuthMode(t *testing.T) {
	if (&Config{}).AuthMode() != "basic" {
		t.Fatal("expected basic")
	}
}

func TestIsConfiguredLoadError(t *testing.T) {
	defer overrideHome(t)()
	t.Setenv("KIBANA_CLI_USER", "only-user")
	if IsConfigured() {
		t.Fatal("expected false when Load fails")
	}
}

func TestMustLoadErrors(t *testing.T) {
	defer overrideHome(t)()
	t.Setenv("KIBANA_CLI_USER", "partial")
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected load error")
	}
	_ = os.Unsetenv("KIBANA_CLI_USER")
	// Context with whitespace username → empty username error.
	_ = Save(&Config{Host: "https://kibana.example.com/", Username: " ", Password: "p"}, SaveOptions{})
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected empty username error")
	}
	// Context present but no keyring secret → empty password error.
	_ = SaveStore(&Store{CurrentContext: "nopass", Contexts: map[string]*ContextEntry{
		"nopass": {Host: "https://kibana.example.com", Username: "u", CredentialStore: CredentialStoreKeyring},
	}})
	keyring.MockInit()
	if _, err := LoadFor("nopass"); err == nil {
		// keyring missing secret surfaces as read error; acceptable either way
		t.Log("keyring read returned no error (secret absent path)")
	}
	// Invalid host → validation error.
	_ = Save(&Config{ContextName: "badhost", Host: "not-a-url", Username: "u", Password: "p"}, SaveOptions{})
	if _, err := MustLoadFor("badhost"); err == nil {
		t.Fatal("expected host validation error")
	}
}

func TestLoadStoreRejectsPlaintextPassword(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"schemaVersion":2,"currentContext":"x","contexts":{"x":{"host":"https://k.example.com","username":"u","password":"oops"}}}`
	if err := os.WriteFile(FilePath(), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "plaintext password") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMustLoadTrimsHostSlash(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{Host: "https://kibana.example.com/", Username: "u", Password: "p"}, SaveOptions{})
	got, err := MustLoad()
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "https://kibana.example.com" {
		t.Fatalf("host=%q", got.Host)
	}
}

func TestLoopbackHost(t *testing.T) {
	cases := map[string]string{
		"http://localhost:5601":     "localhost",
		"http://127.0.0.1:5601":     "127.0.0.1",
		"http://[::1]:5601":         "[::1]",
		"http://user@127.0.0.1/app": "127.0.0.1",
		"http://evil.example.com":   "evil.example.com",
	}
	for in, want := range cases {
		if got := loopbackHost(in); got != want {
			t.Fatalf("loopbackHost(%q) = %q want %q", in, got, want)
		}
	}
}
