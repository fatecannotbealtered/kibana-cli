// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Validators for the contract's shapes (VER-002, decision 15). They check the key
// sets, enums and cross-field rules that contract.json and the JSON Schemas in
// contract/schemas define, plus the semantic checks the schemas cannot express
// (receipt rules, batch tallies and the batch ok rule, token binding, untrusted
// paths, size budgets), so ValidateEnvelope accepts every valid example of the
// contract corpus and rejects every invalid one. The Python library's validate.py
// implements the same checks.

// validation collects problems with their paths.
type validation struct{ problems []string }

func (v *validation) add(path, format string, args ...any) {
	if path == "" {
		path = "$"
	}
	v.problems = append(v.problems, path+": "+fmt.Sprintf(format, args...))
}

func (v *validation) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(v.problems, "; "))
}

func (v *validation) failed() bool { return len(v.problems) > 0 }

// pj and pi build problem paths: data.items[2].receipt.
func pj(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func pi(base string, i int) string { return base + "[" + strconv.Itoa(i) + "]" }

var (
	snakeCase     = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
	utcTime       = regexp.MustCompile(`^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]{1,9})?Z$`)
	dateOnly      = regexp.MustCompile(`^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])$`)
	commandPathRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?: [a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$`)
	toolNameRE    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	flagNameRE    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	opIDRE        = regexp.MustCompile(`^op_[a-z2-7]{26}$`)
	batchIDRE     = regexp.MustCompile(`^opb_[a-z2-7]{26}$`)
	anyOpIDRE     = regexp.MustCompile(`^opb?_[a-z2-7]{26}$`)
	tokenRE       = regexp.MustCompile(`^ct2_[a-z2-7]{26}_[0-9a-f]{32}$`)
	sha256RE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	untrustedRE   = regexp.MustCompile(`^(?:data(?:\[\])*|error\.details)(?:\.[a-z][a-z0-9]*(?:_[a-z0-9]+)*(?:\[\])*)*$`)
	fieldPathRE   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*(?:\[\])?(?:\.[a-z][a-z0-9]*(?:_[a-z0-9]+)*(?:\[\])?)*$`)
	evidenceRE    = regexp.MustCompile(`^(?:simulated|live|eval):\S+$`)
	exitKeyRE     = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,2})$`)
)

// codeKeyedMaps are objects keyed by error or exit codes rather than field names.
var codeKeyedMaps = map[string]bool{"error_codes": true, "exit_codes": true}

// reservedRuleCodes are the codes the receipt rules give by themselves.
var reservedRuleCodes = []string{"E_OUTCOME_UNKNOWN", "E_PARTIAL", "E_VERIFY_FAILED", "E_VERIFY_INCOMPLETE"}

// ownDispatchRule are the codes whose dispatch_state has its own rule.
var ownDispatchRule = []string{"E_OUTCOME_UNKNOWN", "E_PARTIAL", "E_VERIFY_FAILED", "E_VERIFY_INCOMPLETE", "E_USAGE", "E_CONFIRMATION_REQUIRED", "E_NETWORK"}

// selfUpdate is the built-in outside the confirm gate (CLI-070); the other
// built-ins are the library's own reads.
const selfUpdate = "update"

// ---------------------------------------------------------------- primitives

// intOf returns x as an integer when it is a JSON integer.
func intOf(x any) (int64, bool) {
	switch n := x.(type) {
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			return 0, false
		}
		i, err := n.Int64()
		return i, err == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	}
	return 0, false
}

func isText(x any) bool {
	s, ok := x.(string)
	return ok && s != ""
}

func isTime(x any) bool {
	s, ok := x.(string)
	return ok && utcTime.MatchString(s)
}

func matches(re *regexp.Regexp, x any) bool {
	s, ok := x.(string)
	return ok && re.MatchString(s)
}

func isStringList(x any) bool {
	list, ok := x.([]any)
	if !ok {
		return false
	}
	for _, e := range list {
		if _, ok := e.(string); !ok {
			return false
		}
	}
	return true
}

func textList(x any) bool {
	list, ok := x.([]any)
	if !ok {
		return false
	}
	for _, e := range list {
		if !isText(e) {
			return false
		}
	}
	return true
}

func matchesAll(re *regexp.Regexp, x any) bool {
	list, ok := x.([]any)
	if !ok {
		return false
	}
	for _, e := range list {
		if !matches(re, e) {
			return false
		}
	}
	return true
}

func stringList(x any) []string {
	list, _ := x.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func distinct(list []string) bool {
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

// exactKeys requires x to be an object with every key in required and nothing
// outside required and optional.
func exactKeys(v *validation, p string, x any, required, optional []string) bool {
	obj, ok := x.(map[string]any)
	if !ok {
		v.add(p, "must be an object")
		return false
	}
	for _, k := range required {
		if !hasKey(obj, k) {
			v.add(p, "missing key %q", k)
		}
	}
	for _, k := range sortedKeys(obj) {
		if !contains(required, k) && !contains(optional, k) {
			v.add(p, "unexpected key %q", k)
		}
	}
	return true
}

func requireKeys(v *validation, p string, obj map[string]any, keys []string) {
	for _, k := range keys {
		if !hasKey(obj, k) {
			v.add(p, "missing key %q", k)
		}
	}
}

func validateEffects(v *validation, p string, x any, write bool) {
	list, ok := x.([]any)
	if !ok || len(list) == 0 {
		v.add(p, "must be a non-empty array of effects")
		return
	}
	shown := make([]string, len(list))
	for i, e := range list {
		shown[i] = fmt.Sprint(e)
	}
	if !distinct(shown) {
		v.add(p, "must not repeat an effect")
	}
	anyWrite := false
	for _, e := range list {
		name, _ := e.(string)
		if !contains(effectValues, name) {
			v.add(p, "holds an unknown effect")
		}
		if contains(writeEffects, name) {
			anyWrite = true
		}
	}
	if write && !anyWrite {
		v.add(p, "must hold a write effect")
	}
}

func dangerousRule(v *validation, p string, obj map[string]any) {
	for _, e := range stringList(obj["effects"]) {
		if contains(dangerousEffects, e) && obj["dangerous"] != true {
			v.add(pj(p, "dangerous"), "must be true for deliver and configure (CLI-025)")
			return
		}
	}
}

func validateTarget(v *validation, p string, x any) {
	if !exactKeys(v, p, x, targetKeys[:1], targetKeys[1:]) {
		return
	}
	obj := x.(map[string]any)
	if !isText(obj["key"]) {
		v.add(pj(p, "key"), "must be a non-empty string")
	}
	if k, has := obj["kind"]; has && !matches(snakeCase, k) {
		v.add(pj(p, "kind"), "must be a snake_case name")
	}
	if l, has := obj["label"]; has && !isText(l) {
		v.add(pj(p, "label"), "must be a non-empty string")
	}
}

func validateChanges(v *validation, p string, x any) {
	list, ok := x.([]any)
	if !ok {
		v.add(p, "must be an array")
		return
	}
	for i, c := range list {
		cp := pi(p, i)
		if !exactKeys(v, cp, c, changeRequiredKeys, changeOptionalKeys) {
			continue
		}
		obj := c.(map[string]any)
		if !isText(obj["field"]) {
			v.add(pj(cp, "field"), "must be a non-empty string")
		}
		if n, has := obj["note"]; has && !isText(n) {
			v.add(pj(cp, "note"), "must be a non-empty string")
		}
	}
}

func validateBlocks(v *validation, p string, x any, canApply any) {
	list, ok := x.([]any)
	if !ok {
		v.add(p, "must be an array")
		return
	}
	if canApply == false && len(list) == 0 {
		v.add(p, "must say why when can_apply is false")
	}
	if canApply == true && len(list) > 0 {
		v.add(p, "must be empty when can_apply is true")
	}
	for i, b := range list {
		bp := pi(p, i)
		if !exactKeys(v, bp, b, blockedByKeys, nil) {
			continue
		}
		obj := b.(map[string]any)
		if !matches(snakeCase, obj["kind"]) {
			v.add(pj(bp, "kind"), "must be a snake_case name")
		}
		if !isText(obj["message"]) {
			v.add(pj(bp, "message"), "must be a non-empty string")
		}
	}
}

// ---------------------------------------------------------------- receipts

// ruleCodes are the error codes a receipt rule allows: exact codes and, for "the
// cause code of the failed step", any code the other rules do not reserve.
type ruleCodes struct {
	exact []string
	cause bool
}

func (c ruleCodes) allows(code string) bool {
	return contains(c.exact, code) || (c.cause && !contains(reservedRuleCodes, code))
}

func (c ruleCodes) only(code string) bool {
	return len(c.exact) == 1 && c.exact[0] == code && !c.cause
}

func (c ruleCodes) describe() string {
	parts := append([]string{}, c.exact...)
	sort.Strings(parts)
	if c.cause {
		parts = append(parts, "the cause code of the failed step")
	}
	if len(parts) == 0 {
		return "ok"
	}
	return strings.Join(parts, " or ")
}

func lasting(o map[string]any) bool {
	return o["change"] == "applied" || o["delivery"] == "delivered" || o["persistence"] == "saved"
}

// receiptRule applies contract.json receipt_rules (first match wins) to a receipt
// that passed validateReceipt: the rule number, ok and the codes it allows.
func receiptRule(r map[string]any) (int, bool, ruleCodes) {
	o, _ := r["outcome"].(map[string]any)
	ex, ch, de, pe := o["execution"], o["change"], o["delivery"], o["persistence"]
	switch {
	case ex == "unknown" || ch == "unknown" || de == "unknown" || pe == "unknown" || ex == "running":
		return 1, false, ruleCodes{exact: []string{"E_OUTCOME_UNKNOWN"}}
	case ch == "partial" || de == "partial" || pe == "partial":
		return 2, false, ruleCodes{exact: []string{"E_PARTIAL"}}
	}
	stopped := ex == "failed" || ex == "interrupted" || ex == "not_started"
	switch {
	case stopped && lasting(o):
		return 3, false, ruleCodes{exact: []string{"E_PARTIAL"}}
	case stopped:
		return 4, false, ruleCodes{cause: true}
	case ch == "rolled_back":
		return 5, false, ruleCodes{exact: []string{"E_VERIFY_FAILED", "E_VERIFY_INCOMPLETE"}, cause: true}
	case de == "rejected":
		return 6, false, ruleCodes{cause: true}
	case o["verification"] == "failed":
		return 7, false, ruleCodes{exact: []string{"E_VERIFY_FAILED"}}
	case o["verification"] == "incomplete":
		return 8, false, ruleCodes{exact: []string{"E_VERIFY_INCOMPLETE"}}
	}
	return 9, true, ruleCodes{}
}

// validateReceipt checks a receipt (CLI-059) and reports whether its outcome can be
// ruled on.
func validateReceipt(v *validation, p string, x any) bool {
	if !exactKeys(v, p, x, receiptRequiredKeys, receiptOptionalKeys) {
		return false
	}
	r := x.(map[string]any)
	if !matches(opIDRE, r["operation_id"]) {
		v.add(pj(p, "operation_id"), "must be an operation id")
	}
	if !matches(commandPathRE, r["command"]) {
		v.add(pj(p, "command"), "must be a command path")
	}
	validateTarget(v, pj(p, "target"), r["target"])
	validateEffects(v, pj(p, "effects"), r["effects"], true)
	observed, ok := r["observed"].(map[string]any)
	if !ok {
		v.add(pj(p, "observed"), "must be an object")
		observed = map[string]any{}
	}
	if _, ok := r["replayed"].(bool); !ok {
		v.add(pj(p, "replayed"), "must be a boolean")
	}
	if !exactKeys(v, pj(p, "outcome"), r["outcome"], outcomeKeys, nil) {
		return false
	}
	out := r["outcome"].(map[string]any)
	good := true
	for _, dim := range []struct {
		key     string
		allowed []string
	}{{"execution", executionValues}, {"change", changeValues}, {"delivery", deliveryValues},
		{"persistence", persistenceValues}, {"verification", verificationValues}} {
		if s, _ := out[dim.key].(string); !contains(dim.allowed, s) {
			v.add(pj(pj(p, "outcome"), dim.key), "must be one of %s", strings.Join(dim.allowed, ", "))
			good = false
		}
	}
	if !good {
		return false
	}
	var mismatched []string
	if mm, has := r["mismatched"]; has {
		mismatched = stringList(mm)
		if !isStringList(mm) || !distinct(mismatched) {
			v.add(pj(p, "mismatched"), "must be an array of distinct strings")
			mismatched = nil
		}
	}
	fs, hasFS := r["failed_stage"]
	if hasFS {
		if s, _ := fs.(string); !contains(failedStageValues, s) {
			v.add(pj(p, "failed_stage"), "must be one of %s", strings.Join(failedStageValues, ", "))
		}
	}
	effects := stringList(r["effects"])
	has := func(names ...string) bool {
		for _, e := range effects {
			if contains(names, e) {
				return true
			}
		}
		return false
	}
	ex := out["execution"]
	if (ex == "failed" || ex == "interrupted") && !hasFS {
		v.add(p, "missing key \"failed_stage\"")
	}
	if ex == "completed" && hasFS && fs != "verify" {
		v.add(pj(p, "failed_stage"), "must be verify when execution completed")
	}
	if fs == "verify" && out["verification"] != "failed" && out["verification"] != "incomplete" {
		v.add(pj(pj(p, "outcome"), "verification"), "must be failed or incomplete when failed_stage is verify")
	}
	if fs == "deliver" && !has("deliver") {
		v.add(pj(p, "failed_stage"), "deliver needs the deliver effect")
	}
	if fs == "persist" && !has("artifact", "persist") {
		v.add(pj(p, "failed_stage"), "persist needs the artifact or persist effect")
	}
	if has("deliver") == (out["delivery"] == "not_applicable") {
		v.add(pj(pj(p, "outcome"), "delivery"), "is not_applicable exactly when the command does not deliver")
	}
	if has("artifact", "persist") == (out["persistence"] == "not_applicable") {
		v.add(pj(pj(p, "outcome"), "persistence"), "is not_applicable exactly when the command writes no local file")
	}
	if has("change", "configure") == (out["change"] == "not_applicable") {
		v.add(pj(pj(p, "outcome"), "change"), "is not_applicable exactly when the command changes nothing upstream")
	}
	switch out["verification"] {
	case "failed":
		if len(mismatched) == 0 {
			v.add(pj(p, "mismatched"), "must name the fields when verification failed")
		}
	case "passed":
		if len(mismatched) > 0 {
			v.add(pj(p, "mismatched"), "must be empty when verification passed")
		}
	}
	if (out["verification"] == "passed" || out["verification"] == "failed") && len(observed) == 0 {
		v.add(pj(p, "observed"), "must hold the read-back when verification is passed or failed (decision 15)")
	}
	for i, f := range mismatched {
		if _, ok := observed[strings.SplitN(f, ".", 2)[0]]; !ok {
			v.add(pi(pj(p, "mismatched"), i), "field %q is not in observed (CLI-059)", f)
		}
	}
	return true
}

// ---------------------------------------------------------------- previews

var previewAllKeys = func() []string {
	keys := append(append(append([]string{}, previewRequiredKeys...), previewSingleKeys...), previewBatchKeys...)
	return append(keys, previewOptionalKeys...)
}()

// validatePreview checks a preview (CLI-051, CLI-063). A batch preview cut to its
// envelope's size budget keeps its first targets and items and says in omitted how
// many targets it left out.
func validatePreview(v *validation, p string, x any) bool {
	if !exactKeys(v, p, x, previewRequiredKeys, previewAllKeys) {
		return false
	}
	pv := x.(map[string]any)
	before := len(v.problems)
	batch := hasKey(pv, "items")
	if batch {
		requireKeys(v, p, pv, previewBatchKeys)
		if hasKey(pv, "target") || hasKey(pv, "changes") {
			v.add(p, "a batch preview has targets and items, not target and changes")
		}
		if !matches(batchIDRE, pv["operation_id"]) {
			v.add(pj(p, "operation_id"), "must be a batch id")
		}
		if om, has := pv["omitted"]; has {
			if n, ok := intOf(om); !ok || n < 1 {
				v.add(pj(p, "omitted"), "must be a positive integer (absent when nothing was left out)")
			}
		}
	} else {
		requireKeys(v, p, pv, previewSingleKeys)
		if hasKey(pv, "targets") || hasKey(pv, "total") || hasKey(pv, "omitted") {
			v.add(p, "a single preview has target and changes, not targets")
		}
		if !matches(opIDRE, pv["operation_id"]) {
			v.add(pj(p, "operation_id"), "must be an operation id")
		}
	}
	if !matches(commandPathRE, pv["command"]) {
		v.add(pj(p, "command"), "must be a command path")
	}
	if !isText(pv["action"]) {
		v.add(pj(p, "action"), "must be a non-empty string")
	}
	validateEffects(v, pj(p, "effects"), pv["effects"], true)
	if _, ok := pv["dangerous"].(bool); !ok {
		v.add(pj(p, "dangerous"), "must be a boolean")
	}
	dangerousRule(v, p, pv)
	if inputs, ok := pv["inputs"].(map[string]any); !ok {
		v.add(pj(p, "inputs"), "must be an object")
	} else {
		for _, k := range sortedKeys(inputs) {
			if fi, isObj := inputs[k].(map[string]any); isObj && hasKey(fi, "sha256") {
				ip := pj(pj(p, "inputs"), k)
				exactKeys(v, ip, fi, []string{"sha256", "bytes"}, nil)
				if !matches(sha256RE, fi["sha256"]) {
					v.add(pj(ip, "sha256"), "must be 64 lowercase hex characters")
				}
				if n, ok := intOf(fi["bytes"]); !ok || n < 0 {
					v.add(pj(ip, "bytes"), "must be a non-negative integer")
				}
			}
		}
	}
	can := pv["can_apply"]
	if _, ok := can.(bool); !ok {
		v.add(pj(p, "can_apply"), "must be a boolean")
	}
	validateBlocks(v, pj(p, "blocked_by"), pv["blocked_by"], can)
	if t, has := pv["target"]; has {
		validateTarget(v, pj(p, "target"), t)
	}
	if c, has := pv["changes"]; has {
		validateChanges(v, pj(p, "changes"), c)
	}
	if batch {
		targets, ok := pv["targets"].([]any)
		if !ok || len(targets) == 0 {
			v.add(pj(p, "targets"), "must be a non-empty array")
		}
		for i, t := range targets {
			validateTarget(v, pi(pj(p, "targets"), i), t)
		}
		if n, ok := intOf(pv["total"]); !ok || n < 1 {
			v.add(pj(p, "total"), "must be a positive integer")
		}
		items, ok := pv["items"].([]any)
		if !ok || len(items) == 0 {
			v.add(pj(p, "items"), "must be a non-empty array")
		}
		for i, it := range items {
			ip := pi(pj(p, "items"), i)
			if !exactKeys(v, ip, it, []string{"target", "operation_id", "changes", "can_apply", "blocked_by"}, nil) {
				continue
			}
			io := it.(map[string]any)
			validateTarget(v, pj(ip, "target"), io["target"])
			if !matches(opIDRE, io["operation_id"]) {
				v.add(pj(ip, "operation_id"), "must be an operation id")
			}
			validateChanges(v, pj(ip, "changes"), io["changes"])
			if _, ok := io["can_apply"].(bool); !ok {
				v.add(pj(ip, "can_apply"), "must be a boolean")
			}
			validateBlocks(v, pj(ip, "blocked_by"), io["blocked_by"], io["can_apply"])
		}
	}
	if b, has := pv["base"]; has {
		if exactKeys(v, pj(p, "base"), b, []string{"version", "source"}, nil) {
			bo := b.(map[string]any)
			if !isText(bo["version"]) {
				v.add(pj(pj(p, "base"), "version"), "must be a non-empty string")
			}
			if !matches(snakeCase, bo["source"]) {
				v.add(pj(pj(p, "base"), "source"), "must be a snake_case name")
			}
		}
	}
	for _, k := range []string{"destinations", "outputs"} {
		if val, has := pv[k]; has && !textList(val) {
			v.add(pj(p, k), "must be an array of non-empty strings")
		}
	}
	effects := stringList(pv["effects"])
	if contains(effects, "deliver") && len(stringList(pv["destinations"])) == 0 {
		v.add(pj(p, "destinations"), "a deliver preview lists its destinations (decision 15)")
	}
	if (contains(effects, "artifact") || contains(effects, "persist")) && len(stringList(pv["outputs"])) == 0 {
		v.add(pj(p, "outputs"), "an artifact or persist preview lists its outputs (decision 15)")
	}
	if batch && len(v.problems) == before {
		checkPreviewBatch(v, p, pv)
	}
	return true
}

func checkPreviewBatch(v *validation, p string, pv map[string]any) {
	targets, _ := pv["targets"].([]any)
	items, _ := pv["items"].([]any)
	omitted, _ := intOf(pv["omitted"])
	total, _ := intOf(pv["total"])
	keyOf := func(x any) any {
		m, _ := x.(map[string]any)
		return m["key"]
	}
	if len(targets) != len(items) {
		v.add(pj(p, "items"), "follow the %d listed targets one to one (CLI-063); there are %d", len(targets), len(items))
	} else {
		for i := range targets {
			im, _ := items[i].(map[string]any)
			if keyOf(targets[i]) != keyOf(im["target"]) {
				v.add(pj(p, "items"), "follow the targets one to one, in input order (CLI-063)")
				break
			}
		}
	}
	if total != int64(len(targets))+omitted {
		if omitted > 0 {
			v.add(pj(p, "omitted"), "is %d, but total %d less the %d listed targets is %d (CLI-063)", omitted, total, len(targets), total-int64(len(targets)))
		} else {
			v.add(pj(p, "total"), "counts the %d targets one to one; a preview that leaves targets out says how many in omitted (CLI-063)", len(targets))
		}
	}
	var keys, ops []string
	for _, t := range targets {
		s, _ := keyOf(t).(string)
		keys = append(keys, s)
	}
	if !distinct(keys) {
		v.add(pj(p, "targets"), "are de-duplicated (CLI-063)")
	}
	allCan := true
	for _, it := range items {
		im, _ := it.(map[string]any)
		s, _ := im["operation_id"].(string)
		ops = append(ops, s)
		if im["can_apply"] != true {
			allCan = false
		}
	}
	bid, _ := pv["operation_id"].(string)
	if !distinct(ops) || contains(ops, bid) {
		v.add(pj(p, "items"), "every item has its own operation id, distinct from the batch id (CLI-063)")
	}
	if pv["can_apply"] == true && !allCan {
		v.add(pj(p, "can_apply"), "a batch that can be applied has no blocked item")
	}
}

// validateTokenFields checks the token, expiry and operation id that come with a
// preview (dry-run data, confirmation-required details).
func validateTokenFields(v *validation, p string, d map[string]any) {
	preview, _ := d["preview"].(map[string]any)
	tok, exp := d["confirm_token"], d["expires_at"]
	if preview["can_apply"] == false {
		if tok != nil {
			v.add(pj(p, "confirm_token"), "must be null when the preview cannot apply")
		}
		if exp != nil {
			v.add(pj(p, "expires_at"), "must be null when the preview cannot apply")
		}
	} else {
		if _, ok := tok.(string); !ok {
			v.add(pj(p, "confirm_token"), "must be a token when the preview can apply")
		}
		if _, ok := exp.(string); !ok {
			v.add(pj(p, "expires_at"), "must be a time when the preview can apply")
		}
	}
	if tok != nil && !matches(tokenRE, tok) {
		v.add(pj(p, "confirm_token"), "is not a canonical token")
	}
	if exp != nil && !isTime(exp) {
		v.add(pj(p, "expires_at"), "must be an ISO 8601 UTC time")
	}
	oid, _ := d["operation_id"].(string)
	if !anyOpIDRE.MatchString(oid) {
		v.add(pj(p, "operation_id"), "must be an operation id")
		return
	}
	if pid, ok := preview["operation_id"].(string); ok && pid != oid {
		v.add(pj(p, "operation_id"), "is the preview's operation id (CLI-051)")
	}
	if t, ok := tok.(string); ok && tokenRE.MatchString(t) && t[4:30] != idPart(oid) {
		v.add(pj(p, "confirm_token"), "carries the id part of its operation id (CLI-055)")
	}
}

// ---------------------------------------------------------------- batches

func classifyItem(item map[string]any) string {
	if item["ok"] == true {
		return "succeeded"
	}
	errObj, _ := item["error"].(map[string]any)
	receipt, _ := item["receipt"].(map[string]any)
	if errObj != nil && errObj["code"] == "E_OUTCOME_UNKNOWN" {
		return "unknown"
	}
	if receipt != nil {
		if n, _, _ := receiptRule(receipt); n == 1 {
			return "unknown"
		}
	}
	if errObj == nil {
		if receipt == nil {
			return "skipped"
		}
		if o, _ := receipt["outcome"].(map[string]any); o["execution"] == "not_started" && !lasting(o) {
			return "skipped"
		}
	}
	return "failed"
}

func itemApplied(item map[string]any) bool {
	if item["ok"] == true {
		return true
	}
	if r, ok := item["receipt"].(map[string]any); ok {
		o, _ := r["outcome"].(map[string]any)
		return lasting(o)
	}
	return false
}

// batchExpectedCode applies contract.json batch.ok_rule: ok (true) when every item
// succeeded, else the code the batch must carry ("" when the listed items do not
// say). A shortened list (omitted) is judged by its summary, which counts every item.
func batchExpectedCode(batch map[string]any) (string, bool) {
	items, _ := batch["items"].([]any)
	summary, _ := batch["summary"].(map[string]any)
	allSucceeded, unknown, applied := true, false, false
	for _, it := range items {
		m, _ := it.(map[string]any)
		k := classifyItem(m)
		if k != "succeeded" {
			allSucceeded = false
		}
		if k == "unknown" {
			unknown = true
		}
		if itemApplied(m) {
			applied = true
		}
	}
	if hasKey(batch, "omitted") {
		succeeded, _ := intOf(summary["succeeded"])
		total, _ := intOf(summary["total"])
		unknownN, _ := intOf(summary["unknown"])
		if succeeded == total && allSucceeded {
			return "", true
		}
		unknown = unknown || unknownN > 0
		applied = applied || succeeded > 0
	} else if allSucceeded {
		return "", true
	}
	switch {
	case unknown:
		return "E_OUTCOME_UNKNOWN", false
	case applied:
		return "E_PARTIAL", false
	}
	for _, it := range items {
		m, _ := it.(map[string]any)
		if e, ok := m["error"].(map[string]any); ok {
			s, _ := e["code"].(string)
			return s, false
		}
	}
	return "", false
}

var batchCounts = []string{"succeeded", "failed", "unknown", "skipped"}

// validateBatch checks a batch result (CLI-063): items in input order, each under
// its own op_ id, and a summary that counts every target. A list cut to the size
// budget says in omitted how many items it left out and keeps the items with ok
// false first, all of them before any item that succeeded.
func validateBatch(v *validation, p string, batch map[string]any) bool {
	items, ok := batch["items"].([]any)
	if !ok || len(items) == 0 {
		v.add(pj(p, "items"), "must be a non-empty array")
		return false
	}
	before := len(v.problems)
	if om, has := batch["omitted"]; has {
		if n, ok := intOf(om); !ok || n < 1 {
			v.add(pj(p, "omitted"), "must be a positive integer (absent when nothing was left out)")
		}
	}
	for i, it := range items {
		ip := pi(pj(p, "items"), i)
		if !exactKeys(v, ip, it, batchItemRequiredKeys, batchItemOptionalKeys) {
			continue
		}
		m := it.(map[string]any)
		if !isText(m["target"]) {
			v.add(pj(ip, "target"), "must be the target key")
		}
		if !matches(opIDRE, m["operation_id"]) {
			v.add(pj(ip, "operation_id"), "must be an operation id")
		}
		if _, ok := m["ok"].(bool); !ok {
			v.add(pj(ip, "ok"), "must be a boolean")
		}
		if m["ok"] == true && hasKey(m, "error") {
			v.add(pj(ip, "error"), "a successful item has no error")
		}
		if r, has := m["receipt"]; has {
			validateReceipt(v, pj(ip, "receipt"), r)
		}
		if e, has := m["error"]; has && exactKeys(v, pj(ip, "error"), e, batchItemErrorKeys, nil) {
			eo := e.(map[string]any)
			code, _ := eo["code"].(string)
			if !codePattern.MatchString(code) {
				v.add(pj(pj(ip, "error"), "code"), "must match ^E_[A-Z0-9][A-Z0-9_]*$")
			}
			if retry, ok := eo["retryable"].(bool); !ok {
				v.add(pj(pj(ip, "error"), "retryable"), "must be a boolean")
			} else if spec, core := coreCodes[code]; core && retry != spec.retryable {
				v.add(pj(pj(ip, "error"), "retryable"), "must be %v for %s", spec.retryable, code)
			}
			if !isText(eo["message"]) {
				v.add(pj(pj(ip, "error"), "message"), "must be a non-empty string")
			}
		}
	}
	if exactKeys(v, pj(p, "summary"), batch["summary"], batchSummaryKeys, nil) {
		s := batch["summary"].(map[string]any)
		if n, ok := intOf(s["total"]); !ok || n < 1 {
			v.add(pj(pj(p, "summary"), "total"), "must be a positive integer")
		}
		for _, k := range batchCounts {
			if n, ok := intOf(s[k]); !ok || n < 0 {
				v.add(pj(pj(p, "summary"), k), "must be a non-negative integer")
			}
		}
	}
	if len(v.problems) > before {
		return false
	}
	summary := batch["summary"].(map[string]any)
	var targets, ops, kinds []string
	tally := map[string]int64{}
	for _, it := range items {
		m := it.(map[string]any)
		t, _ := m["target"].(string)
		o, _ := m["operation_id"].(string)
		targets = append(targets, t)
		ops = append(ops, o)
		k := classifyItem(m)
		kinds = append(kinds, k)
		tally[k]++
	}
	if !distinct(targets) {
		v.add(pj(p, "items"), "targets are de-duplicated before execution (CLI-063)")
	}
	bid, _ := batch["operation_id"].(string)
	if !distinct(ops) || contains(ops, bid) {
		v.add(pj(p, "items"), "every item runs under its own operation id, distinct from the batch id (CLI-063)")
	}
	for i, it := range items {
		m := it.(map[string]any)
		ip := pi(pj(p, "items"), i)
		if r, ok := m["receipt"].(map[string]any); ok {
			if r["operation_id"] != m["operation_id"] {
				v.add(pj(ip, "receipt"), "the item and its receipt name the same operation")
			}
			n, rok, codes := receiptRule(r)
			if rok != (m["ok"] == true) {
				v.add(pj(ip, "ok"), "follows its receipt (receipt rule %d gives ok %v)", n, rok)
			} else if e, has := m["error"].(map[string]any); !rok && has {
				if code, _ := e["code"].(string); !codes.allows(code) {
					v.add(pj(pj(ip, "error"), "code"), "receipt rule %d gives %s", n, codes.describe())
				}
			}
		}
		if m["ok"] != true && !hasKey(m, "error") && kinds[i] != "skipped" {
			v.add(ip, "an attempted item that did not succeed carries its error code")
		}
	}
	count := func(k string) int64 { n, _ := intOf(summary[k]); return n }
	om, hasOmitted := intOf(batch["omitted"])
	if !hasOmitted {
		if count("total") != int64(len(items)) {
			v.add(pj(pj(p, "summary"), "total"), "is %d but the items give %d (CLI-063: counts equal the item tally)", count("total"), len(items))
		}
		for _, k := range batchCounts {
			if count(k) != tally[k] {
				v.add(pj(pj(p, "summary"), k), "is %d but the items give %d (CLI-063: counts equal the item tally)", count(k), tally[k])
			}
		}
		return true
	}
	if count("total") != int64(len(items))+om {
		v.add(pj(p, "omitted"), "is %d, but summary.total %d less the %d listed items is %d (CLI-063)", om, count("total"), len(items), count("total")-int64(len(items)))
	}
	var sum int64
	for _, k := range batchCounts {
		sum += count(k)
		if count(k) < tally[k] {
			v.add(pj(pj(p, "summary"), k), "is %d, but %d of the listed items are %s (CLI-063)", count(k), tally[k], k)
		}
	}
	if sum != count("total") {
		v.add(pj(p, "summary"), "counts every item exactly once: succeeded + failed + unknown + skipped = total (CLI-063)")
	}
	notOK := count("failed") + count("unknown") + count("skipped")
	listedNotOK := tally["failed"] + tally["unknown"] + tally["skipped"]
	sawSucceeded, ordered := false, true
	for _, k := range kinds {
		if k == "succeeded" {
			sawSucceeded = true
		} else if sawSucceeded {
			ordered = false
		}
	}
	switch {
	case !ordered:
		v.add(pj(p, "items"), "a shortened list puts the items with ok false first, before any that succeeded (CLI-063)")
	case tally["succeeded"] > 0 && listedNotOK < notOK:
		v.add(pj(p, "items"), "a shortened list keeps every item with ok false before any that succeeded; it leaves out %d of the %d items with ok false but lists %d that succeeded (CLI-063)",
			notOK-listedNotOK, notOK, tally["succeeded"])
	}
	return true
}

// ---------------------------------------------------------------- pages

var pageAllKeys = func() []string {
	keys := append(append([]string{}, pageRequiredKeys...), pageContinuationKeys...)
	return append(keys, pageOptionalKeys...)
}()

func validatePage(v *validation, p string, x map[string]any) {
	if !exactKeys(v, p, x, pageRequiredKeys, pageAllKeys) {
		return
	}
	items, itemsOK := x["items"].([]any)
	if !itemsOK {
		v.add(pj(p, "items"), "must be an array")
	}
	count, countOK := intOf(x["count"])
	if !countOK || count < 0 {
		v.add(pj(p, "count"), "must be a non-negative integer")
	}
	more, moreOK := x["has_more"].(bool)
	if !moreOK {
		v.add(pj(p, "has_more"), "must be a boolean")
	}
	conts := 0
	for _, k := range pageContinuationKeys {
		if hasKey(x, k) {
			conts++
		}
	}
	if moreOK && more && conts != 1 {
		v.add(p, "has_more needs exactly one of %s", strings.Join(pageContinuationKeys, ", "))
	}
	if moreOK && !more && conts > 0 {
		v.add(p, "a complete list has no continuation")
	}
	if x["truncated"] == true {
		if !isText(x["truncated_reason"]) {
			v.add(pj(p, "truncated_reason"), "is required when truncated is true")
		}
		if !moreOK || !more {
			v.add(pj(p, "has_more"), "is true when the list was truncated")
		}
	} else if hasKey(x, "truncated_reason") {
		v.add(pj(p, "truncated_reason"), "is only given when truncated is true")
	}
	for _, k := range []string{"next_offset", "offset", "total"} {
		if val, has := x[k]; has {
			if n, ok := intOf(val); !ok || n < 0 {
				v.add(pj(p, k), "must be a non-negative integer")
			}
		}
	}
	for _, k := range []string{"next_cursor", "continuation_reason", "sort"} {
		if val, has := x[k]; has && !isText(val) {
			v.add(pj(p, k), "must be a non-empty string")
		}
	}
	if s, has := x["scope"]; has {
		if _, ok := s.(map[string]any); !ok {
			v.add(pj(p, "scope"), "must be an object")
		}
	}
	if t, has := x["truncated"]; has {
		if _, ok := t.(bool); !ok {
			v.add(pj(p, "truncated"), "must be a boolean")
		}
	}
	if itemsOK && countOK && count != int64(len(items)) {
		v.add(pj(p, "count"), "is %d but there are %d items (CLI-041)", count, len(items))
	}
	next, nextOK := intOf(x["next_offset"])
	offset, offsetOK := intOf(x["offset"])
	if nextOK && offsetOK && countOK && next < offset+max(count, 1) {
		v.add(pj(p, "next_offset"), "advances past offset + count (CLI-041)")
	}
	if total, ok := intOf(x["total"]); ok && countOK {
		end := offset + count
		switch {
		case total < end:
			v.add(pj(p, "total"), "is %d, less than the %d items read up to the end of this page (CLI-041)", total, end)
		case moreOK && !more && offsetOK && total != end:
			v.add(pj(p, "total"), "has_more is false, so this page ends the list at offset + count = %d; total says %d (CLI-041)", end, total)
		}
	}
}

// ---------------------------------------------------------------- scopes

// validateScopeObject checks a scope file object (a refusal's grant_example).
func validateScopeObject(v *validation, p string, x any) {
	if !exactKeys(v, p, x, scopeRequiredKeys, scopeOptionalKeys) {
		return
	}
	s := x.(map[string]any)
	for _, k := range []string{"scope_id", "granted_by"} {
		if val, has := s[k]; has && !isText(val) {
			v.add(pj(p, k), "must be a non-empty string")
		}
	}
	if t, has := s["tool"]; has && !matches(toolNameRE, t) {
		v.add(pj(p, "tool"), "must be a kebab-case tool name")
	}
	if g, has := s["granted_at"]; has && !isTime(g) {
		v.add(pj(p, "granted_at"), "must be an ISO 8601 UTC time")
	}
	if e, has := s["expires_at"]; has && e != nil && !isTime(e) {
		v.add(pj(p, "expires_at"), "must be an ISO 8601 UTC time or null")
	}
	if e, has := s["effects"]; has {
		validateEffects(v, pj(p, "effects"), e, false)
	}
	if d, has := s["dangerous"]; has {
		if _, ok := d.(bool); !ok {
			v.add(pj(p, "dangerous"), "must be a boolean")
		}
	}
	if t, has := s["targets"]; has {
		list, ok := t.([]any)
		if !ok {
			v.add(pj(p, "targets"), "must be an array")
		}
		for i, sel := range list {
			tp := pi(pj(p, "targets"), i)
			if exactKeys(v, tp, sel, targetSelectorRequiredKeys, targetSelectorOptionalKeys) {
				so := sel.(map[string]any)
				if !isText(so["key"]) {
					v.add(pj(tp, "key"), "must be a non-empty string")
				}
				if m, has := so["match"]; has {
					if ms, _ := m.(string); !contains(targetMatchValues, ms) {
						v.add(pj(tp, "match"), "must be subtree or exact")
					}
				}
			}
		}
	}
	for _, k := range []string{"destinations", "outputs", "roots"} {
		if val, has := s[k]; has && !textList(val) {
			v.add(pj(p, k), "must be an array of non-empty strings")
		}
	}
	if l, has := s["limits"]; has && exactKeys(v, pj(p, "limits"), l, nil, limitsKeys) {
		if mo, has := l.(map[string]any)["max_operations"]; has {
			if n, ok := intOf(mo); !ok || n < 1 {
				v.add(pj(pj(p, "limits"), "max_operations"), "must be a positive integer")
			}
		}
	}
	if r, has := s["revoked"]; has {
		if _, ok := r.(bool); !ok {
			v.add(pj(p, "revoked"), "must be a boolean")
		}
	}
	if n, has := s["notes"]; has {
		if _, ok := n.(string); !ok {
			v.add(pj(p, "notes"), "must be a string")
		}
	}
	granted, _ := s["granted_at"].(string)
	expires, _ := s["expires_at"].(string)
	if a, ok := parseTimeAny(granted); ok {
		if b, ok := parseTimeAny(expires); ok && !b.After(a) {
			v.add(pj(p, "expires_at"), "a scope expires after it was granted (SEC-011)")
		}
	}
	for _, k := range []string{"outputs", "roots"} {
		for i, path := range stringList(s[k]) {
			if !isAbsoluteAny(path) {
				v.add(pi(pj(p, k), i), "outputs and roots are absolute directories, checked after symlink resolution (SEC-012)")
			}
		}
	}
}

// isAbsoluteAny accepts a POSIX or a Windows absolute path, whatever the platform.
func isAbsoluteAny(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// parseTimeAny parses an ISO 8601 UTC time with an optional fraction.
func parseTimeAny(s string) (time.Time, bool) {
	if !utcTime.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func validateScopeRefusal(v *validation, p string, x any) {
	if !exactKeys(v, p, x, scopeRefusalRequiredKeys, scopeRefusalOptionalKeys) {
		return
	}
	s := x.(map[string]any)
	status, _ := s["status"].(string)
	if !contains(scopeRefusalStatus, status) {
		v.add(pj(p, "status"), "must be one of %s", strings.Join(scopeRefusalStatus, ", "))
	}
	if !isText(s["path"]) {
		v.add(pj(p, "path"), "must be a non-empty string")
	}
	sid, hasSid := s["scope_id"]
	if hasSid && sid != nil && !isText(sid) {
		v.add(pj(p, "scope_id"), "must be a non-empty string or null")
	}
	switch status {
	case "missing", "invalid":
		if sid != nil {
			v.add(pj(p, "scope_id"), "is null when the scope is missing or invalid")
		}
	case "expired", "revoked", "insufficient", "budget_spent":
		if _, ok := sid.(string); !ok {
			v.add(pj(p, "scope_id"), "names the scope that refused")
		}
	}
	if status == "insufficient" && !hasKey(s, "missing") {
		v.add(p, "missing key \"missing\"")
	}
	if mi, has := s["missing"]; has {
		mo, ok := mi.(map[string]any)
		if !ok || len(mo) == 0 {
			v.add(pj(p, "missing"), "must be a non-empty object")
		} else {
			if e, has := mo["effects"]; has {
				names := stringList(e)
				bad := !isStringList(e) || !distinct(names)
				for _, n := range names {
					bad = bad || !contains(effectValues, n)
				}
				if bad {
					v.add(pj(pj(p, "missing"), "effects"), "must be distinct effects")
				}
			}
			if d, has := mo["dangerous"]; has {
				if _, ok := d.(bool); !ok {
					v.add(pj(pj(p, "missing"), "dangerous"), "must be a boolean")
				}
			}
			if t, has := mo["target"]; has && !isText(t) {
				v.add(pj(pj(p, "missing"), "target"), "must be a non-empty string")
			}
			// outputs: new files outside the scope's outputs; roots: existing files the
			// write would overwrite, outside its roots (C1).
			for _, k := range []string{"destinations", "outputs", "roots"} {
				if val, has := mo[k]; has && !textList(val) {
					v.add(pj(pj(p, "missing"), k), "must be an array of non-empty strings")
				}
			}
		}
	}
	if g, has := s["grant_example"]; has {
		validateScopeObject(v, pj(p, "grant_example"), g)
	}
}

// ---------------------------------------------------------------- errors

// checkSyncCommand enforces F10 (P16): skill_sync_command comes only when the Skill
// sync could not finish (skill_sync_status failed, or the E_PARTIAL at skill_sync,
// partialSync), and then always; never with skipped, not_needed or synced.
func checkSyncCommand(v *validation, p string, d map[string]any, partialSync bool) {
	ss := d["skill_sync_status"]
	if (ss == "skipped" || ss == "not_needed" || ss == "synced") && hasKey(d, "skill_sync_command") {
		v.add(pj(p, "skill_sync_command"), "comes only when the Skill sync could not finish, not with skill_sync_status %v (F10)", ss)
	}
	if (ss == "failed" || partialSync) && !hasKey(d, "skill_sync_command") {
		v.add(p, "missing key \"skill_sync_command\": the Skill sync could not finish (F10)")
	}
}

func validateUpdateFailure(v *validation, p, code string, d map[string]any) {
	requireKeys(v, p, d, updateFailureDetailKeys)
	checkSyncCommand(v, p, d, code == "E_PARTIAL" && d["stage"] == "skill_sync")
	if s, has := d["stage"]; has {
		if st, _ := s.(string); !contains(updateStages, st) {
			v.add(pj(p, "stage"), "must be an update stage")
		}
	}
	for _, k := range []string{"current_version", "target_version", "previous_version"} {
		if val, has := d[k]; has && !matches(semverPattern, val) {
			v.add(pj(p, k), "must be a semantic version")
		}
	}
	if s, has := d["skill_sync_status"]; has {
		if st, _ := s.(string); !contains(updateSkillSyncStatusValues, st) {
			v.add(pj(p, "skill_sync_status"), "must be a Skill sync status")
		}
	}
	if d["binary_replaced"] == true {
		requireKeys(v, p, d, []string{"target_version"})
		if st := d["stage"]; st != "replace" && st != "skill_sync" {
			v.add(pj(p, "stage"), "is replace or skill_sync once the binary is replaced")
		}
		if ss := d["skill_sync_status"]; ss != "failed" && ss != "skipped" {
			v.add(pj(p, "skill_sync_status"), "is failed or skipped for a partial update")
		}
		if ua, has := d["update_available"]; has && ua != false {
			v.add(pj(p, "update_available"), "is false once the binary is replaced")
		}
		if tv, has := d["target_version"]; has && d["current_version"] != tv {
			v.add(pj(p, "current_version"), "once the binary is replaced the running version is the target (CLI-070)")
		}
	} else {
		if d["stage"] == "skill_sync" {
			v.add(pj(p, "stage"), "skill_sync comes after the binary was replaced")
		}
		if hasKey(d, "status") {
			v.add(pj(p, "status"), "only a partial update has a status")
		}
	}
}

// validateUpdateData checks the data of a successful update call (CLI-070); a
// partial update is E_PARTIAL, checked by validateUpdateFailure.
func validateUpdateData(v *validation, p string, x any) {
	if !exactKeys(v, p, x, updateRequiredKeys, updateOptionalKeys) {
		return
	}
	d := x.(map[string]any)
	before := len(v.problems)
	status, _ := d["status"].(string)
	if !contains(updateStatusValues, status) || status == "partial" {
		v.add(pj(p, "status"), "must be up_to_date, updated or available (a partial update is E_PARTIAL)")
	}
	for _, k := range []string{"current_version", "target_version", "previous_version"} {
		if val, has := d[k]; has && !matches(semverPattern, val) {
			v.add(pj(p, k), "must be a semantic version")
		}
	}
	for _, k := range []string{"update_available", "binary_replaced", "signature_verified", "checksum_verified"} {
		if val, has := d[k]; has {
			if _, ok := val.(bool); !ok {
				v.add(pj(p, k), "must be a boolean")
			}
		}
	}
	for _, f := range []struct {
		key    string
		values []string
	}{
		{"install_method", updateInstallMethods},
		{"signature_status", updateSignatureStatusValues},
		{"skill_sync_status", updateSkillSyncStatusValues},
	} {
		if s, _ := d[f.key].(string); !contains(f.values, s) {
			v.add(pj(p, f.key), "must be one of %s", strings.Join(f.values, ", "))
		}
	}
	if s, has := d["stage"]; has {
		if st, _ := s.(string); !contains(updateStages, st) {
			v.add(pj(p, "stage"), "must be an update stage")
		}
	}
	if c, has := d["skill_sync_command"]; has && !isText(c) {
		v.add(pj(p, "skill_sync_command"), "must be a non-empty string")
	}
	checkSyncCommand(v, p, d, false)
	if c, has := d["command"]; has {
		if list, _ := c.([]any); !isText(c) && (len(list) == 0 || !textList(c)) {
			v.add(pj(p, "command"), "must be the command text or its argv")
		}
	}
	replaced := d["binary_replaced"]
	switch status {
	case "updated":
		requireKeys(v, p, d, []string{"previous_version"})
		if d["update_available"] != false {
			v.add(pj(p, "update_available"), "is false once updated")
		}
		if replaced != true {
			v.add(pj(p, "binary_replaced"), "is true once updated")
		}
		if s := d["skill_sync_status"]; s != "synced" && s != "not_needed" && s != "skipped" {
			v.add(pj(p, "skill_sync_status"), "is synced, not_needed or skipped once updated")
		}
	case "up_to_date":
		if d["update_available"] != false {
			v.add(pj(p, "update_available"), "is false when up to date")
		}
		if replaced != nil && replaced != false {
			v.add(pj(p, "binary_replaced"), "is false when up to date")
		}
	case "available":
		if d["update_available"] != true {
			v.add(pj(p, "update_available"), "is true when an update is available")
		}
		if replaced != nil && replaced != false {
			v.add(pj(p, "binary_replaced"), "is false: update --check changes nothing")
		}
	}
	verified := d["signature_status"] == "verified"
	if sv, has := d["signature_verified"]; has && sv != verified {
		v.add(pj(p, "signature_verified"), "is true exactly when signature_status is verified (SEC-050)")
	}
	if d["install_method"] == "binary" && status == "updated" {
		if d["signature_status"] != "verified" {
			v.add(pj(p, "signature_status"), "is verified for an update on the binary path (SEC-050)")
		}
		if cv, has := d["checksum_verified"]; has && cv != true {
			v.add(pj(p, "checksum_verified"), "is true for an update on the binary path (SEC-050)")
		}
	}
	if len(v.problems) > before {
		return
	}
	current, _ := d["current_version"].(string)
	target, _ := d["target_version"].(string)
	cur, cerr := ParseVersion(current)
	tgt, terr := ParseVersion(target)
	if (status == "updated" || status == "up_to_date") && current != target {
		v.add(pj(p, "current_version"), "%s reports the final state: current_version equals target_version (CLI-070)", status)
	}
	if status == "available" && cerr == nil && terr == nil && CompareVersions(tgt, cur) == 0 {
		v.add(pj(p, "target_version"), "an available target differs from the current version (CLI-070)")
	}
	if status == "updated" && d["previous_version"] == current {
		v.add(pj(p, "previous_version"), "an update changes the installed version")
	}
}

func validateErrorObject(v *validation, p string, x any) {
	if !exactKeys(v, p, x, errorObjectKeys, nil) {
		return
	}
	obj := x.(map[string]any)
	code, _ := obj["code"].(string)
	if !codePattern.MatchString(code) {
		v.add(pj(p, "code"), "must match ^E_[A-Z0-9][A-Z0-9_]*$")
		code = ""
	}
	if !isText(obj["message"]) {
		v.add(pj(p, "message"), "must be a non-empty string")
	}
	if retry, ok := obj["retryable"].(bool); !ok {
		v.add(pj(p, "retryable"), "must be a boolean")
	} else if spec, core := coreCodes[code]; core && retry != spec.retryable {
		v.add(pj(p, "retryable"), "must be %v for %s", spec.retryable, code)
	}
	d, ok := obj["details"].(map[string]any)
	if !ok {
		v.add(pj(p, "details"), "must be an object")
		return
	}
	validateErrorDetails(v, pj(p, "details"), code, d)
}

// validateErrorDetails checks the details every error and each code require
// (CLI-011 .. CLI-015, CLI-059, CLI-063, CLI-065, CLI-070), then the receipt and
// batch rules against the code.
func validateErrorDetails(v *validation, p, code string, d map[string]any) {
	before := len(v.problems)
	requireKeys(v, p, d, detailsAlwaysRequired)
	ds, _ := d["dispatch_state"].(string)
	if hasKey(d, "dispatch_state") && !contains(dispatchStates, ds) {
		v.add(pj(p, "dispatch_state"), "must be one of %s", strings.Join(dispatchStates, ", "))
	}
	if na, has := d["next_action"]; has && !isText(na) {
		v.add(pj(p, "next_action"), "must be a non-empty string")
	}
	problem, _ := d["problem"].(string)
	if hasKey(d, "problem") && !contains(problemValues, problem) {
		v.add(pj(p, "problem"), "must be one of the contract's problem values")
	}
	for _, k := range []string{"parameter", "usage", "lookup", "resume"} {
		if val, has := d[k]; has && !isText(val) {
			v.add(pj(p, k), "must be a non-empty string")
		}
	}
	if dym, has := d["did_you_mean"]; has {
		if list, ok := dym.([]any); !ok || len(list) == 0 || !textList(dym) {
			v.add(pj(p, "did_you_mean"), "must be a non-empty array of strings")
		}
	}
	if av, has := d["available"]; has && !textList(av) {
		v.add(pj(p, "available"), "must be an array of strings")
	}
	if um, has := d["upstream_message"]; has {
		if _, ok := um.(string); !ok {
			v.add(pj(p, "upstream_message"), "must be a string")
		}
	}
	if hs, has := d["http_status"]; has {
		if n, ok := intOf(hs); !ok || n < 100 || n > 599 {
			v.add(pj(p, "http_status"), "must be an HTTP status")
		}
	}
	conflict, _ := d["conflict"].(string)
	if hasKey(d, "conflict") && !contains(conflictValues, conflict) {
		v.add(pj(p, "conflict"), "must be one of the contract's conflict values")
	}
	for _, k := range []string{"active_operation", "operation_id"} {
		if val, has := d[k]; has && !matches(anyOpIDRE, val) {
			v.add(pj(p, k), "must be an operation id")
		}
	}
	if c, has := d["candidates"]; has {
		cands, ok := c.([]any)
		if !ok {
			v.add(pj(p, "candidates"), "must be an array")
		}
		for i, cand := range cands {
			cp := pi(pj(p, "candidates"), i)
			if exactKeys(v, cp, cand, []string{"ref", "label"}, nil) {
				co := cand.(map[string]any)
				for _, k := range []string{"ref", "label"} {
					if !isText(co[k]) {
						v.add(pj(cp, k), "must be a non-empty string")
					}
				}
			}
		}
		if code == "E_AMBIGUOUS" && len(cands) < 2 {
			v.add(pj(p, "candidates"), "an ambiguous name has at least two candidates")
		}
	}
	if s, has := d["scope"]; has {
		validateScopeRefusal(v, pj(p, "scope"), s)
	}
	if r, has := d["required"]; has {
		_, isStr := r.(string)
		_, isObj := r.(map[string]any)
		if !isStr && !isObj {
			v.add(pj(p, "required"), "must be a string or an object")
		}
	}
	if pv, has := d["preview"]; has {
		validatePreview(v, pj(p, "preview"), pv)
	}
	if t, has := d["confirm_token"]; has && t != nil && !matches(tokenRE, t) {
		v.add(pj(p, "confirm_token"), "is not a canonical token")
	}
	if e, has := d["expires_at"]; has && e != nil && !isTime(e) {
		v.add(pj(p, "expires_at"), "must be an ISO 8601 UTC time")
	}
	receiptOK := false
	if r, has := d["receipt"]; has {
		receiptOK = validateReceipt(v, pj(p, "receipt"), r)
	}
	if hasKey(d, "items") && !hasKey(d, "summary") {
		v.add(p, "missing key \"summary\" (items come with their summary)")
	}
	if hasKey(d, "omitted") && !hasKey(d, "items") {
		v.add(p, "missing key \"items\" (omitted counts the items a list left out)")
	}
	if a, has := d["action"]; has && code == "E_HUMAN_REQUIRED" {
		if s, _ := a.(string); !contains(humanActionValues, s) {
			v.add(pj(p, "action"), "must be one of %s", strings.Join(humanActionValues, ", "))
		}
	}
	if ra, has := d["retry_after_ms"]; has {
		if n, ok := intOf(ra); !ok || n < 0 {
			v.add(pj(p, "retry_after_ms"), "must be a non-negative integer")
		}
	}
	updateDetails := hasKey(d, "stage") && hasKey(d, "binary_replaced")
	if updateDetails {
		validateUpdateFailure(v, p, code, d)
	}
	// dispatched means a write's request reached the upstream, so the error says
	// what happened to it (decision 1); the listed codes have their own rule.
	if ds == "dispatched" && !contains(ownDispatchRule, code) && !hasKey(d, "receipt") && !hasKey(d, "items") && !updateDetails {
		v.add(pj(p, "dispatch_state"), "is dispatched only for a write, whose error carries its receipt or items (CLI-011)")
	}
	switch code {
	case "E_USAGE":
		requireKeys(v, p, d, []string{"problem"})
		if ds != "not_started" {
			v.add(pj(p, "dispatch_state"), "is not_started for a usage error")
		}
		if problem == "confirmation_required" || problem == "dangerous_flag_required" {
			v.add(pj(p, "problem"), "belongs to E_CONFIRMATION_REQUIRED")
		}
		if (problem == "unknown_flag" || problem == "missing_parameter") && !hasKey(d, "parameter") {
			v.add(p, "missing key \"parameter\"")
		}
	case "E_VALIDATION":
		requireKeys(v, p, d, []string{"problem"})
		if problem == "confirmation_required" || problem == "dangerous_flag_required" {
			v.add(pj(p, "problem"), "belongs to E_CONFIRMATION_REQUIRED")
		}
	case "E_CONFIRMATION_REQUIRED":
		requireKeys(v, p, d, []string{"problem"})
		if ds != "not_started" {
			v.add(pj(p, "dispatch_state"), "is not_started when confirmation is required")
		}
		if problem != "confirmation_required" && problem != "dangerous_flag_required" {
			v.add(pj(p, "problem"), "is confirmation_required or dangerous_flag_required")
		}
		if problem == "confirmation_required" {
			requireKeys(v, p, d, []string{"preview", "confirm_token", "expires_at", "operation_id"})
			validateTokenFields(v, p, d)
		}
	case "E_CONFLICT":
		requireKeys(v, p, d, []string{"conflict"})
		if contains(gateConflictValues, conflict) && ds != "not_started" {
			v.add(pj(p, "dispatch_state"), "is not_started: the gate finds %s before anything is sent (CLI-013)", conflict)
		}
		if conflict == "token_mismatch" && !hasKey(d, "parameter") {
			v.add(p, "missing key \"parameter\"")
		}
		// lock_held names the running operation only when the lock's owner is known (P15).
		if conflict == "unresolved_operation" && !hasKey(d, "active_operation") {
			v.add(p, "missing key \"active_operation\"")
		}
	case "E_FORBIDDEN":
		if ds == "not_started" && !hasKey(d, "scope") && !hasKey(d, "http_status") && !hasKey(d, "required") {
			v.add(p, "missing key \"scope\" (a local refusal comes from the scope)")
		}
	case "E_AMBIGUOUS":
		requireKeys(v, p, d, []string{"candidates"})
	case "E_OUTCOME_UNKNOWN":
		requireKeys(v, p, d, []string{"operation_id", "lookup"})
		if ds != "dispatched" && ds != "unknown" {
			v.add(pj(p, "dispatch_state"), "is dispatched or unknown for an unknown outcome")
		}
		if l, ok := d["lookup"].(string); ok {
			if id, ok := d["operation_id"].(string); ok && !strings.Contains(l, id) {
				v.add(pj(p, "lookup"), "must be the command that looks up details.operation_id")
			}
		}
	case "E_PARTIAL":
		if ds != "dispatched" {
			v.add(pj(p, "dispatch_state"), "is dispatched when part of the change took effect")
		}
		if !hasKey(d, "receipt") && !hasKey(d, "items") && !updateDetails {
			v.add(p, "needs receipt, items, or the update failure details")
		}
		if updateDetails && d["binary_replaced"] != true {
			v.add(pj(p, "binary_replaced"), "is true for a partial update")
		}
	case "E_VERIFY_FAILED", "E_VERIFY_INCOMPLETE":
		requireKeys(v, p, d, []string{"receipt"})
		if ds != "dispatched" {
			v.add(pj(p, "dispatch_state"), "is dispatched once the change was applied")
		}
	case "E_HUMAN_REQUIRED":
		requireKeys(v, p, d, []string{"action", "resume"})
	case "E_NETWORK":
		if ds != "not_started" {
			v.add(pj(p, "dispatch_state"), "is not_started: E_NETWORK means nothing was sent (CLI-015)")
		}
	case "E_INTEGRITY":
		requireKeys(v, p, d, []string{"stage", "current_version", "binary_replaced", "skill_sync_status"})
		// replace: an unsafe archive entry, or a new binary that reports another
		// version (contract.json self_description.update.stage_codes).
		if st := d["stage"]; st != "verify_signature" && st != "verify_checksum" && st != "replace" {
			v.add(pj(p, "stage"), "is verify_signature, verify_checksum or replace")
		}
		if d["binary_replaced"] != false {
			v.add(pj(p, "binary_replaced"), "is false: the install was refused")
		}
	}
	if len(v.problems) > before {
		return
	}
	codePath := strings.TrimSuffix(p, ".details") + ".code"
	if receiptOK {
		r := d["receipt"].(map[string]any)
		n, ok, codes := receiptRule(r)
		switch {
		case ok:
			v.add(pj(p, "receipt"), "receipt_rules make this receipt ok; a successful write is not an error (CLI-059)")
		case !codes.allows(code):
			v.add(codePath, "receipt rule %d gives %s, not %s (CLI-059)", n, codes.describe(), code)
		}
		if id, has := d["operation_id"]; has && id != r["operation_id"] {
			v.add(pj(p, "operation_id"), "names the receipt's operation")
		}
	}
	if hasKey(d, "items") && hasKey(d, "summary") && validateBatch(v, p, d) && len(v.problems) == before {
		switch expected, ok := batchExpectedCode(d); {
		case ok:
			v.add(pj(p, "items"), "every item succeeded, so the batch is ok true (CLI-063)")
		case expected != "" && code != expected:
			v.add(codePath, "batch.ok_rule gives %s for these items, not %s (CLI-063)", expected, code)
		}
	}
}

// ---------------------------------------------------------------- operations

func validateOperation(v *validation, p string, x any) {
	if !exactKeys(v, p, x, operationRecordKeys, operationOptionalKeys) {
		return
	}
	r := x.(map[string]any)
	before := len(v.problems)
	oid, _ := r["operation_id"].(string)
	if !anyOpIDRE.MatchString(oid) {
		v.add(pj(p, "operation_id"), "must be an operation id")
		return
	}
	if !matches(commandPathRE, r["command"]) {
		v.add(pj(p, "command"), "must be a command path")
	}
	status, _ := r["status"].(string)
	if !contains(operationStatusValues, status) {
		v.add(pj(p, "status"), "must be one of %s", strings.Join(operationStatusValues, ", "))
	}
	if r["target"] != nil {
		validateTarget(v, pj(p, "target"), r["target"])
	}
	validateEffects(v, pj(p, "effects"), r["effects"], true)
	if sid := r["scope_id"]; sid != nil && !isText(sid) {
		v.add(pj(p, "scope_id"), "must be a non-empty string or null")
	}
	if !isTime(r["created_at"]) {
		v.add(pj(p, "created_at"), "must be an ISO 8601 UTC time")
	}
	for _, k := range []string{"expires_at", "dispatched_at", "finished_at"} {
		if r[k] != nil && !isTime(r[k]) {
			v.add(pj(p, k), "must be an ISO 8601 UTC time or null")
		}
	}
	if r["preview"] != nil {
		validatePreview(v, pj(p, "preview"), r["preview"])
	}
	if r["receipt"] != nil {
		validateReceipt(v, pj(p, "receipt"), r["receipt"])
	}
	res := r["resolution"]
	if res != nil && exactKeys(v, pj(p, "resolution"), res, resolutionKeys, nil) {
		ro := res.(map[string]any)
		if s, _ := ro["outcome"].(string); !contains(resolutionOutcomes, s) {
			v.add(pj(pj(p, "resolution"), "outcome"), "must be applied, not_applied or partial")
		}
		if !isText(ro["evidence"]) {
			v.add(pj(pj(p, "resolution"), "evidence"), "must be a non-empty string")
		}
		if !isTime(ro["resolved_at"]) {
			v.add(pj(pj(p, "resolution"), "resolved_at"), "must be an ISO 8601 UTC time")
		}
	}
	batch := strings.HasPrefix(oid, "opb_")
	if b := r["batch_id"]; b != nil && !matches(batchIDRE, b) {
		v.add(pj(p, "batch_id"), "must be a batch id")
	}
	items, isList := r["items"].([]any)
	if batch {
		switch {
		case hasKey(r, "items") && r["items"] == nil:
			// Only a batch record that could not be read has null items (CLI-058): its
			// outcome is unknown, or resolved once operation resolve recorded it.
			if status != "unknown" && status != "resolved" {
				v.add(pj(p, "status"), "is unknown or resolved when the batch record could not be read (items is null)")
			}
		case !isList || len(items) == 0:
			v.add(pj(p, "items"), "a batch lists its items")
		}
		for _, k := range []string{"target", "receipt", "batch_id"} {
			if r[k] != nil {
				v.add(pj(p, k), "is null for a batch")
			}
		}
	} else if r["items"] != nil {
		v.add(pj(p, "items"), "is null for a single operation")
	}
	if om, has := r["items_omitted"]; has {
		n, isInt := intOf(om)
		switch {
		case !batch:
			v.add(pj(p, "items_omitted"), "is absent for a single operation; it counts the items a shortened batch leaves out")
		case !isInt || n < 1:
			v.add(pj(p, "items_omitted"), "must be an integer of at least 1")
		}
	}
	for i, it := range items {
		ip := pi(pj(p, "items"), i)
		if exactKeys(v, ip, it, []string{"operation_id", "target", "status"}, []string{"receipt"}) {
			io := it.(map[string]any)
			if !matches(opIDRE, io["operation_id"]) {
				v.add(pj(ip, "operation_id"), "must be an operation id")
			}
			if io["target"] != nil {
				validateTarget(v, pj(ip, "target"), io["target"])
			}
			switch s, _ := io["status"].(string); {
			case !contains(operationStatusValues, s):
				v.add(pj(ip, "status"), "must be an operation status")
			case hasKey(io, "target") && io["target"] == nil && s != "unknown":
				// A null target means the item's own record cannot be read.
				v.add(pj(ip, "status"), "is unknown when the item record cannot be read (target is null)")
			}
		}
	}
	_, dispatched := r["dispatched_at"].(string)
	_, finished := r["finished_at"].(string)
	switch status {
	case "planned", "expired":
		for _, k := range []string{"dispatched_at", "finished_at", "receipt"} {
			if r[k] != nil {
				v.add(pj(p, k), "is null while the operation is %s", status)
			}
		}
	case "dispatching":
		if !dispatched {
			v.add(pj(p, "dispatched_at"), "is set once dispatched")
		}
		for _, k := range []string{"finished_at", "receipt"} {
			if r[k] != nil {
				v.add(pj(p, k), "is null while dispatching")
			}
		}
	}
	if contains([]string{"unknown", "applied", "not_applied", "partial", "failed", "resolved"}, status) && !dispatched {
		v.add(pj(p, "dispatched_at"), "is set for status %s", status)
	}
	if contains([]string{"applied", "not_applied", "partial", "failed"}, status) && !finished {
		v.add(pj(p, "finished_at"), "is set for status %s", status)
	}
	if (status == "applied" || status == "partial") && !batch {
		if _, ok := r["receipt"].(map[string]any); !ok {
			v.add(pj(p, "receipt"), "is set for status %s", status)
		}
	}
	if status == "resolved" {
		if _, ok := res.(map[string]any); !ok {
			v.add(pj(p, "resolution"), "is set when resolved")
		}
	} else if res != nil {
		v.add(pj(p, "resolution"), "is null unless resolved")
	}
	if len(v.problems) > before {
		return
	}
	operationSemantics(v, p, r)
}

func operationBatchStatus(statuses []string) string {
	all := func(ok func(string) bool) bool {
		for _, s := range statuses {
			if !ok(s) {
				return false
			}
		}
		return true
	}
	anyOf := func(vals ...string) bool {
		for _, s := range statuses {
			if contains(vals, s) {
				return true
			}
		}
		return false
	}
	switch {
	case anyOf("unknown", "dispatching"):
		return "unknown"
	case all(func(s string) bool { return s == "applied" }):
		return "applied"
	case anyOf("applied", "partial"):
		return "partial"
	case all(func(s string) bool { return s == "not_applied" }):
		return "not_applied"
	case all(func(s string) bool { return s == "not_applied" || s == "failed" }):
		return "failed"
	}
	return ""
}

func operationSemantics(v *validation, p string, r map[string]any) {
	oid := r["operation_id"]
	status, _ := r["status"].(string)
	preview, _ := r["preview"].(map[string]any)
	receipt, _ := r["receipt"].(map[string]any)
	target, _ := r["target"].(map[string]any)
	if preview != nil {
		if preview["operation_id"] != oid {
			v.add(pj(pj(p, "preview"), "operation_id"), "the recorded preview belongs to this operation")
		}
		if pt, ok := preview["target"].(map[string]any); ok && target != nil && pt["key"] != target["key"] {
			v.add(pj(pj(p, "preview"), "target"), "the record and its preview name the same target")
		}
	}
	if receipt != nil {
		if receipt["operation_id"] != oid {
			v.add(pj(pj(p, "receipt"), "operation_id"), "the receipt belongs to this operation")
		}
		if rt, ok := receipt["target"].(map[string]any); ok && target != nil && rt["key"] != target["key"] {
			v.add(pj(pj(p, "receipt"), "target"), "the record and its receipt name the same target")
		}
		n, _, codes := receiptRule(receipt)
		o, _ := receipt["outcome"].(map[string]any)
		last := lasting(o)
		knownFailure := n != 1 && n != 2 && !last
		switch {
		case status == "applied" && (n == 1 || n == 2 || !last):
			v.add(pj(p, "status"), "applied needs a receipt with a lasting effect and no unknown or partial dimension")
		case status == "partial" && !codes.only("E_PARTIAL"):
			v.add(pj(p, "status"), "partial needs a receipt that receipt_rules make E_PARTIAL")
		case status == "unknown" && n != 1:
			v.add(pj(p, "status"), "unknown cannot hold a receipt with a known outcome (CLI-060)")
		case (status == "not_applied" || status == "failed") && !knownFailure:
			v.add(pj(p, "status"), "%s holds a receipt with no lasting effect and no unknown dimension", status)
		}
	}
	if items, ok := r["items"].([]any); ok {
		// A batch too large for the budget lists only some items, those that need
		// attention first, and items_omitted counts the rest (operation.shortening).
		// An item whose own record cannot be read (target null, status unknown) says
		// nothing about the batch: the status rules and the preview comparison use the
		// readable items.
		omitted, _ := intOf(r["items_omitted"])
		var all, statuses, ops []string
		onlyPlanned, anyUnknown := true, false
		for _, it := range items {
			m, _ := it.(map[string]any)
			s, _ := m["status"].(string)
			o, _ := m["operation_id"].(string)
			all = append(all, s)
			ops = append(ops, o)
			if m["target"] == nil {
				continue
			}
			statuses = append(statuses, s)
			onlyPlanned = onlyPlanned && s == "planned"
			anyUnknown = anyUnknown || s == "unknown" || s == "dispatching"
		}
		expected := ""
		if len(statuses) > 0 {
			expected = operationBatchStatus(statuses)
		}
		switch {
		case status == "planned" && !onlyPlanned:
			v.add(pj(p, "status"), "a planned batch has only planned items")
		case omitted > 0 && status != "unknown" && anyUnknown:
			// The listed items of a shortened batch can still prove it unknown.
			v.add(pj(p, "status"), "a batch with an unknown or dispatching item is unknown")
		case omitted == 0 && contains([]string{"applied", "partial", "unknown", "failed", "not_applied"}, status) && expected != "" && status != expected:
			v.add(pj(p, "status"), "a batch whose items are %s is %s", strings.Join(statuses, ", "), expected)
		}
		if !distinct(ops) {
			v.add(pj(p, "items"), "every batch item has its own operation id")
		}
		if omitted > 0 {
			seenDone := false
			for _, s := range all {
				if contains(operationDoneStatusValues, s) {
					seenDone = true
				} else if seenDone {
					v.add(pj(p, "items"), "a shortened batch lists the items that need attention before the done ones (operation.shortening)")
					break
				}
			}
		}
		if preview != nil && hasKey(preview, "items") {
			total, _ := intOf(preview["total"])
			pitems, _ := preview["items"].([]any)
			switch {
			case total != int64(len(items))+omitted:
				listed := strconv.Itoa(len(items))
				if omitted > 0 {
					listed = fmt.Sprintf("%d + %d omitted", len(items), omitted)
				}
				v.add(pj(p, "items"), "a batch record lists every item: its preview counts %d, the record has %s", total, listed)
			case omitted > 0:
				// A shortened list is not in input order: each listed item has the
				// target of the preview item with its operation id.
				keys := map[string]any{}
				for _, pit := range pitems {
					pm, _ := pit.(map[string]any)
					pt, _ := pm["target"].(map[string]any)
					o, _ := pm["operation_id"].(string)
					keys[o] = pt["key"]
				}
				for _, it := range items {
					rm, _ := it.(map[string]any)
					rt, _ := rm["target"].(map[string]any)
					o, _ := rm["operation_id"].(string)
					if key, has := keys[o]; has && rt["key"] != nil && key != rt["key"] {
						v.add(pj(p, "items"), "a batch record lists the items of its preview")
						break
					}
				}
			default:
				// Position by position: the same operation ids, and the same targets
				// where the item's record could be read.
				for i, pit := range pitems {
					if i >= len(items) {
						break
					}
					pm, _ := pit.(map[string]any)
					rm, _ := items[i].(map[string]any)
					pt, _ := pm["target"].(map[string]any)
					rt, _ := rm["target"].(map[string]any)
					if pm["operation_id"] != rm["operation_id"] || (rt["key"] != nil && pt["key"] != rt["key"]) {
						v.add(pj(p, "items"), "a batch record lists the items of its preview, in order")
						break
					}
				}
			}
		}
		for i, it := range items {
			m, _ := it.(map[string]any)
			if rr, ok := m["receipt"].(map[string]any); ok && rr["operation_id"] != m["operation_id"] {
				v.add(pj(pi(pj(p, "items"), i), "receipt"), "the item and its receipt name the same operation")
			}
		}
	}
	createdAt, _ := r["created_at"].(string)
	created, cok := parseTimeAny(createdAt)
	prevKey, prev := "created_at", created
	for _, k := range []string{"dispatched_at", "finished_at"} {
		if s, ok := r[k].(string); ok {
			t, tok := parseTimeAny(s)
			if tok && cok && t.Before(prev) {
				v.add(pj(p, k), "is not before %s", prevKey)
			}
			prevKey, prev = k, t
		}
	}
	if s, ok := r["expires_at"].(string); ok && cok {
		if t, ok := parseTimeAny(s); ok {
			if life := t.Sub(created); life <= 0 || life > time.Duration(tokenMaxTTLSeconds)*time.Second {
				v.add(pj(p, "expires_at"), "the token expires within %d s of the dry-run (CLI-055)", tokenMaxTTLSeconds)
			}
		}
	}
	if res, ok := r["resolution"].(map[string]any); ok && cok {
		resolvedAt, _ := res["resolved_at"].(string)
		if t, ok := parseTimeAny(resolvedAt); ok && t.Before(created) {
			v.add(pj(pj(p, "resolution"), "resolved_at"), "an operation is resolved after it was created")
		}
	}
}

// ---------------------------------------------------------------- notices, meta and paths

func validateNotice(v *validation, p string, x any) {
	n, ok := x.(map[string]any)
	if !ok {
		v.add(p, "must be an object")
		return
	}
	requireKeys(v, p, n, noticeRequiredKeys)
	if !matches(snakeCase, n["type"]) {
		v.add(pj(p, "type"), "must be a snake_case name")
	}
	if s, _ := n["severity"].(string); !contains(noticeSeverityValues, s) {
		v.add(pj(p, "severity"), "must be one of %s", strings.Join(noticeSeverityValues, ", "))
	}
	if !isText(n["message"]) {
		v.add(pj(p, "message"), "must be a non-empty string")
	}
	if n["type"] != "update_available" {
		return
	}
	before := len(v.problems)
	exactKeys(v, p, n, append(append([]string{}, noticeRequiredKeys...), updateNoticeKeys...), nil)
	for _, k := range []string{"current_version", "latest_version"} {
		if val, has := n[k]; has && !matches(semverPattern, val) {
			v.add(pj(p, k), "must be a semantic version")
		}
	}
	if c, has := n["command"]; has && !isText(c) {
		v.add(pj(p, "command"), "must be a non-empty string")
	}
	if c, has := n["checked_at"]; has && !isTime(c) {
		v.add(pj(p, "checked_at"), "must be an ISO 8601 UTC time")
	}
	if len(v.problems) > before {
		return
	}
	curText, _ := n["current_version"].(string)
	latestText, _ := n["latest_version"].(string)
	cur, err1 := ParseVersion(curText)
	latest, err2 := ParseVersion(latestText)
	if err1 != nil || err2 != nil {
		return
	}
	switch {
	case CompareVersions(latest, cur) <= 0:
		v.add(pj(p, "latest_version"), "names a version newer than the current one (CLI-071)")
	case latest.Major > cur.Major && n["severity"] != "warning":
		v.add(pj(p, "severity"), "an update that crosses a major version is a warning (CLI-071)")
	}
}

func validateMeta(v *validation, raw any, ok bool) {
	if !exactKeys(v, "meta", raw, metaRequiredKeys, metaOptionalKeys) {
		return
	}
	meta := raw.(map[string]any)
	if n, isInt := intOf(meta["duration_ms"]); !isInt || n < 0 {
		v.add("meta.duration_ms", "must be a non-negative integer")
	}
	if n, has := meta["notices"]; has {
		list, isList := n.([]any)
		if !isList || len(list) == 0 {
			v.add("meta.notices", "must be a non-empty array")
		}
		for i, item := range list {
			validateNotice(v, pi("meta.notices", i), item)
		}
	}
	if u, has := meta["untrusted"]; has {
		list, isList := u.([]any)
		if !isList || len(list) == 0 {
			v.add("meta.untrusted", "must be a non-empty array")
			return
		}
		if paths := stringList(list); len(paths) != len(list) || !distinct(paths) {
			v.add("meta.untrusted", "must not repeat a path")
		}
		for i, item := range list {
			s, _ := item.(string)
			up := pi("meta.untrusted", i)
			switch {
			case !untrustedRE.MatchString(s):
				v.add(up, "is not a path like data.items[].title or error.details.upstream_message")
			case ok && s != "data" && !strings.HasPrefix(s, "data.") && !strings.HasPrefix(s, "data["):
				v.add(up, "a success response marks paths in data")
			case !ok && s != "error.details" && !strings.HasPrefix(s, "error.details."):
				v.add(up, "a failure response marks paths in error.details")
			}
		}
	}
}

// checkUntrusted walks each meta.untrusted path through the envelope: [] must step
// into an array, and an array must be stepped into with [] (CLI-046).
func checkUntrusted(v *validation, env map[string]any) {
	meta, _ := env["meta"].(map[string]any)
	for i, raw := range stringList(meta["untrusted"]) {
		nodes := []any{env}
		segments := strings.Split(raw, ".")
		for depth, segment := range segments {
			name := strings.TrimRight(segment, "[]")
			arrays := (len(segment) - len(name)) / 2
			following := depth+1 < len(segments)
			var next []any
			for _, node := range nodes {
				obj, ok := node.(map[string]any)
				if !ok {
					continue
				}
				val, has := obj[name]
				if !has {
					continue
				}
				values := []any{val}
				for a := 0; a < arrays; a++ {
					var expanded []any
					for _, x := range values {
						if list, isList := x.([]any); isList {
							expanded = append(expanded, list...)
						} else {
							v.add(pi("meta.untrusted", i), "%s: %s is not an array, so [] does not apply", raw, name)
						}
					}
					values = expanded
				}
				for _, x := range values {
					if _, isList := x.([]any); following && isList {
						v.add(pi("meta.untrusted", i), "%s: %s is an array; step into it with [] (CLI-046)", raw, name)
					} else {
						next = append(next, x)
					}
				}
			}
			nodes = next
		}
	}
}

// genericChecks applies CLI-007 to every key: snake_case names, ids as strings and
// times as ISO 8601 UTC with Z (keys of code-keyed maps are exempt from snake_case).
func genericChecks(v *validation, value any, path string, mapObject bool) {
	switch x := value.(type) {
	case map[string]any:
		for _, k := range sortedKeys(x) {
			item := x[k]
			at := pj(path, k)
			if !mapObject {
				if !snakeCase.MatchString(k) {
					v.add(at, "key is not lowercase snake_case (CLI-007)")
				}
				if k == "id" || k == "iid" || strings.HasSuffix(k, "_id") {
					if _, isString := item.(string); item != nil && !isString {
						v.add(at, "ids are strings (CLI-007)")
					}
				}
				if (k == "ids" || strings.HasSuffix(k, "_ids")) && !isStringList(item) {
					v.add(at, "ids are strings (CLI-007)")
				}
				if strings.HasSuffix(k, "_at") && item != nil && !isTime(item) {
					v.add(at, "times are ISO 8601 UTC with Z (CLI-007)")
				}
			}
			_, isObject := item.(map[string]any)
			genericChecks(v, item, at, codeKeyedMaps[k] && isObject && !mapObject)
		}
	case []any:
		for i, item := range x {
			genericChecks(v, item, pi(path, i), false)
		}
	}
}

// dataKind recognises the objects the contract reserves inside data, in the order
// envelope.schema.json $defs/data checks them.
func dataKind(data any) string {
	d, ok := data.(map[string]any)
	switch {
	case !ok:
		return "other"
	case hasKey(d, "operation_id") && hasKey(d, "status") && hasKey(d, "created_at"):
		return "operation"
	case hasKey(d, "confirm_token") || hasKey(d, "preview"):
		return "dry_run"
	case hasKey(d, "receipt"):
		return "write"
	case hasKey(d, "items") && hasKey(d, "summary"):
		return "batch"
	case hasKey(d, "items"):
		for _, k := range []string{"count", "has_more", "next_cursor", "next_offset", "continuation_reason", "truncated"} {
			if hasKey(d, k) {
				return "page"
			}
		}
	}
	return "other"
}

func validateSuccessData(v *validation, data any) {
	d, _ := data.(map[string]any)
	switch dataKind(data) {
	case "operation":
		validateOperation(v, "data", d)
	case "dry_run":
		if exactKeys(v, "data", d, dryRunDataKeys, nil) {
			validatePreview(v, "data.preview", d["preview"])
			validateTokenFields(v, "data", d)
		}
	case "write":
		if !exactKeys(v, "data", d, []string{"operation_id", "receipt"}, []string{"result"}) {
			return
		}
		if !matches(opIDRE, d["operation_id"]) {
			v.add("data.operation_id", "must be an operation id")
		}
		before := len(v.problems)
		if validateReceipt(v, "data.receipt", d["receipt"]) && len(v.problems) == before {
			r := d["receipt"].(map[string]any)
			if n, ok, codes := receiptRule(r); !ok {
				v.add("data.receipt", "receipt rule %d makes this write ok false with %s (CLI-059)", n, codes.describe())
			}
			if d["operation_id"] != r["operation_id"] {
				v.add("data.operation_id", "names the receipt's operation")
			}
		}
	case "batch":
		if !exactKeys(v, "data", d, []string{"items", "summary"}, []string{"operation_id", "omitted"}) {
			return
		}
		if id, has := d["operation_id"]; has && !matches(batchIDRE, id) {
			v.add("data.operation_id", "must be a batch id")
		}
		before := len(v.problems)
		if validateBatch(v, "data", d) && len(v.problems) == before {
			if expected, ok := batchExpectedCode(d); !ok {
				if expected == "" {
					expected = "its first failed item's code"
				}
				v.add("data", "a batch with a failed, unknown or skipped item is ok false with %s (CLI-063)", expected)
			}
		}
	case "page":
		validatePage(v, "data", d)
	}
}

// isFullReference recognises the full reference, which is exempt from the success
// budget (decision 17).
func isFullReference(data any) bool {
	d, ok := data.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range referenceRequiredKeys {
		if !hasKey(d, k) {
			return false
		}
	}
	return true
}

// compactSize is the length in characters of a value's compact JSON encoding.
func compactSize(x any) int {
	b, err := encodeJSON(x, false)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(bytes.TrimRight(b, "\n"))
}

// validateEnvelopeDoc checks one parsed envelope (a stream line when stream is true).
func validateEnvelopeDoc(v *validation, doc any, stream bool) {
	env, ok := doc.(map[string]any)
	if !ok {
		v.add("$", "is not a JSON object")
		return
	}
	plain := env
	if stream {
		plain = cloneMap(env)
		delete(plain, "type")
	}
	genericChecks(v, plain, "", false)
	isOK, okBool := env["ok"].(bool)
	if !okBool {
		v.add("ok", "must be a boolean")
		return
	}
	want := successKeys
	if !isOK {
		want = errorKeys
	}
	var optional []string
	if stream {
		optional = []string{"type"}
	}
	exactKeys(v, "$", env, want, optional)
	if stream {
		if t, _ := env["type"].(string); t != "item" && t != "summary" {
			v.add("type", "must be item or summary")
		}
	}
	if env["schema_version"] != SchemaVersion {
		v.add("schema_version", "must be %q", SchemaVersion)
	}
	validateMeta(v, env["meta"], isOK)
	if isOK {
		// The success budget holds for default invocations only (CLI-042), which
		// the envelope cannot tell: conformance R04 checks it on its own (Q24).
		if data, has := env["data"]; has {
			validateSuccessData(v, data)
		}
	} else {
		validateErrorObject(v, "error", env["error"])
		if size := compactSize(env); size > errorBudgetChars {
			v.add("$", "error envelope is %d characters (compact), over the %d budget", size, errorBudgetChars)
		}
	}
	if !v.failed() {
		checkUntrusted(v, env)
	}
}

// parseDocument parses one JSON document strictly: UTF-8 without a BOM, no
// duplicate keys, nothing after it.
func parseDocument(v *validation, raw []byte) (any, bool) {
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		v.add("$", "starts with a byte order mark")
		raw = raw[3:]
	}
	if !utf8.Valid(raw) {
		v.add("$", "is not valid UTF-8")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		v.add("$", "is not JSON: %v", err)
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		v.add("$", "holds more than one JSON document")
	}
	if err := checkDuplicateKeys(raw); err != nil {
		v.add("$", "%v", err)
	}
	return doc, true
}

// ValidateEnvelope checks one stdout document in JSON mode against the contract
// (VER-002): a single UTF-8 JSON object with a trailing newline and no BOM; the
// exact envelope keys; schema_version; meta, its notices and untrusted paths; the
// error object, its details per code (dispatch_state, the per-code rules, the
// receipt and batch rules against the code) and the error budget; and for success
// data the operation, dry-run, receipt, batch and page shapes with their semantic
// rules. The 8,000-character success budget applies to default invocations only
// (CLI-042), so conformance R04 checks it there, not this function (Q24).
func ValidateEnvelope(raw []byte) error {
	v := &validation{}
	if !bytes.HasSuffix(raw, []byte("\n")) || bytes.HasSuffix(raw, []byte("\r\n")) {
		v.add("$", "does not end with a single \\n")
	}
	if doc, ok := parseDocument(v, raw); ok {
		validateEnvelopeDoc(v, doc, false)
	}
	return v.err()
}

// ValidateStream checks the NDJSON output of a streaming command (CLI-006): every
// line a full envelope with type item or summary (no size budget per line), and
// the last line, and only the last, the summary.
func ValidateStream(raw []byte) error {
	v := &validation{}
	if !bytes.HasSuffix(raw, []byte("\n")) || bytes.Contains(raw, []byte("\r\n")) {
		v.add("$", "does not end with \\n, or holds \\r\\n")
	}
	var lines [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		v.add("$", "holds no lines")
	}
	for i, line := range lines {
		lv := &validation{}
		doc, ok := parseDocument(lv, line)
		if ok {
			validateEnvelopeDoc(lv, doc, true)
		}
		for _, prob := range lv.problems {
			v.problems = append(v.problems, fmt.Sprintf("line %d: %s", i+1, prob))
		}
		if lv.failed() {
			continue
		}
		typ := doc.(map[string]any)["type"]
		last := i == len(lines)-1
		switch {
		case last && typ != "summary":
			v.add(fmt.Sprintf("line %d", i+1), "the last line has type summary")
		case !last && typ != "item":
			v.add(fmt.Sprintf("line %d", i+1), "only the last line has type summary")
		}
	}
	return v.err()
}

// checkDuplicateKeys rejects duplicate keys anywhere in a JSON document.
func checkDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok && d == '{' {
		return walkObject(dec, nil, 0)
	}
	return nil
}

// ---------------------------------------------------------------- built-in data shapes

func runCheck(check func(v *validation)) error {
	v := &validation{}
	check(v)
	return v.err()
}

// valueMatches reports whether a JSON value fits a parameter type; json and enum
// take any value.
func valueMatches(typ string, x any) bool {
	switch typ {
	case "string", "path":
		_, ok := x.(string)
		return ok
	case "integer":
		_, ok := intOf(x)
		return ok
	case "number":
		switch x.(type) {
		case json.Number, float64, int, int64:
			return true
		}
		return false
	case "boolean":
		_, ok := x.(bool)
		return ok
	}
	return true
}

// distinctValues reports whether no two values of list have the same JSON encoding.
func distinctValues(list []any) bool {
	encoded := make([]string, len(list))
	for i, x := range list {
		b, _ := json.Marshal(x)
		encoded[i] = string(b)
	}
	return distinct(encoded)
}

// validateParam checks one reference parameter (or global flag) and
// check-contract.py's checks of its enum values and default (CLI-024).
func validateParam(v *validation, p string, x any, global bool) {
	if !exactKeys(v, p, x, paramRequiredKeys, paramOptionalKeys) {
		return
	}
	prm := x.(map[string]any)
	name, _ := prm["name"].(string)
	if !matches(flagNameRE, prm["name"]) {
		v.add(pj(p, "name"), "must be a kebab-case flag name")
	} else if !global && contains(excludedFromBinding, name) {
		v.add(pj(p, "name"), "--%s shadows a global flag (CLI-021)", name)
	}
	typ, _ := prm["type"].(string)
	if !contains(paramTypes, typ) {
		v.add(pj(p, "type"), "must be one of %s", strings.Join(paramTypes, ", "))
	}
	if kind := prm["kind"]; global && kind != "flag" {
		v.add(pj(p, "kind"), "must be flag")
	} else if kind != "flag" && kind != "positional" {
		v.add(pj(p, "kind"), "must be flag or positional")
	}
	if _, ok := prm["required"].(bool); !ok {
		v.add(pj(p, "required"), "must be a boolean")
	}
	for _, k := range []string{"multiple", "secret", "file_input"} {
		if val, has := prm[k]; has {
			if _, ok := val.(bool); !ok {
				v.add(pj(p, k), "must be a boolean")
			}
		}
	}
	for _, k := range []string{"unit", "description"} {
		if val, has := prm[k]; has && !isText(val) {
			v.add(pj(p, k), "must be a non-empty string")
		}
	}
	if e, has := prm["enum"]; has {
		if list, ok := e.([]any); !ok || len(list) == 0 || !distinctValues(list) {
			v.add(pj(p, "enum"), "must be a non-empty array of distinct values")
		}
	}
	if prm["required"] == true && hasKey(prm, "default") {
		v.add(pj(p, "default"), "is absent on a required parameter (CLI-024)")
	}
	if prm["secret"] == true && hasKey(prm, "default") {
		v.add(pj(p, "default"), "is absent on a secret: a secret is never echoed (CLI-024, SEC-021)")
	}
	if typ == "enum" && !hasKey(prm, "enum") {
		v.add(p, "missing key \"enum\" (type enum lists its values)")
	}
	if prm["file_input"] == true && typ != "path" {
		v.add(pj(p, "type"), "is path for a file input (CLI-048)")
	}
	if v.failed() {
		return
	}
	enum, _ := prm["enum"].([]any)
	for _, e := range enum {
		if typ != "enum" && !valueMatches(typ, e) {
			v.add(pj(p, "enum"), "--%s: enum values match type %s (CLI-024)", name, typ)
			break
		}
	}
	if d, has := prm["default"]; has {
		values := []any{d}
		if list, ok := d.([]any); ok && prm["multiple"] == true {
			values = list
		}
		for _, val := range values {
			if (typ == "enum" && !listHas(enum, val)) || (typ != "enum" && !valueMatches(typ, val)) {
				v.add(pj(p, "default"), "--%s: the default matches the parameter's type and values (CLI-024)", name)
				break
			}
		}
	}
}

// validateSchemaEntry checks one output schema of the catalog and
// check-contract.py's links: its default and untrusted fields are its fields
// (CLI-046).
func validateSchemaEntry(v *validation, p string, x any) {
	if !exactKeys(v, p, x, schemaEntryKeys, nil) {
		return
	}
	s := x.(map[string]any)
	if sh, _ := s["shape"].(string); !contains(schemaShapes, sh) {
		v.add(pj(p, "shape"), "must be one of %s", strings.Join(schemaShapes, ", "))
	}
	for _, k := range []string{"fields", "default_fields"} {
		if !matchesAll(snakeCase, s[k]) || !distinct(stringList(s[k])) {
			v.add(pj(p, k), "must be distinct snake_case field names")
			return
		}
	}
	untrusted := stringList(s["untrusted_fields"])
	if !matchesAll(fieldPathRE, s["untrusted_fields"]) || !distinct(untrusted) {
		v.add(pj(p, "untrusted_fields"), "must be distinct field paths")
		return
	}
	fields := stringList(s["fields"])
	var unknown []string
	for _, f := range stringList(s["default_fields"]) {
		if !contains(fields, f) {
			unknown = append(unknown, f)
		}
	}
	if len(unknown) > 0 {
		v.add(pj(p, "default_fields"), "default fields are fields of the schema; unknown: %s", strings.Join(unknown, ", "))
	}
	for i, path := range untrusted {
		if root, _, _ := strings.Cut(path, "."); !contains(fields, strings.TrimRight(root, "[]")) {
			v.add(pi(pj(p, "untrusted_fields"), i), "untrusted field '%s' is not a field of the schema (CLI-046)", path)
		}
	}
}

// validateErrorTable checks an error table and check-contract.py's rules for it:
// the core codes keep their contract exit and retryable, and are all listed when
// requireCore (CLI-010); exit 9 is only for human-action codes (CLI-018).
func validateErrorTable(v *validation, p string, x any, requireCore bool) {
	table, ok := x.(map[string]any)
	if !ok || len(table) == 0 {
		v.add(p, "must be a non-empty object of error codes")
		return
	}
	good := true
	for _, code := range sortedKeys(table) {
		cp := pj(p, code)
		if !codePattern.MatchString(code) {
			v.add(cp, "is not an error code")
		}
		if !exactKeys(v, cp, table[code], []string{"exit", "retryable"}, []string{"meaning"}) {
			good = false
			continue
		}
		entry := table[code].(map[string]any)
		exit, isInt := intOf(entry["exit"])
		if _, documented := exitCodeMeanings[int(exit)]; !isInt || !documented || exit == 0 {
			v.add(pj(cp, "exit"), "is not a documented failure exit code")
			good = false
		}
		if _, ok := entry["retryable"].(bool); !ok {
			v.add(pj(cp, "retryable"), "must be a boolean")
			good = false
		}
		if m, has := entry["meaning"]; has && !isText(m) {
			v.add(pj(cp, "meaning"), "must be a non-empty string")
		}
	}
	if !good {
		return
	}
	for _, code := range sortedKeys(coreCodes) {
		spec := coreCodes[code]
		entry, listed := table[code].(map[string]any)
		if !listed {
			if requireCore {
				v.add(p, "the error table lists every core code; missing %s (CLI-010)", code)
			}
			continue
		}
		if exit, _ := intOf(entry["exit"]); int(exit) != spec.exit || entry["retryable"] != spec.retryable {
			v.add(pj(p, code), "%s keeps exit %d and retryable %v (CLI-010)", code, spec.exit, spec.retryable)
		}
	}
	for _, code := range sortedKeys(table) {
		exit, _ := intOf(table[code].(map[string]any)["exit"])
		if _, core := coreCodes[code]; !core && exit == 9 && !strings.HasSuffix(code, "_REQUIRED") {
			v.add(pj(p, code), "%s: exit 9 is reserved for human-action codes (CLI-018)", code)
		}
	}
}

// validateTree checks the command tree (CLI-021): paths are unique, a path is a
// command or a group but not both, and no alias collides with a path or another
// alias.
func validateTree(v *validation, p string, paths []string, aliases map[string][]string) {
	count := map[string]int{}
	for _, path := range paths {
		count[path]++
	}
	var repeated []string
	for _, path := range sortedKeys(count) {
		if count[path] > 1 {
			repeated = append(repeated, path)
		}
	}
	if len(repeated) > 0 {
		v.add(p, "command paths are unique (CLI-021); repeated: %s", strings.Join(repeated, ", "))
	}
	for _, path := range sortedKeys(count) {
		for _, other := range paths {
			if strings.HasPrefix(other, path+" ") {
				v.add(p, "'%s' is both a command and a group; a path is one or the other (CLI-021)", path)
				break
			}
		}
	}
	// Aliases are per parent (P1): `label create` and `milestone create` may both have
	// the alias add. An alias collides only with the name of a sibling (a command or a
	// group under the same parent) or another sibling's alias, and a root alias also
	// with a built-in name.
	children := map[string]map[string]bool{}
	for _, path := range paths {
		words := strings.Fields(path)
		for i := range words {
			parent := strings.Join(words[:i], " ")
			if children[parent] == nil {
				children[parent] = map[string]bool{}
			}
			children[parent][words[i]] = true
		}
	}
	taken := map[string]bool{}
	for _, path := range sortedKeys(aliases) {
		words := strings.Fields(path)
		if len(words) == 0 {
			continue
		}
		parent := strings.Join(words[:len(words)-1], " ")
		for _, alias := range aliases[path] {
			qualified := strings.TrimSpace(parent + " " + alias)
			if children[parent][alias] || taken[qualified] || (parent == "" && isReservedRoot(alias)) {
				v.add(p, "alias '%s' collides with a command path or another alias (CLI-021)", qualified)
			}
			taken[qualified] = true
		}
	}
}

// isReservedRoot reports the top-level names only the built-ins take (CLI-021):
// the first words of the built-in paths, help and version.
func isReservedRoot(name string) bool {
	for _, c := range builtinCommands {
		if root, _, _ := strings.Cut(c, " "); root == name {
			return true
		}
	}
	return name == "help" || name == "version"
}

// validateCommandEntry checks one reference command and check-contract.py's links
// and examples. schemas and table are nil when the caller checks the links itself.
func validateCommandEntry(v *validation, p string, x, tool any, schemas, table map[string]any) {
	if !exactKeys(v, p, x, referenceCommandKeys, nil) {
		return
	}
	c := x.(map[string]any)
	path, _ := c["path"].(string)
	if !commandPathRE.MatchString(path) {
		v.add(pj(p, "path"), "must be a command path")
		return
	}
	aliases := stringList(c["aliases"])
	if !matchesAll(commandPathRE, c["aliases"]) || !distinct(aliases) {
		v.add(pj(p, "aliases"), "must be distinct command paths")
		aliases = nil
	}
	if !isText(c["summary"]) {
		v.add(pj(p, "summary"), "must be a non-empty string")
	}
	typ := c["type"]
	if typ != "read" && typ != "write" {
		v.add(pj(p, "type"), "must be read or write")
	}
	validateEffects(v, pj(p, "effects"), c["effects"], typ == "write")
	if typ == "read" {
		effects, _ := c["effects"].([]any)
		for _, e := range effects {
			if e != string(Observe) {
				v.add(pj(p, "effects"), "a read only observes (CLI-025)")
				break
			}
		}
	}
	if _, ok := c["dangerous"].(bool); !ok {
		v.add(pj(p, "dangerous"), "must be a boolean")
	}
	dangerousRule(v, p, c)
	m, _ := c["maturity"].(string)
	if !contains(maturityValues, m) {
		v.add(pj(p, "maturity"), "must be one of %s", strings.Join(maturityValues, ", "))
	}
	evidence := stringList(c["evidence"])
	if !matchesAll(evidenceRE, c["evidence"]) {
		v.add(pj(p, "evidence"), "must be evidence references <kind>:<path> (VER-005)")
		evidence = nil
	}
	if (m == "verified" || m == "stable") && len(evidence) == 0 {
		v.add(pj(p, "evidence"), "verified and stable commands cite their evidence (CLI-027, VER-005)")
	}
	params, ok := c["params"].([]any)
	if !ok {
		v.add(pj(p, "params"), "must be an array")
	}
	positionals := 0
	for i, prm := range params {
		validateParam(v, pi(pj(p, "params"), i), prm, false)
		if pm, ok := prm.(map[string]any); ok && pm["kind"] == "positional" {
			positionals++
		}
	}
	if positionals > 1 {
		v.add(pj(p, "params"), "a command binds at most one positional argument (CLI-023)")
	}
	if !matches(snakeCase, c["output"]) {
		v.add(pj(p, "output"), "must be a snake_case schema name")
	}
	codes := stringList(c["error_codes"])
	if !matchesAll(codePattern, c["error_codes"]) || len(codes) == 0 || !distinct(codes) {
		v.add(pj(p, "error_codes"), "must be a non-empty array of distinct error codes")
		codes = nil
	}
	examples := stringList(c["examples"])
	if !textList(c["examples"]) || len(examples) == 0 {
		v.add(pj(p, "examples"), "must be a non-empty array of strings")
		examples = nil
	}
	if v.failed() {
		return
	}
	if output, _ := c["output"].(string); schemas != nil && !hasKey(schemas, output) {
		v.add(pj(p, "output"), "%s: output names an entry of schemas (CLI-020)", path)
	}
	var undeclared []string
	for _, code := range codes {
		if table != nil && !hasKey(table, code) {
			undeclared = append(undeclared, code)
		}
	}
	if len(undeclared) > 0 {
		v.add(pj(p, "error_codes"), "%s: error codes are declared in error_codes: %s (CLI-026)", path, strings.Join(undeclared, ", "))
	}
	names := make([]string, len(params))
	for i, prm := range params {
		names[i], _ = prm.(map[string]any)["name"].(string)
	}
	if !distinct(names) {
		v.add(pj(p, "params"), "%s: parameter names are unique", path)
	}
	if t, ok := tool.(string); ok {
		// An alias replaces the last word of the path (aliases are per parent, P1).
		parent := ""
		if i := strings.LastIndex(path, " "); i >= 0 {
			parent = path[:i] + " "
		}
		forms := []string{path}
		for _, al := range aliases {
			forms = append(forms, parent+al)
		}
		for i, e := range examples {
			runnable := false
			for _, q := range forms {
				runnable = runnable || e == t+" "+q || strings.HasPrefix(e, t+" "+q+" ")
			}
			if !runnable {
				v.add(pi(pj(p, "examples"), i), "%s: an example is a runnable call that starts with '%s %s'", path, t, path)
			}
		}
	}
	if path == selfUpdate {
		if anyContains(examples, "--confirm") {
			v.add(pj(p, "examples"), "%s is outside the confirm gate and never takes --confirm (CLI-070)", path)
		}
	} else if typ == "write" && (!anyContains(examples, "--dry-run") || !anyContains(examples, "--confirm")) {
		v.add(pj(p, "examples"), "%s: a write command's examples show the --dry-run and --confirm pair (CLI-030)", path)
	}
	if c["dangerous"] == true {
		for _, e := range examples {
			if !strings.Contains(e, "--dangerous") {
				v.add(pj(p, "examples"), "%s: a dangerous command's examples carry --dangerous (CLI-056)", path)
				break
			}
		}
	}
}

// validateReferenceFull checks the full reference (CLI-033): its schema and
// check-contract.py's checks of the tree, built-ins, error table, risk tier, links
// and examples.
func validateReferenceFull(data map[string]any) error {
	return runCheck(func(v *validation) {
		if !exactKeys(v, "data", data, referenceRequiredKeys, nil) {
			return
		}
		if !matches(toolNameRE, data["tool"]) {
			v.add("data.tool", "must be a kebab-case tool name")
		}
		if !matches(semverPattern, data["version"]) {
			v.add("data.version", "must be a semantic version")
		}
		if sv, _ := data["spec_version"].(string); !semverPattern.MatchString(sv) || !strings.HasPrefix(sv, "2.") {
			v.add("data.spec_version", "must be a 2.x.y semantic version")
		}
		if data["schema_version"] != SchemaVersion {
			v.add("data.schema_version", "must be %q", SchemaVersion)
		}
		tier, _ := data["risk_tier"].(string)
		if last := len(riskTierValues) - 1; !contains(riskTierValues, tier) {
			v.add("data.risk_tier", "must be %s or %s", strings.Join(riskTierValues[:last], ", "), riskTierValues[last])
		}
		flags, ok := data["global_flags"].([]any)
		if !ok || len(flags) == 0 {
			v.add("data.global_flags", "must be a non-empty array")
		}
		for i, f := range flags {
			validateParam(v, pi("data.global_flags", i), f, true)
		}
		schemas, ok := data["schemas"].(map[string]any)
		if !ok || len(schemas) == 0 {
			v.add("data.schemas", "must be a non-empty object")
		}
		for _, name := range sortedKeys(schemas) {
			if !snakeCase.MatchString(name) {
				v.add(pj("data.schemas", name), "is not a snake_case schema name")
			}
			validateSchemaEntry(v, pj("data.schemas", name), schemas[name])
		}
		table, _ := data["error_codes"].(map[string]any)
		validateErrorTable(v, "data.error_codes", data["error_codes"], true)
		exits, ok := data["exit_codes"].(map[string]any)
		for _, k := range sortedKeys(exits) {
			ok = ok && exitKeyRE.MatchString(k) && isText(exits[k])
		}
		if !ok || len(exits) == 0 {
			v.add("data.exit_codes", "must map exit codes to their meaning")
		}
		cmds, ok := data["commands"].([]any)
		if !ok || len(cmds) == 0 {
			v.add("data.commands", "must list the commands")
			return
		}
		for i, c := range cmds {
			validateCommandEntry(v, pi("data.commands", i), c, data["tool"], schemas, table)
		}
		// From here on every part is well-formed, the error table and exit codes too.
		if v.failed() {
			return
		}
		paths := make([]string, len(cmds))
		aliases := map[string][]string{}
		byPath := map[string]map[string]any{}
		for i, c := range cmds {
			cm := c.(map[string]any)
			paths[i], _ = cm["path"].(string)
			aliases[paths[i]] = append(aliases[paths[i]], stringList(cm["aliases"])...)
			byPath[paths[i]] = cm
		}
		validateTree(v, "data.commands", paths, aliases)
		for i, path := range paths {
			if root, _, _ := strings.Cut(path, " "); isReservedRoot(root) && !contains(builtinCommands, path) {
				v.add(pi("data.commands", i), "'%s' is a reserved built-in name (CLI-021)", root)
			}
		}
		for _, name := range builtinCommands {
			if name == selfUpdate {
				continue
			}
			c, listed := byPath[name]
			effects := stringList(c["effects"])
			if !listed {
				v.add("data.commands", "the built-in '%s' is part of the command tree (CLI-021, CLI-031, CLI-061)", name)
			} else if c["type"] != "read" || len(effects) != 1 || effects[0] != string(Observe) || c["dangerous"] == true {
				v.add("data.commands", "the built-in '%s' is a read that only observes", name)
			}
		}
		used := map[int]bool{0: true}
		for _, entry := range table {
			exit, _ := intOf(entry.(map[string]any)["exit"])
			used[int(exit)] = true
		}
		var missing []int
		for exit := range used {
			if !hasKey(exits, strconv.Itoa(exit)) {
				missing = append(missing, exit)
			}
		}
		if len(missing) > 0 {
			sort.Ints(missing)
			list := make([]string, len(missing))
			for i, exit := range missing {
				list[i] = strconv.Itoa(exit)
			}
			v.add("data.exit_codes", "exit_codes explains every exit the error table uses; missing %s", strings.Join(list, ", "))
		}
		need := 0
		for i, c := range cmds {
			if paths[i] == selfUpdate {
				continue
			}
			for _, e := range stringList(c.(map[string]any)["effects"]) {
				need = max(need, tierRank(minimumRiskTier[e]))
			}
		}
		if need > tierRank(tier) {
			v.add("data.risk_tier", "the commands' effects need at least %s; the tool declares %s (SEC-001)", riskTierValues[need], tier)
		}
		var names, missingFlags []string
		for _, f := range flags {
			name, _ := f.(map[string]any)["name"].(string)
			names = append(names, name)
		}
		for _, f := range globalFlags {
			if !contains(names, f) {
				missingFlags = append(missingFlags, f)
			}
		}
		if len(missingFlags) > 0 || !distinct(names) {
			list := strings.Join(missingFlags, ", ")
			if list == "" {
				list = "none"
			}
			v.add("data.global_flags", "lists each control flag once; missing %s", list)
		}
	})
}

// validateReferenceIndex checks reference --index data (CLI-031).
func validateReferenceIndex(data map[string]any) error {
	return runCheck(func(v *validation) {
		requireKeys(v, "data", data, []string{"tool", "version"})
		var paths []string
		switch {
		case hasKey(data, "groups"):
			if hasKey(data, "commands") {
				v.add("data", "has commands or groups, not both")
			}
			list, _ := data["groups"].([]any)
			for i, g := range list {
				if exactKeys(v, pi("data.groups", i), g, indexGroupKeys, nil) {
					p, _ := g.(map[string]any)["path"].(string)
					paths = append(paths, p)
				}
			}
			if !distinct(paths) {
				v.add("data.groups", "group paths are unique (CLI-031)")
			}
		case hasKey(data, "commands"):
			list, _ := data["commands"].([]any)
			for i, c := range list {
				cp := pi("data.commands", i)
				if exactKeys(v, cp, c, indexCommandKeys, nil) {
					co := c.(map[string]any)
					validateEffects(v, pj(cp, "effects"), co["effects"], co["type"] == "write")
					dangerousRule(v, cp, co)
					p, _ := co["path"].(string)
					paths = append(paths, p)
				}
			}
			validateTree(v, "data.commands", paths, nil)
		default:
			v.add("data", "needs commands or groups")
		}
	})
}

// validateReferenceCommand checks reference --command data (CLI-032).
func validateReferenceCommand(data map[string]any) error {
	return runCheck(func(v *validation) {
		if !exactKeys(v, "data", data, []string{"tool", "version", "command", "schemas", "error_codes"}, nil) {
			return
		}
		validateCommandEntry(v, "data.command", data["command"], data["tool"], nil, nil)
		cmd, _ := data["command"].(map[string]any)
		schemas, _ := data["schemas"].(map[string]any)
		output, _ := cmd["output"].(string)
		if len(schemas) != 1 || !hasKey(schemas, output) {
			v.add("data.schemas", "holds exactly the entry the command's output names (CLI-032)")
		}
		for _, name := range sortedKeys(schemas) {
			validateSchemaEntry(v, "data.schemas."+name, schemas[name])
		}
		validateErrorTable(v, "data.error_codes", data["error_codes"], false)
		codes, _ := data["error_codes"].(map[string]any)
		listed := stringList(cmd["error_codes"])
		same := len(codes) == len(listed)
		for _, code := range sortedKeys(codes) {
			same = same && contains(listed, code)
		}
		if !same {
			v.add("data.error_codes", "holds exactly the codes the command lists (CLI-032)")
		}
	})
}

// validateContext checks context data (CLI-035).
func validateContext(data map[string]any) error {
	return runCheck(func(v *validation) {
		requireKeys(v, "data", data, contextRequiredKeys)
		creds, _ := data["credentials"].(map[string]any)
		requireKeys(v, "data.credentials", creds, contextCredentialsKeys)
		if exactKeys(v, "data.state", data["state"], contextStateKeys, []string{"reason"}) {
			state := data["state"].(map[string]any)
			prot := state["protection"]
			if s, isStr := prot.(string); prot != nil && (!isStr || !contains(stateProtectionValues, s)) {
				v.add("data.state.protection", "must be one of %s", strings.Join(stateProtectionValues, ", "))
			}
			if state["available"] == true && (!isText(state["dir"]) || prot == nil) {
				v.add("data.state", "an available state store names its dir and protection")
			}
			if state["dir"] == "" {
				v.add("data.state.dir", "is null when unknown, never empty (decision 15)")
			} else if dir, ok := state["dir"].(string); ok && !isAbsoluteAny(dir) {
				v.add("data.state.dir", "the state directory is reported as an absolute path (SEC-030)")
			}
			if r, has := state["reason"]; has && !isText(r) {
				v.add("data.state.reason", "must be a non-empty string")
			}
		}
		if prof, has := data["profile"]; has {
			validateProfile(v, "data.profile", prof)
		}
		if !exactKeys(v, "data.authorization", data["authorization"], contextAuthorizationKeys, nil) {
			return
		}
		auth := data["authorization"].(map[string]any)
		if path, ok := auth["path"].(string); ok && !isAbsoluteAny(path) {
			v.add("data.authorization.path", "the scope path is absolute (SEC-011)")
		}
		status, _ := auth["status"].(string)
		if !contains(contextAuthorizationStatus, status) {
			v.add("data.authorization.status", "must be one of %s", strings.Join(contextAuthorizationStatus, ", "))
		}
		switch status {
		case "granted":
			if !isText(auth["scope_id"]) || len(stringList(auth["effects"])) == 0 {
				v.add("data.authorization", "a granted scope is named and grants at least one effect")
			}
		case "missing", "invalid":
			if auth["scope_id"] != nil || len(stringList(auth["effects"])) > 0 || auth["dangerous"] != false || auth["expires_at"] != nil {
				v.add("data.authorization", "without a readable scope nothing is granted")
			}
		case "expired", "revoked", "budget_spent":
			if !isText(auth["scope_id"]) {
				v.add("data.authorization.scope_id", "names the scope that was read")
			}
		}
	})
}

// validateProfile checks context.profile (CLI-101): exactly its keys, profile
// names, a known source, and a name exactly when a profile was chosen.
func validateProfile(v *validation, p string, x any) {
	if !exactKeys(v, p, x, contextProfileKeys, nil) {
		return
	}
	prof := x.(map[string]any)
	name := prof["name"]
	if name != nil && !matches(profileNamePattern, name) {
		v.add(pj(p, "name"), "must be a profile name or null")
	}
	source, _ := prof["source"].(string)
	if !contains(profileSourceValues, source) {
		v.add(pj(p, "source"), "must be one of %s", strings.Join(profileSourceValues, ", "))
	}
	if !matchesAll(profileNamePattern, prof["profiles"]) || !distinct(stringList(prof["profiles"])) {
		v.add(pj(p, "profiles"), "must be distinct profile names")
	}
	if f := prof["file"]; f != nil && !isText(f) {
		v.add(pj(p, "file"), "must be a non-empty string or null")
	}
	if contains(profileSourceValues, source) && (source == "none") != (name == nil) {
		v.add(pj(p, "name"), "is null exactly when source is none: a profile is named when one was chosen (CLI-101)")
	}
}

// validateDoctor checks doctor data and its required checks (CLI-036).
func validateDoctor(data map[string]any) error {
	return runCheck(func(v *validation) {
		if !exactKeys(v, "data", data, doctorDataKeys, nil) {
			return
		}
		list, _ := data["checks"].([]any)
		var seen []string
		for i, c := range list {
			cp := pi("data.checks", i)
			if !exactKeys(v, cp, c, doctorCheckKeys, nil) {
				continue
			}
			obj := c.(map[string]any)
			status, _ := obj["status"].(string)
			if !contains(doctorStatusValues, status) {
				v.add(pj(cp, "status"), "must be one of %s", strings.Join(doctorStatusValues, ", "))
			}
			if !isText(obj["message"]) {
				v.add(pj(cp, "message"), "must be a non-empty string")
			}
			switch {
			case status == "pass" && obj["fix"] != nil:
				v.add(pj(cp, "fix"), "is null when the check passes")
			case status == "fail" && !isText(obj["fix"]):
				v.add(pj(cp, "fix"), "says how to fix a failed check (decision 15)")
			case obj["fix"] != nil && !isText(obj["fix"]):
				v.add(pj(cp, "fix"), "must be a non-empty string or null")
			}
			name, _ := obj["check"].(string)
			seen = append(seen, name)
		}
		var missing []string
		for _, r := range doctorRequiredChecks {
			if !contains(seen, r) {
				missing = append(missing, r)
			}
		}
		if len(missing) > 0 {
			v.add("data.checks", "missing required checks %s", strings.Join(missing, ", "))
		}
		if !distinct(seen) {
			v.add("data.checks", "check names are unique (CLI-036)")
		}
	})
}

// validateChangelog checks changelog data (CLI-037).
func validateChangelog(data map[string]any) error {
	return runCheck(func(v *validation) {
		if !exactKeys(v, "data", data, changelogRequiredKeys, changelogOptionalKeys) {
			return
		}
		curText, _ := data["current_version"].(string)
		cur, curErr := ParseVersion(curText)
		if curErr != nil || !semverPattern.MatchString(curText) {
			v.add("data.current_version", "must be a semantic version")
		}
		var since *Version
		if s, has := data["since"]; has {
			sinceText, _ := s.(string)
			sv, err := ParseVersion(sinceText)
			if err != nil || !semverPattern.MatchString(sinceText) {
				v.add("data.since", "must be a semantic version")
			} else {
				since = &sv
			}
		}
		entries, ok := data["entries"].([]any)
		if !ok {
			v.add("data.entries", "must be an array")
		}
		if c, has := data["count"]; has {
			if n, ok := intOf(c); !ok || n != int64(len(entries)) {
				v.add("data.count", "equals the number of entries")
			}
		}
		// A changelog cut to the size budget says so (CLI-041, CLI-042).
		if t, has := data["truncated"]; has && t != true {
			v.add("data.truncated", "is true when present")
		}
		if data["truncated"] == true {
			if !isText(data["truncated_reason"]) {
				v.add("data.truncated_reason", "a truncated changelog says why (CLI-041)")
			}
			if n, ok := intOf(data["omitted"]); !ok || n < 1 {
				v.add("data.omitted", "counts the entries left out, a positive integer")
			}
		} else if hasKey(data, "truncated_reason") || hasKey(data, "omitted") {
			v.add("data", "truncated_reason and omitted come with truncated: true")
		}
		var versions []Version
		for i, e := range entries {
			ep := pi("data.entries", i)
			if !exactKeys(v, ep, e, changelogEntryKeys, nil) {
				continue
			}
			eo := e.(map[string]any)
			verText, _ := eo["version"].(string)
			ver, err := ParseVersion(verText)
			if err != nil || !semverPattern.MatchString(verText) {
				v.add(pj(ep, "version"), "must be a semantic version")
			} else {
				versions = append(versions, ver)
				if curErr == nil && CompareVersions(ver, cur) > 0 {
					v.add(pj(ep, "version"), "no entry is newer than current_version")
				}
				if since != nil && CompareVersions(ver, *since) <= 0 {
					v.add(pj(ep, "version"), "--since returns only versions strictly newer than since (CLI-037)")
				}
			}
			if d := eo["date"]; d != nil && !matches(dateOnly, d) {
				v.add(pj(ep, "date"), "must be YYYY-MM-DD or null")
			}
			if exactKeys(v, pj(ep, "changes"), eo["changes"], nil, changelogCategories) {
				changes := eo["changes"].(map[string]any)
				for _, k := range sortedKeys(changes) {
					if !textList(changes[k]) {
						v.add(pj(pj(ep, "changes"), k), "must be an array of non-empty strings")
					}
				}
			}
		}
		for i := 1; i < len(versions); i++ {
			if CompareVersions(versions[i-1], versions[i]) <= 0 {
				v.add("data.entries", "entries are unique and newest first (CLI-037)")
				break
			}
		}
	})
}

// validateOperationView checks an operation show view (CLI-061).
func validateOperationView(data map[string]any) error {
	return runCheck(func(v *validation) { validateOperation(v, "data", data) })
}

// validateScopeFile checks a scope object with the same rules loadScope applies.
func validateScopeFile(raw []byte) error {
	if _, reason := parseScope(raw); reason != "" {
		return errors.New(reason)
	}
	return nil
}
