package cmd

import (
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestContext_AddListUseRemove_Flow exercises the full multi-system lifecycle:
// add two contexts, list them, switch current, then remove one.
func TestContext_AddListUseRemove_Flow(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)

	// Add sys-a (write command → dry-run/confirm). Validates against the mock.
	out, code := runConfirmedCLI(t, []string{
		"context", "add", "sys-a",
		"--host", srv.URL, "--user", "ops", "--password", "secret",
		"--default-index", "sysA-app-*", "--json",
	})
	if code != ExitOK {
		t.Fatalf("add sys-a exit %d: %s", code, out)
	}

	// First context becomes current.
	out, code = runCLI(t, []string{"context", "current", "--json"})
	if code != ExitOK {
		t.Fatalf("current exit %d: %s", code, out)
	}
	if data := envelopeData(t, out); data["currentContext"] != "sys-a" {
		t.Fatalf("expected sys-a current: %s", out)
	}

	// Add sys-b.
	out, code = runConfirmedCLI(t, []string{
		"context", "add", "sys-b",
		"--host", srv.URL, "--user", "bob", "--password", "pw",
		"--default-index", "sysB-log-*", "--json",
	})
	if code != ExitOK {
		t.Fatalf("add sys-b exit %d: %s", code, out)
	}

	// List shows both, sys-a marked current.
	out, code = runCLI(t, []string{"context", "list", "--json"})
	if code != ExitOK {
		t.Fatalf("list exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if cnt, _ := data["count"].(float64); cnt != 2 {
		t.Fatalf("expected 2 contexts: %s", out)
	}

	// Switch to sys-b.
	out, code = runConfirmedCLI(t, []string{"context", "use", "sys-b", "--json"})
	if code != ExitOK {
		t.Fatalf("use exit %d: %s", code, out)
	}
	out, code = runCLI(t, []string{"context", "current", "--json"})
	if code != ExitOK || envelopeData(t, out)["currentContext"] != "sys-b" {
		t.Fatalf("switch failed exit %d: %s", code, out)
	}

	// Remove sys-a.
	out, code = runConfirmedCLI(t, []string{"context", "remove", "sys-a", "--json"})
	if code != ExitOK {
		t.Fatalf("remove exit %d: %s", code, out)
	}
	out, _ = runCLI(t, []string{"context", "list", "--json"})
	if strings.Contains(out, "sys-a") {
		t.Fatalf("sys-a should be gone: %s", out)
	}
}

func TestContext_List_Empty(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"context", "list", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if cnt, _ := envelopeData(t, out)["count"].(float64); cnt != 0 {
		t.Fatalf("expected 0 contexts: %s", out)
	}
}

func TestContext_List_Text(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"context", "list", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "No contexts") {
		t.Fatalf("expected empty hint: %s", out)
	}
}

func TestContext_Current_None(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"context", "current", "--json"})
	if code != ExitNotFound {
		t.Fatalf("expected ExitNotFound, got %d", code)
	}
}

func TestContext_Add_MissingFlags(t *testing.T) {
	keyring.MockInit()
	setupTestHome(t)
	_, code := runCLI(t, []string{"context", "add", "x", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestContext_Add_NoValidate(t *testing.T) {
	keyring.MockInit()
	setupTestHome(t)
	out, code := runConfirmedCLI(t, []string{
		"context", "add", "offline",
		"--host", "https://kibana.example.com", "--user", "u", "--password", "p",
		"--no-validate", "--json",
	})
	if code != ExitOK {
		t.Fatalf("add --no-validate exit %d: %s", code, out)
	}
}

func TestContext_Add_DryRun(t *testing.T) {
	keyring.MockInit()
	setupTestHome(t)
	out, code := runCLI(t, []string{
		"context", "add", "sys-a",
		"--host", "https://kibana.example.com", "--user", "u", "--password", "p",
		"--dry-run", "--json",
	})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"confirm_token"`) {
		t.Fatalf("expected confirm token: %s", out)
	}
}

func TestContext_Use_Unknown(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"context", "use", "ghost", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

func TestContext_Remove_Unknown(t *testing.T) {
	setupTestHome(t)
	_, code := runCLI(t, []string{"context", "remove", "ghost", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("expected ExitBadArgs, got %d", code)
	}
}

// TestContext_SelectByFlag verifies --context selects a stored context for reads.
func TestContext_SelectByFlag(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	if _, code := runConfirmedCLI(t, []string{
		"context", "add", "sys-a", "--host", srv.URL, "--user", "ops", "--password", "secret", "--json",
	}); code != ExitOK {
		t.Fatal("add sys-a failed")
	}
	if _, code := runConfirmedCLI(t, []string{
		"context", "add", "sys-b", "--host", srv.URL, "--user", "bob", "--password", "pw", "--json",
	}); code != ExitOK {
		t.Fatal("add sys-b failed")
	}
	// context bootstrap with --context sys-b reports the selected context.
	out, code := runCLI(t, []string{"context", "--context", "sys-b", "--json"})
	if code != ExitOK {
		t.Fatalf("context --context exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"activeContext"`) || !strings.Contains(out, "sys-b") {
		t.Fatalf("expected activeContext sys-b: %s", out)
	}
}
