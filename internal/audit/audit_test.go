package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

func TestSanitizeArgs(t *testing.T) {
	got := sanitizeArgs([]string{"auth", "login", "--password", "secret", "-p=xyz"})
	for _, a := range got {
		if a == "secret" || a == "xyz" {
			t.Fatalf("secret leaked: %v", got)
		}
	}
	if !strings.Contains(strings.Join(got, " "), "login") {
		t.Fatalf("got %v", got)
	}
}

func TestLogWritesJSONL(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("USERPROFILE", tmp)
	defer func() {
		_ = os.Setenv("HOME", origHome)
		_ = os.Setenv("USERPROFILE", origUserProfile)
	}()

	Log("auth login", []string{"--password", "x"}, 0, 10)
	files, err := os.ReadDir(filepath.Join(config.Dir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	data, err := os.ReadFile(filepath.Join(config.Dir(), "audit", files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"x"`) {
		t.Fatal("password should be redacted from audit")
	}
}
