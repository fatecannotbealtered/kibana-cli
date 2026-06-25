package output

import (
	"encoding/json"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/contract"
)

// allErrorCodes enumerates every ErrorCode this tool can emit. Keep in sync with
// the const block in json.go; the conformance test asserts each is part of the
// canonical fleet contract (contract/contract.json, single-sourced from the
// ai-native-cli-spec template) with the exact exit code and retryability.
var allErrorCodes = []ErrorCode{
	ErrConfig, ErrAuth, ErrForbidden, ErrNotFound, ErrRateLimit, ErrServer,
	ErrValidation, ErrNetwork, ErrTimeout, ErrConfirmRequired, ErrConflict,
	ErrIntegrity, ErrIO, ErrInterrupted, ErrUnknown,
}

// expectedCodeSpec is the independent hardcoded expected {exit, retryable} for
// the canonical 16 core codes. This table is written by hand so the test can
// catch a wrong contract.json (which the contract-delegating assertion alone
// cannot detect). Canonical source: CLI-SPEC §6 / contract.json error_codes.core.
var expectedCodeSpec = map[ErrorCode]struct {
	exit      int
	retryable bool
}{
	ErrConfig:          {exit: 4, retryable: false},
	ErrAuth:            {exit: 4, retryable: false},
	ErrForbidden:       {exit: 4, retryable: false},
	ErrNotFound:        {exit: 3, retryable: false},
	ErrRateLimit:       {exit: 7, retryable: true},
	ErrServer:          {exit: 7, retryable: true},
	ErrValidation:      {exit: 2, retryable: false},
	ErrNetwork:         {exit: 7, retryable: true},
	ErrTimeout:         {exit: 8, retryable: true},
	ErrConfirmRequired: {exit: 5, retryable: false},
	ErrConflict:        {exit: 6, retryable: false},
	ErrIntegrity:       {exit: 1, retryable: false},
	ErrIO:              {exit: 1, retryable: false},
	ErrInterrupted:     {exit: 130, retryable: true},
	ErrUnknown:         {exit: 1, retryable: false},
}

// TestContractConformance_ErrorCodes asserts every emitted error code is in the
// canonical contract (core ∪ this tool's ext) with the exact exit + retryable.
// This is the CI-red guard against the drift the fleet audit found (misnamed
// codes, wrong exit-code mappings).
func TestContractConformance_ErrorCodes(t *testing.T) {
	for _, c := range allErrorCodes {
		spec, ok := contract.Codes[string(c)]
		if !ok {
			t.Errorf("error code %q is not in the canonical contract (core∪ext)", c)
			continue
		}
		if got := ExitCodeForErrorCode(c); got != spec.Exit {
			t.Errorf("exit drift for %q: tool=%d contract=%d", c, got, spec.Exit)
		}
		if got := RetryableForErrorCode(c); got != spec.Retryable {
			t.Errorf("retryable drift for %q: tool=%v contract=%v", c, got, spec.Retryable)
		}
	}
}

// TestContractConformance_IndependentCodeTable is an independent (hardcoded)
// check of exit and retryable for every core code. It does NOT delegate to
// contract.Codes, so it catches a wrong contract.json that the delegating
// assertion above cannot see.
func TestContractConformance_IndependentCodeTable(t *testing.T) {
	for code, want := range expectedCodeSpec {
		if got := ExitCodeForErrorCode(code); got != want.exit {
			t.Errorf("exit mismatch for %q: got %d want %d", code, got, want.exit)
		}
		if got := RetryableForErrorCode(code); got != want.retryable {
			t.Errorf("retryable mismatch for %q: got %v want %v", code, got, want.retryable)
		}
	}
	// Ensure the independent table covers all emitted codes.
	for _, c := range allErrorCodes {
		if _, ok := expectedCodeSpec[c]; !ok {
			t.Errorf("error code %q is in allErrorCodes but missing from expectedCodeSpec", c)
		}
	}
}

func TestContractConformance_SchemaVersion(t *testing.T) {
	if SchemaVersion != contract.SchemaVersion {
		t.Fatalf("schema_version drift: output=%q contract=%q", SchemaVersion, contract.SchemaVersion)
	}
}

// TestContractConformance_EnvelopeKeys asserts the success and error envelopes
// (and meta) carry only the canonical top-level keys, catching extra/renamed
// fields (e.g. a stray meta.timestamp). It also asserts:
//   - every MetaRequiredKey is present in meta (3a)
//   - the success envelope carries "data" when a non-empty payload is supplied (3b)
func TestContractConformance_EnvelopeKeys(t *testing.T) {
	checkEnvelopeKeys(t, SuccessEnvelope(map[string]any{"x": 1}, 0), contract.SuccessEnvelopeKeys, "success")
	checkEnvelopeKeys(t, FailureEnvelope(ErrValidation, "m", nil, 0), contract.ErrorEnvelopeKeys, "error")
}

func checkEnvelopeKeys(t *testing.T, env Envelope, canonical []string, label string) {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal %s envelope: %v", label, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatalf("unmarshal %s envelope: %v", label, err)
	}
	// "data"/"error" are omitempty and may be absent; flag only UNEXPECTED keys.
	for k := range top {
		if !contains(canonical, k) && k != "data" && k != "error" {
			t.Errorf("%s envelope has unexpected top-level key %q (canonical: %v)", label, k, canonical)
		}
	}
	for _, req := range []string{"ok", "schema_version", "meta"} {
		if _, ok := top[req]; !ok {
			t.Errorf("%s envelope missing required key %q", label, req)
		}
	}
	// 3b: success envelope must expose "data" when a non-empty payload is provided.
	if label == "success" {
		if _, ok := top["data"]; !ok {
			t.Errorf("success envelope missing \"data\" key (success_keys require it)")
		}
	}
	var meta map[string]json.RawMessage
	if raw, ok := top["meta"]; ok {
		_ = json.Unmarshal(raw, &meta)
	}
	allowed := append(append([]string{}, contract.MetaRequiredKeys...), contract.MetaOptionalKeys...)
	for k := range meta {
		if !contains(allowed, k) {
			t.Errorf("meta has unexpected key %q (canonical: %v)", k, allowed)
		}
	}
	// 3a: every MetaRequiredKey must be present in meta.
	for _, req := range contract.MetaRequiredKeys {
		if _, ok := meta[req]; !ok {
			t.Errorf("%s envelope meta missing required key %q", label, req)
		}
	}
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
