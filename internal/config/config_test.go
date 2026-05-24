package config

import (
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
