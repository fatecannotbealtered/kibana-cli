package fieldmap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

func overrideHome(t *testing.T) func() {
	t.Helper()
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	_ = os.Setenv("HOME", tmpDir)
	_ = os.Setenv("USERPROFILE", tmpDir)
	return func() {
		_ = os.Setenv("HOME", origHome)
		_ = os.Setenv("USERPROFILE", origUserProfile)
	}
}

func TestFilePath(t *testing.T) {
	defer overrideHome(t)()
	want := filepath.Join(config.Dir(), FileName)
	if got := FilePath(); got != want {
		t.Fatalf("FilePath() = %q want %q", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	defer overrideHome(t)()
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatalf("got %+v", m)
	}
}

func TestLoadReadError(t *testing.T) {
	defer overrideHome(t)()
	path := FilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "reading field map") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadParseError(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(filepath.Dir(FilePath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FilePath(), []byte(":\n\tbad"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "parsing field map") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadDefaultVersion(t *testing.T) {
	defer overrideHome(t)()
	if err := os.MkdirAll(filepath.Dir(FilePath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FilePath(), []byte("defaults:\n  index: logs-*\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 {
		t.Fatalf("version = %d", m.Version)
	}
}
