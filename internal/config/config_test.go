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
		"KIBANA_CLI_HOST", "KIBANA_CLI_USER", "KIBANA_CLI_PASSWORD", "KIBANA_CLI_KIBANA_VERSION",
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

func TestSaveAndLoadBasic(t *testing.T) {
	defer overrideHome(t)()
	want := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := Save(want, SaveOptions{Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != want.Host || got.Username != want.Username || got.Password != want.Password {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadRejectsPartialEnvAuth(t *testing.T) {
	defer overrideHome(t)()
	if err := Save(&Config{Host: "https://file.example.com", Username: "fileuser", Password: "filepass"}, SaveOptions{Plaintext: true}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		set  map[string]string
	}{
		{"user only", map[string]string{"KIBANA_CLI_USER": "envuser"}},
		{"host only", map[string]string{"KIBANA_CLI_HOST": "https://env.example.com"}},
		{"password only", map[string]string{"KIBANA_CLI_PASSWORD": "envpass"}},
		{"host and user", map[string]string{"KIBANA_CLI_HOST": "https://env.example.com", "KIBANA_CLI_USER": "envuser"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil {
				t.Fatal("expected error for partial auth env")
			}
			if !strings.Contains(err.Error(), "partial KIBANA_CLI_* auth env") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEnvCLIOverridesFile(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{Host: "https://file.example.com", Username: "a", Password: "p"}, SaveOptions{Plaintext: true})
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

func TestMustLoadValidation(t *testing.T) {
	defer overrideHome(t)()
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected error")
	}
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
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
	if err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{Plaintext: true}); err != nil {
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
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
	if !IsConfigured() {
		t.Fatal("expected true")
	}
}

func TestSaveAndLoadKeyring(t *testing.T) {
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

func TestLoadInvalidJSON(t *testing.T) {
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

func TestLoadReadErrorWhenConfigIsDirectory(t *testing.T) {
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

func TestSaveMkdirFailsWhenHomeIsFile(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "homefile")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	origHome := userHomeDir
	userHomeDir = func() (string, error) { return blocker, nil }
	defer func() { userHomeDir = origHome }()
	keys := []string{"KIBANA_CLI_HOST", "KIBANA_CLI_USER", "KIBANA_CLI_PASSWORD", "KIBANA_CLI_KIBANA_VERSION"}
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
	err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
	if err == nil || !strings.Contains(err.Error(), "creating config dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteConfigFileEncodeError(t *testing.T) {
	defer overrideHome(t)()
	orig := configJSONMarshal
	configJSONMarshal = func(any) ([]byte, error) { return nil, errors.New("encode failed") }
	defer func() { configJSONMarshal = orig }()
	err := writeConfigFile(&Config{Host: "https://kibana.example.com", Username: "u"})
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
	err := Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
	if err == nil || !strings.Contains(err.Error(), "writing config") {
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

func TestDeleteWithInvalidJSONStillRemoves(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FilePath(), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialStoreLabel(t *testing.T) {
	if CredentialStoreLabel(nil) != "" {
		t.Fatal("nil cfg")
	}
	defer overrideHome(t)()
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	if CredentialStoreLabel(&Config{}) != "environment" {
		t.Fatal("expected environment")
	}
	_ = os.Unsetenv("KIBANA_CLI_PASSWORD")
	if CredentialStoreLabel(&Config{CredentialStore: CredentialStoreKeyring}) != CredentialStoreKeyring {
		t.Fatal("expected keyring")
	}
	if CredentialStoreLabel(&Config{Password: "x"}) != CredentialStoreFile {
		t.Fatal("expected file")
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

func TestAuthSourceFileAndKeyring(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
	if AuthSource() != "file" {
		t.Fatalf("file: got %s", AuthSource())
	}
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: "p"}, SaveOptions{})
	if AuthSource() != "keyring" {
		t.Fatalf("keyring: got %s", AuthSource())
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
	_ = Save(&Config{Host: "https://kibana.example.com/", Username: " ", Password: "p"}, SaveOptions{Plaintext: true})
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected empty username error")
	}
	_ = Save(&Config{Host: "https://kibana.example.com", Username: "u", Password: ""}, SaveOptions{Plaintext: true})
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected empty password error")
	}
	_ = Save(&Config{Host: "not-a-url", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
	if _, err := MustLoad(); err == nil {
		t.Fatal("expected host validation error")
	}
}

func TestMustLoadTrimsHostSlash(t *testing.T) {
	defer overrideHome(t)()
	_ = Save(&Config{Host: "https://kibana.example.com/", Username: "u", Password: "p"}, SaveOptions{Plaintext: true})
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
