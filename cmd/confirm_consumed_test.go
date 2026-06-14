package cmd

import (
	"strings"
	"testing"
)

// A confirm token may drive exactly one write; replaying it (e.g. an agent
// retrying a confirmed write that timed out) must be rejected with E_CONFLICT so
// the local/auth operation cannot be duplicated.
func TestConfirmTokenSingleUse(t *testing.T) {
	setupTestHome(t)

	args := []string{"auth", "logout"}
	token := dryRunConfirmToken(t, args)

	confirmArgs := append(append([]string{}, args...), "--confirm", token, "--json")
	if _, code := runCLI(t, confirmArgs); code != ExitOK {
		t.Fatalf("first confirm should succeed, got exit %d", code)
	}

	out, code := runCLI(t, confirmArgs)
	if code != ExitConflict {
		t.Fatalf("replayed token exit %d want %d: %s", code, ExitConflict, out)
	}
	if !strings.Contains(out, "E_CONFLICT") || !strings.Contains(out, "already used") {
		t.Fatalf("replayed token should be rejected as already-used E_CONFLICT, got: %s", out)
	}
}

func TestTokenFingerprintStable(t *testing.T) {
	a := tokenFingerprint("ct_123_abc")
	b := tokenFingerprint("ct_123_abc")
	c := tokenFingerprint("ct_123_abd")
	if a != b {
		t.Fatal("fingerprint not stable for the same token")
	}
	if a == c {
		t.Fatal("fingerprint collided for different tokens")
	}
	if len(a) != 16 {
		t.Fatalf("fingerprint length = %d, want 16", len(a))
	}
}
