// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"reflect"
)

// Outcome is the observed result of a write along separate dimensions (CLI-059).
// Empty fields get defaults from the command's effects when Apply succeeds:
// execution completed, change applied (change/configure), delivery delivered
// (deliver), persistence saved (artifact/persist), and verification computed from
// Result.Observed against the preview's changes (not_run when nothing could be
// compared). Unknown values are treated as unknown (fail closed).
type Outcome struct {
	Execution, Change, Delivery, Persistence, Verification, FailedStage string
}

// Result is what Apply observed. Observed is the read-back of the fields the write
// meant to change (from the write response or a re-read); Mismatched names fields
// whose read-back disagrees; Data is returned as data.result; Untrusted lists paths
// relative to Data that carry external content. Observed values are always
// declared untrusted.
type Result struct {
	Outcome    Outcome
	Observed   map[string]any
	Mismatched []string
	Data       any
	Untrusted  []string
}

// normalizeOutcome fills defaults for a successful Apply and replaces values outside
// the contract enums with the fail-closed value.
func normalizeOutcome(o Outcome, m *Meta) Outcome {
	changes := m.hasEffect(Change, Configure)
	delivers := m.hasEffect(Deliver)
	persists := m.hasEffect(Artifact, Persist)
	if o.Execution == "" {
		o.Execution = "completed"
	}
	if o.Change == "" {
		o.Change = pick(changes, "applied", "not_applicable")
	}
	if o.Delivery == "" {
		o.Delivery = pick(delivers, "delivered", "not_applicable")
	}
	if o.Persistence == "" {
		o.Persistence = pick(persists, "saved", "not_applicable")
	}
	return sanitizeOutcome(o)
}

// sanitizeOutcome replaces values outside the contract enums: unknown for the
// outcome dimensions, incomplete for verification.
func sanitizeOutcome(o Outcome) Outcome {
	if !contains(executionValues, o.Execution) {
		o.Execution = "unknown"
	}
	if !contains(changeValues, o.Change) {
		o.Change = "unknown"
	}
	if !contains(deliveryValues, o.Delivery) {
		o.Delivery = "unknown"
	}
	if !contains(persistenceValues, o.Persistence) {
		o.Persistence = "unknown"
	}
	if o.Verification == "" {
		o.Verification = "not_run"
	}
	if !contains(verificationValues, o.Verification) {
		o.Verification = "incomplete"
	}
	if o.Execution == "failed" || o.Execution == "interrupted" {
		if !contains(failedStageValues, o.FailedStage) {
			o.FailedStage = "apply"
		}
	} else {
		o.FailedStage = ""
	}
	return o
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// deriveReceipt applies contract.json receipt_rules in order (first match wins) and
// returns whether the write is ok and, if not, its error code. cause is the code of
// the error the apply, rollback or delivery step reported, used by rules 4 to 6.
func deriveReceipt(o Outcome, cause string) (bool, string) {
	failedExec := o.Execution == "failed" || o.Execution == "interrupted" || o.Execution == "not_started"
	lasting := o.Change == "applied" || o.Delivery == "delivered" || o.Persistence == "saved"
	switch {
	// 1. anything unknown, or still running
	case o.Execution == "unknown" || o.Change == "unknown" || o.Delivery == "unknown" || o.Persistence == "unknown" ||
		o.Execution == "running":
		return false, "E_OUTCOME_UNKNOWN"
	// 2. anything partial
	case o.Change == "partial" || o.Delivery == "partial" || o.Persistence == "partial":
		return false, "E_PARTIAL"
	// 3. execution failed but something lasting happened
	case failedExec && lasting:
		return false, "E_PARTIAL"
	// 4. execution failed without lasting effect
	case failedExec:
		if cause == "" {
			cause = "E_SERVER"
		}
		return false, cause
	// 5. the change was rolled back
	case o.Change == "rolled_back":
		if cause == "" {
			cause = "E_VERIFY_FAILED"
		}
		return false, cause
	// 6. delivery rejected
	case o.Delivery == "rejected":
		if cause == "" {
			cause = "E_FORBIDDEN"
		}
		return false, cause
	// 7. verification failed
	case o.Verification == "failed":
		return false, "E_VERIFY_FAILED"
	// 8. verification incomplete
	case o.Verification == "incomplete":
		return false, "E_VERIFY_INCOMPLETE"
	}
	// 9. otherwise
	return true, ""
}

// recordStatus maps a receipt's derived result to the operation record status: a
// change that took effect is applied even when its read-back failed or was
// incomplete (the receipt says so); a rolled-back change failed; anything else that
// left no lasting effect was not applied.
func recordStatus(ok bool, code string, o Outcome) string {
	switch {
	case ok:
		return "applied"
	case code == "E_OUTCOME_UNKNOWN":
		return "unknown"
	case code == "E_PARTIAL":
		return "partial"
	case o.Change == "applied" || o.Delivery == "delivered" || o.Persistence == "saved":
		return "applied"
	case o.Change == "rolled_back":
		return "failed"
	}
	return "not_applied"
}

// autoVerify compares the read-back with the planned changes: after must equal, add
// must be present, remove must be absent. Fields missing from observed are skipped.
func autoVerify(changes []any, observed map[string]any) (string, []string) {
	compared := 0
	var mismatched []string
	for _, raw := range changes {
		ch, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		field, _ := ch["field"].(string)
		got, present := observed[field]
		if field == "" || !present {
			continue
		}
		bad := false
		if after, ok := ch["after"]; ok {
			compared++
			if !reflect.DeepEqual(normalizeJSON(after), normalizeJSON(got)) {
				bad = true
			}
		}
		if add, ok := ch["add"]; ok {
			compared++
			for _, v := range asList(add) {
				if !listHas(got, v) {
					bad = true
				}
			}
		}
		if rem, ok := ch["remove"]; ok {
			compared++
			for _, v := range asList(rem) {
				if listHas(got, v) {
					bad = true
				}
			}
		}
		if bad {
			mismatched = append(mismatched, field)
		}
	}
	if compared == 0 {
		return "not_run", nil
	}
	if len(mismatched) > 0 {
		return "failed", mismatched
	}
	return "passed", nil
}

func normalizeJSON(v any) any {
	g, err := toGeneric(v)
	if err != nil {
		return v
	}
	return g
}

func asList(v any) []any {
	g := normalizeJSON(v)
	if list, ok := g.([]any); ok {
		return list
	}
	return []any{g}
}

func listHas(container any, v any) bool {
	for _, x := range asList(container) {
		if reflect.DeepEqual(x, normalizeJSON(v)) {
			return true
		}
	}
	return false
}
