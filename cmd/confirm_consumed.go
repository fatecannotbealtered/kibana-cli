package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Single-use confirm tokens: once a confirm token has been accepted to execute a
// write, its fingerprint is recorded so the SAME token cannot drive a second
// write. This gives agents safe-retry semantics — a confirmed write that times
// out cannot be blindly replayed; the retry is rejected with E_CONFLICT and the
// agent must re-run --dry-run (which reveals the now-current state). kibana's
// writes are local/auth operations with no upstream resource version, so
// single-use is the safe-retry mechanism rather than version binding. The store
// lives at ~/.kibana-cli/confirm-consumed.json (0600) and is pruned of expired
// entries on every access.
var consumedTokensMu sync.Mutex

func consumedTokensPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	return filepath.Join(home, "."+toolName, "confirm-consumed.json"), nil
}

// tokenFingerprint is a short, non-reversible id for a confirm token.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

// tokenExpiryUnix parses the expiry embedded in a ct_<unix>_<hex> token. Returns
// 0 when the token is malformed; callers fall back to a default retention.
func tokenExpiryUnix(token string) int64 {
	parts := strings.Split(token, "_")
	if len(parts) != 3 || parts[0] != "ct" {
		return 0
	}
	unix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || unix <= 0 {
		return 0
	}
	return unix
}

func loadConsumedTokens(path string, now time.Time) map[string]int64 {
	out := map[string]int64{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var stored map[string]int64
	if json.Unmarshal(data, &stored) != nil {
		return out
	}
	// Drop expired entries so the file cannot grow without bound.
	for fp, exp := range stored {
		if exp > now.Unix() {
			out[fp] = exp
		}
	}
	return out
}

// isConfirmTokenConsumed reports whether this token has already been used.
func isConfirmTokenConsumed(token string, now time.Time) bool {
	path, err := consumedTokensPath()
	if err != nil {
		return false // cannot check; do not block the operation
	}
	consumedTokensMu.Lock()
	defer consumedTokensMu.Unlock()
	tokens := loadConsumedTokens(path, now)
	_, ok := tokens[tokenFingerprint(token)]
	return ok
}

// markConfirmTokenConsumed records the token as used until its expiry. Best
// effort: a storage failure does not block the write (single-use simply cannot
// be guaranteed on that host). The expiry is parsed from the token itself so the
// entry is pruned no later than the token's own lifetime.
func markConfirmTokenConsumed(token string, now time.Time) {
	path, err := consumedTokensPath()
	if err != nil {
		return
	}
	expiresUnix := tokenExpiryUnix(token)
	if expiresUnix <= 0 {
		expiresUnix = now.Add(confirmTokenTTL).Unix()
	}
	consumedTokensMu.Lock()
	defer consumedTokensMu.Unlock()
	tokens := loadConsumedTokens(path, now)
	tokens[tokenFingerprint(token)] = expiresUnix
	data, err := json.Marshal(tokens)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
