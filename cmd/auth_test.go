package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestAuth_Help_ListsSubcommands(t *testing.T) {
	out, _ := runCLI(t, []string{"auth", "--help"})
	for _, want := range []string{"login", "logout", "status"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in auth help: %s", want, out)
		}
	}
}

func TestReadPasswordPair_ReaderFallback(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	go func() {
		_, _ = io.WriteString(w, "from-reader\n")
		_ = w.Close()
	}()
	reader := bufio.NewReader(r)
	user, pass := readPasswordPair(reader, "ops")
	if user != "ops" || pass != "from-reader" {
		t.Fatalf("user=%q pass=%q", user, pass)
	}
}

func TestAuth_Login_DryRun_JSON(t *testing.T) {
	out, code := runCLI(t, []string{
		"auth", "login",
		"--host", "https://kibana.example.com",
		"--user", "ops",
		"--password", "secret",
		"--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"confirm_token"`) || !strings.Contains(out, `"preview"`) {
		t.Fatalf("expected confirm-token dry-run json: %s", out)
	}
}

func TestAuth_Login_DryRun_Text(t *testing.T) {
	out, code := runCLI(t, []string{
		"auth", "login",
		"--host", "https://kibana.example.com",
		"--user", "ops",
		"--password", "secret",
		"--dry-run",
		"--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("expected dry-run text: %s", out)
	}
}

func TestAuth_Login_Success_JSON_Keyring(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	var payload map[string]any
	if err := json.Unmarshal([]byte(j), &payload); err != nil {
		t.Fatalf("json: %v line=%s", err, j)
	}
	data := envelopeData(t, out)
	if data["ok"] != true {
		t.Fatalf("ok=%v in %s", data["ok"], j)
	}
	if data["username"] != "agent" {
		t.Fatalf("username=%v in %s", data["username"], j)
	}
	store, _ := data["credentialStore"].(string)
	if store != "keyring" {
		t.Fatalf("credentialStore=%q in %s", store, j)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".kibana-cli", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("password must not be in config.json: %s", raw)
	}
}

func TestAuth_Login_Success_Text_Keyring(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Logged in as agent", "OS credential store", "Config saved to"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
}

func TestAuth_Login_Success_Plaintext_JSON(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--plaintext", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"credentialStore":"file"`) && !strings.Contains(j, `"credentialStore": "file"`) {
		t.Fatalf("expected file store: %s", j)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".kibana-cli", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "secret") {
		t.Fatal("plaintext login should persist password in config.json")
	}
}

func TestAuth_Login_Success_Text_PlaintextWarn(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--plaintext",
		"--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "plaintext config.json") {
		t.Fatalf("expected plaintext warning: %s", out)
	}
}

func TestAuth_Login_SearchProbeWarn_Text(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchProbeFail: true})
	defer srv.Close()
	setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--format", "text",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Log search probe failed") {
		t.Fatalf("expected search probe warning: %s", out)
	}
}

func TestAuth_Login_AuthFail(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServerWith(mockKibanaOptions{AuthFail: true})
	defer srv.Close()
	setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "bad",
		"--json",
	})
	if code != ExitAuth {
		t.Fatalf("exit %d want %d: %s", code, ExitAuth, out)
	}
}

func TestAuth_Login_Validation_InvalidHost(t *testing.T) {
	keyring.MockInit()
	setupTestHome(t)
	_, code := runCLI(t, []string{
		"auth", "login",
		"--host", "https://kibana.example.com/app/discover",
		"--user", "ops",
		"--password", "secret",
		"--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestAuth_Login_Validation_MissingCredentials(t *testing.T) {
	keyring.MockInit()
	setupTestHome(t)
	_, code := runCLIWithStdin(t, "\n\n", []string{
		"auth", "login",
		"--host", "https://kibana.example.com",
		"--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestAuth_Login_KeyringUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	defer keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--json",
	})
	if code != ExitBadArgs {
		t.Fatalf("exit %d want config error: %s", code, out)
	}
	if !strings.Contains(out, "credential store") && !strings.Contains(out, "OS credential") {
		t.Fatalf("expected keyring error: %s", out)
	}
}

func TestAuth_Login_Interactive_Success(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	stdin := srv.URL + "\nops\nsecret\n"
	out, code := runCLIWithStdin(t, stdin, []string{"auth", "login", "--format", "text"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "provide --host, --user, and --password") {
		t.Fatalf("expected non-interactive validation: %s", out)
	}
}

func TestAuth_Login_Interactive_ValidationError(t *testing.T) {
	keyring.MockInit()
	setupTestHome(t)
	stdin := "https://kibana.example.com/app/discover\n"
	_, code := runCLIWithStdin(t, stdin, []string{"auth", "login"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestAuth_Login_Interactive_PasswordStdin(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithStdin(t, "secret\n", []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--format", "text",
	})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "provide --host, --user, and --password") {
		t.Fatalf("expected non-interactive validation: %s", out)
	}
}

func TestAuth_Logout_DryRun_JSON(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"auth", "logout", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"confirm_token"`) || !strings.Contains(out, `"preview"`) {
		t.Fatalf("expected confirm-token dry-run json: %s", out)
	}
}

func TestAuth_Logout_DryRun_Text(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"auth", "logout", "--dry-run", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("expected dry-run text: %s", out)
	}
}

func TestAuth_Logout_Success_JSON(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"host":"http://x","username":"u","password":"p"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, code := runConfirmedCLI(t, []string{"auth", "logout", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"logged_out"`) {
		t.Fatalf("unexpected: %s", out)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatal("config should be removed")
	}
}

func TestAuth_Logout_Success_Text(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"host":"http://x","username":"u","password":"p"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, code := runConfirmedCLI(t, []string{"auth", "logout", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Logged out") {
		t.Fatalf("expected text logout: %s", out)
	}
}

func TestAuth_Logout_DeleteError(t *testing.T) {
	deleteConfigHook = func() error { return errors.New("delete blocked") }
	defer func() { deleteConfigHook = nil }()
	_, code := runConfirmedCLI(t, []string{"auth", "logout", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs on delete error, got %d", code)
	}
}

func TestAuth_Logout_DeleteError_UnixPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory config path delete error is unix-specific")
	}
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(cfgPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgPath, "nested"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, code := runConfirmedCLI(t, []string{"auth", "logout", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs on delete error, got %d", code)
	}
}

func TestAuth_Status_NoConfig_JSON(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	_, code := runCLI(t, []string{"auth", "status", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestAuth_Status_NoConfig_Text(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	_, code := runCLI(t, []string{"auth", "status", "--format", "text"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestAuth_Status_LoadError(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	_, code := runCLI(t, []string{"auth", "status", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestAuth_Status_Configured_JSON_Env(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"auth", "status", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"configured":true`) && !strings.Contains(j, `"configured": true`) {
		t.Fatalf("unexpected: %s", j)
	}
}

func TestAuth_Status_Configured_Text_File(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	_, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--plaintext",
		"--format", "text",
	})
	if code != ExitOK {
		t.Fatal(code)
	}
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	out, code := runCLI(t, []string{"auth", "status", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Configured") {
		t.Fatalf("expected configured status: %s", out)
	}
	_ = home
}
