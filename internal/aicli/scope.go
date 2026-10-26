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
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Scope files (SEC-010 .. SEC-012): a person writes one to grant this tool effects
// on targets for a while; the CLI only reads it and never creates or edits it.
// Without a valid scope every write is refused before anything is sent.
//
// Protection is honest (SEC-013): a scope stops injected instructions, agent drift
// and wrong targets; it cannot stop an agent running as the same OS user from
// editing the file.

const maxScopeBytes = 64 << 10

type scopeFile struct {
	ScopeID      string            `json:"scope_id"`
	Tool         string            `json:"tool"`
	GrantedBy    string            `json:"granted_by"`
	GrantedAt    string            `json:"granted_at"`
	ExpiresAt    *string           `json:"expires_at"`
	Effects      []string          `json:"effects"`
	Dangerous    bool              `json:"dangerous"`
	Targets      *[]targetSelector `json:"targets"`
	Destinations []string          `json:"destinations"`
	Outputs      []string          `json:"outputs"`
	Roots        []string          `json:"roots"`
	Limits       *scopeLimits      `json:"limits"`
	Revoked      bool              `json:"revoked"`
	Notes        string            `json:"notes"`
}

type targetSelector struct {
	Key   string `json:"key"`
	Match string `json:"match"`
}

type scopeLimits struct {
	MaxOperations *int `json:"max_operations"`
}

// scopeState is the resolved scope: granted, missing, invalid, expired or revoked.
type scopeState struct {
	status string
	path   string
	digest string
	file   *scopeFile
	reason string
}

func (s scopeState) id() *string {
	if s.file != nil && s.status != "invalid" && s.file.ScopeID != "" {
		return strPtr(s.file.ScopeID)
	}
	return nil
}

// scopePath resolves the scope file: <PREFIX>_SCOPE_FILE, else <state>/scope.json.
// There is deliberately no flag (SEC-011). The variable must hold an absolute path
// (C4): a relative one would resolve against whatever directory the agent runs in,
// so loadScope refuses it.
func (a *App) scopePath(stateDir string) string {
	path := filepath.Join(stateDir, "scope.json")
	if p := strings.TrimSpace(a.env(a.envPrefix() + "_SCOPE_FILE")); p != "" {
		path = p
	}
	a.debugOnce("scope file: %s", path)
	return path
}

// loadScope reads and strictly validates the scope file.
func (a *App) loadScope(stateDir string) scopeState {
	path := a.scopePath(stateDir)
	st := scopeState{path: path}
	invalid := func(reason string) scopeState {
		st.status = "invalid"
		st.reason = reason
		return st
	}
	if !filepath.IsAbs(path) {
		return invalid(a.envPrefix() + "_SCOPE_FILE must be an absolute path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		st.status = "missing"
		return st
	}
	if err != nil {
		return invalid("it cannot be read")
	}
	mode := info.Mode()
	if mode&fs.ModeSymlink != 0 || mode&fs.ModeIrregular != 0 || !mode.IsRegular() {
		return invalid("it must be a regular file, not a link")
	}
	if reason := fileTrustProblem(path, info); reason != "" {
		return invalid(reason)
	}
	if info.Size() > maxScopeBytes {
		return invalid("it is larger than 64 KiB")
	}
	raw, err := readLimited(path, maxScopeBytes)
	if err != nil {
		return invalid("it cannot be read")
	}
	if len(raw) > maxScopeBytes {
		return invalid("it is larger than 64 KiB")
	}
	st.digest = "sha256:" + sha256Hex(raw)
	file, reason := parseScope(raw)
	if reason != "" {
		return invalid(reason)
	}
	st.file = file
	if file.Tool != a.toolName() {
		return invalid(fmt.Sprintf("it names the tool %q, not %q", truncateRunes(file.Tool, 40), a.toolName()))
	}
	// A granted directory reached through a junction or another reparse point could
	// lead anywhere, so such a grant is no grant (C2, SEC-012).
	for _, dir := range append(append([]string{}, file.Outputs...), file.Roots...) {
		if _, reason := resolvePath(dir); reason != "" {
			return invalid(reason)
		}
	}
	if file.Revoked {
		st.status = "revoked"
		return st
	}
	if file.ExpiresAt != nil {
		exp, _ := parseTime(*file.ExpiresAt)
		if !a.now().Before(exp) {
			st.status = "expired"
			return st
		}
	}
	st.status = "granted"
	return st
}

// parseScope parses a scope file strictly: one UTF-8 JSON object without a BOM,
// duplicate keys and unknown keys rejected, required keys present and typed.
func parseScope(raw []byte) (*scopeFile, string) {
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		return nil, "it starts with a byte order mark"
	}
	if !utf8.Valid(raw) {
		return nil, "it is not valid UTF-8"
	}
	top, err := strictKeys(raw)
	if err != nil {
		return nil, err.Error()
	}
	for _, k := range scopeRequiredKeys {
		if !top[k] {
			return nil, fmt.Sprintf("the required key %q is missing", k)
		}
	}
	if reason := scopeKeyProblem(raw); reason != "" {
		return nil, reason
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f scopeFile
	if err := dec.Decode(&f); err != nil {
		return nil, scopeDecodeReason(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, "it holds more than one JSON value"
	}
	switch {
	case strings.TrimSpace(f.ScopeID) == "":
		return nil, "scope_id is empty"
	case strings.TrimSpace(f.GrantedBy) == "":
		return nil, "granted_by is empty"
	}
	granted, ok := parseTimeAny(f.GrantedAt)
	if !ok {
		return nil, "granted_at is not an ISO 8601 UTC time"
	}
	if f.ExpiresAt != nil {
		expires, ok := parseTimeAny(*f.ExpiresAt)
		if !ok {
			return nil, "expires_at is not an ISO 8601 UTC time or null"
		}
		if !expires.After(granted) {
			return nil, "expires_at is not after granted_at"
		}
	}
	if f.Effects == nil {
		return nil, "effects must be an array"
	}
	for _, e := range f.Effects {
		if !contains(effectValues, e) {
			return nil, fmt.Sprintf("effects lists an unknown effect %q", truncateRunes(e, 40))
		}
	}
	if top["targets"] && f.Targets == nil {
		return nil, "targets must be an array; leave it out to allow every target"
	}
	if f.Targets != nil {
		for _, t := range *f.Targets {
			if t.Key == "" {
				return nil, "a targets entry has an empty key"
			}
			if t.Match != "" && !contains(targetMatchValues, t.Match) {
				return nil, "a targets entry has an unknown match; use subtree or exact"
			}
		}
	}
	for _, d := range f.Destinations {
		if strings.TrimSpace(d) == "" {
			return nil, "destinations holds an empty entry"
		}
	}
	for _, p := range append(append([]string{}, f.Outputs...), f.Roots...) {
		if !filepath.IsAbs(p) {
			return nil, "outputs and roots must be absolute paths"
		}
	}
	if f.Limits != nil && f.Limits.MaxOperations != nil && *f.Limits.MaxOperations < 1 {
		return nil, "limits.max_operations must be at least 1"
	}
	return &f, ""
}

// scopeDecodeReason says what is wrong with a scope file in the file's own terms,
// not the decoder's: an unknown key (with the closest known one) or a value of the
// wrong type.
func scopeDecodeReason(err error) string {
	if rest, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		key := strings.Trim(rest, `"`)
		known := append(append([]string{}, scopeRequiredKeys...), scopeOptionalKeys...)
		known = append(known, "key", "match", "max_operations")
		reason := fmt.Sprintf("unknown key `%s`", truncateRunes(key, 40))
		if near := closestWords(key, known); len(near) > 0 {
			reason += fmt.Sprintf(" (did you mean `%s`?)", near[0])
		}
		return reason
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		want := "another type"
		switch te.Type.Kind() {
		case reflect.String:
			want = "a string"
		case reflect.Bool:
			want = "true or false"
		case reflect.Int, reflect.Int64, reflect.Pointer:
			want = "a number"
		case reflect.Slice:
			want = "an array"
		case reflect.Struct:
			want = "an object"
		}
		field := te.Field
		if field == "" {
			field = "a value"
		}
		return fmt.Sprintf("%s must be %s, not %s", field, want, te.Value)
	}
	return "it does not match the scope format"
}

// scopeKeyProblem rejects a key that is not spelled exactly as the scope format
// spells it (C3, SEC-011). encoding/json matches object keys to struct fields without
// regard to case, so "Dangerous": true beside "dangerous": false would decode as a
// grant of dangerous: the keys are checked on the raw document first.
func scopeKeyProblem(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var top map[string]any
	if dec.Decode(&top) != nil {
		return "" // the decoder reports what is wrong with the document
	}
	if reason := unknownKey(top, append(append([]string{}, scopeRequiredKeys...), scopeOptionalKeys...)); reason != "" {
		return reason
	}
	if list, ok := top["targets"].([]any); ok {
		for _, t := range list {
			if m, ok := t.(map[string]any); ok {
				if reason := unknownKey(m, append(append([]string{}, targetSelectorRequiredKeys...), targetSelectorOptionalKeys...)); reason != "" {
					return reason
				}
			}
		}
	}
	if m, ok := top["limits"].(map[string]any); ok {
		return unknownKey(m, limitsKeys)
	}
	return ""
}

// unknownKey names the first key of m that is not exactly one of known, with the
// closest known key.
func unknownKey(m map[string]any, known []string) string {
	for _, k := range sortedKeys(m) {
		if contains(known, k) {
			continue
		}
		reason := fmt.Sprintf("unknown key `%s`", truncateRunes(k, 40))
		if near := closestWords(k, known); len(near) > 0 {
			reason += fmt.Sprintf(" (did you mean `%s`?)", near[0])
		} else if near := closestWords(strings.ToLower(k), known); len(near) > 0 {
			reason += fmt.Sprintf(" (did you mean `%s`?)", near[0])
		}
		return reason
	}
	return ""
}

// fileTrustProblem says why a file a person writes (the scope file, the settings
// file) cannot be trusted on POSIX systems (C4): it is writable by its group or by
// others, or another user owns it. "" when it can be trusted; on Windows the profile
// ACL decides and nothing is checked here.
func fileTrustProblem(path string, info fs.FileInfo) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if info.Mode().Perm()&0o022 != 0 {
		return path + " is writable by other users; only its owner may write it"
	}
	if !ownedByCurrentUser(info) {
		return path + " is not owned by the current user"
	}
	return ""
}

// readLimited reads at most limit+1 bytes of path, so a caller can tell a file that
// grew past its cap from one that fits.
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// strictKeys walks a JSON document, rejecting duplicate keys at any depth, and
// returns the keys of the top-level object.
func strictKeys(raw []byte) (map[string]bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, errors.New("it is not JSON")
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("it is not a JSON object")
	}
	top := map[string]bool{}
	if err := walkObject(dec, top, 0); err != nil {
		return nil, err
	}
	return top, nil
}

func walkObject(dec *json.Decoder, keys map[string]bool, depth int) error {
	if depth > 32 {
		return errors.New("it is nested too deeply")
	}
	if keys == nil {
		keys = map[string]bool{}
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return errors.New("it is not valid JSON")
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("it is not valid JSON")
		}
		if keys[key] {
			return fmt.Errorf("the key %q appears twice", truncateRunes(key, 40))
		}
		keys[key] = true
		if err := walkValue(dec, depth+1); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return errors.New("it is not valid JSON")
	}
	return nil
}

func walkValue(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return errors.New("it is not valid JSON")
	}
	if d, ok := tok.(json.Delim); ok {
		switch d {
		case '{':
			return walkObject(dec, nil, depth)
		case '[':
			for dec.More() {
				if err := walkValue(dec, depth+1); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return errors.New("it is not valid JSON")
			}
		}
	}
	return nil
}

// scopeNeed is what one write needs from the scope.
type scopeNeed struct {
	effects      []string
	dangerous    bool
	targets      []string
	destinations []string
	outputs      []string
	operations   int
}

// subtreeMatch reports whether key lies within selector (SEC-012): equal, or the
// selector followed by one of / ! # : @ (a selector ending in one of them matches
// anything after it).
func subtreeMatch(sel targetSelector, key string) bool {
	if sel.Match == "exact" {
		return key == sel.Key
	}
	if key == sel.Key {
		return true
	}
	if sel.Key == "" || !strings.HasPrefix(key, sel.Key) {
		return false
	}
	boundaries := strings.Join(subtreeBoundaries, "")
	if strings.ContainsRune(boundaries, rune(sel.Key[len(sel.Key)-1])) {
		return true
	}
	return strings.ContainsRune(boundaries, rune(key[len(sel.Key)]))
}

// destinationAllowed checks a delivery destination against the granted ones
// (contract.json scope.destination_match, SEC-012). Destinations have their own
// matcher, never the target subtree rule: a granted URL (with ://) matches by strict
// URL containment (scheme and host exactly, the path by whole segments; user info,
// queries, fragments and dot segments in the destination never match). Any other
// granted destination (an email address, handle or channel) matches exactly, and is
// a prefix only when it ends with "/", at "/" boundaries only: "@", ":", "#" and "!"
// are ordinary characters, so a grant for bob@example.com never covers
// bob@example.com@evil.example.
func destinationAllowed(dest string, granted []string) bool {
	for _, g := range granted {
		if dest == g {
			return true
		}
		if strings.Contains(g, "://") || strings.Contains(dest, "://") {
			if urlWithin(dest, g) {
				return true
			}
			continue
		}
		if strings.HasSuffix(g, "/") && strings.HasPrefix(dest, g) && cleanSegments(dest[len(g):]) {
			return true
		}
	}
	return false
}

// cleanSegments reports whether rest (what follows a granted "dir/" prefix) is one
// or more plain "/"-separated segments: no empty, "." or ".." segment, no backslash
// and no control character.
func cleanSegments(rest string) bool {
	if rest == "" || strings.ContainsAny(rest, "\\") || strings.ContainsFunc(rest, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func urlWithin(dest, granted string) bool {
	d, err1 := url.Parse(dest)
	g, err2 := url.Parse(granted)
	if err1 != nil || err2 != nil || d.Scheme == "" || d.Host == "" || g.Host == "" {
		return false
	}
	if d.User != nil || d.RawQuery != "" || d.Fragment != "" || strings.Contains(dest, "\\") {
		return false
	}
	if !strings.EqualFold(d.Scheme, g.Scheme) || !strings.EqualFold(d.Host, g.Host) {
		return false
	}
	ds, ok := urlSegments(d.EscapedPath())
	if !ok {
		return false
	}
	gs, ok := urlSegments(g.EscapedPath())
	if !ok {
		return false
	}
	if len(gs) > len(ds) {
		return false
	}
	for i := range gs {
		if gs[i] != ds[i] {
			return false
		}
	}
	return true
}

// urlSegments splits an escaped URL path into decoded segments, refusing dot
// segments and encoded separators (decoded once).
func urlSegments(escaped string) ([]string, bool) {
	trimmed := strings.Trim(escaped, "/")
	if trimmed == "" {
		return nil, true
	}
	var out []string
	for _, raw := range strings.Split(trimmed, "/") {
		seg, err := url.PathUnescape(raw)
		if err != nil || seg == "." || seg == ".." || strings.ContainsAny(seg, "/\\") {
			return nil, false
		}
		out = append(out, seg)
	}
	return out, true
}

// pathWithin reports whether a local path lies inside one of roots after resolving
// symbolic links (SEC-012); a path or root through a junction is never inside.
func pathWithin(p string, roots []string) bool {
	target, reason := resolvePath(p)
	return reason == "" && resolvedWithin(target, roots)
}

// resolvedWithin reports whether the resolved path target lies inside one of dirs
// (each resolved too; a relative or unresolvable directory covers nothing).
func resolvedWithin(target string, dirs []string) bool {
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			continue
		}
		root, reason := resolvePath(d)
		if reason != "" {
			continue
		}
		rel, err := filepath.Rel(foldCase(root), foldCase(target))
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

// maxLinkHops bounds the symbolic links one path may pass through.
const maxLinkHops = 40

// resolvePath resolves a local path the way the system follows it (C2, SEC-012).
// Each existing component is checked with Lstat: a symbolic link is replaced by its
// target, while a junction or other reparse point (Lstat's ModeIrregular; since Go
// 1.23 filepath.EvalSymlinks no longer follows Windows junctions) refuses the whole
// path, since what it leads to cannot be checked. The components that do not exist
// yet are kept as given. It returns the resolved absolute path, or the reason the
// path is refused.
func resolvePath(p string) (string, string) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", "it is not a valid path: " + truncateRunes(p, 200)
	}
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(abs)
	cur := vol + sep
	parts := splitPath(abs[len(vol):])
	for hops := 0; len(parts) > 0; {
		c := parts[0]
		parts = parts[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		info, err := os.Lstat(next)
		if err != nil {
			// From here on nothing exists (or nothing can be read): the rest is new.
			return filepath.Join(append([]string{next}, parts...)...), ""
		}
		mode := info.Mode()
		switch {
		case mode&fs.ModeIrregular != 0:
			return "", "a junction or other reparse point is in the path " + abs
		case mode&fs.ModeSymlink != 0:
			hops++
			target, err := os.Readlink(next)
			if err != nil || hops > maxLinkHops {
				return "", "the path cannot be resolved: " + abs
			}
			if filepath.IsAbs(target) {
				tv := filepath.VolumeName(target)
				cur = tv + sep
				target = target[len(tv):]
			}
			parts = append(splitPath(target), parts...)
		default:
			cur = next
		}
	}
	return cur, ""
}

// splitPath splits a path (without its volume) into its components.
func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == filepath.Separator || r == '/' })
}

// splitOutputs sorts the local paths a write names into new files (which the scope's
// outputs must cover) and existing ones (an overwrite, which its roots must cover),
// after resolving links (C1, SEC-011, SEC-012). A path whose existence cannot be
// read counts as existing, the stricter case. refused maps a path that passes
// through a junction or other reparse point to the reason.
func splitOutputs(paths []string) (news, existing []string, resolved map[string]string, refused map[string]string) {
	resolved, refused = map[string]string{}, map[string]string{}
	for _, p := range paths {
		r, reason := resolvePath(p)
		if reason != "" {
			refused[p] = reason
			existing = append(existing, p)
			continue
		}
		resolved[p] = r
		if _, err := os.Lstat(r); errors.Is(err, fs.ErrNotExist) {
			news = append(news, p)
		} else {
			existing = append(existing, p)
		}
	}
	return news, existing, resolved, refused
}

func foldCase(p string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

// checkScope refuses a write the scope does not cover (E_FORBIDDEN with
// details.scope). With a state directory it also checks the operation budget
// against the durable count in scope-usage.json (A4); the confirm counts the
// operation itself, under the budget lock (consumeCounted).
func (a *App) checkScope(sc scopeState, need scopeNeed, state string) *Error {
	missing := map[string]any{}
	status := sc.status
	used := 0
	switch sc.status {
	case "granted":
		var effs []string
		for _, e := range need.effects {
			if e != string(Observe) && !contains(sc.file.Effects, e) {
				effs = append(effs, e)
			}
		}
		if len(effs) > 0 {
			missing["effects"] = effs
		}
		if need.dangerous && !sc.file.Dangerous {
			missing["dangerous"] = true
		}
		if sc.file.Targets != nil {
			for _, t := range need.targets {
				ok := false
				for _, sel := range *sc.file.Targets {
					if subtreeMatch(sel, t) {
						ok = true
						break
					}
				}
				if !ok {
					missing["target"] = t
					break
				}
			}
		}
		var dests []string
		for _, d := range need.destinations {
			if !destinationAllowed(d, sc.file.Destinations) {
				dests = append(dests, d)
			}
		}
		if len(dests) > 0 {
			missing["destinations"] = dests
		}
		// A new file must be inside outputs and an existing one (an overwrite) inside
		// roots, after resolving links (C1, SEC-011); absent roots, nothing may be
		// overwritten.
		news, existing, resolved, refused := splitOutputs(need.outputs)
		for _, p := range need.outputs {
			if reason, bad := refused[p]; bad {
				return a.reparseRefusal(sc, need, p, reason)
			}
		}
		var outs, overwrites []string
		for _, p := range news {
			if !resolvedWithin(resolved[p], sc.file.Outputs) {
				outs = append(outs, p)
			}
		}
		for _, p := range existing {
			if !resolvedWithin(resolved[p], sc.file.Roots) {
				overwrites = append(overwrites, p)
			}
		}
		if len(outs) > 0 {
			missing["outputs"] = outs
		}
		if len(overwrites) > 0 {
			missing["roots"] = overwrites
		}
		if len(missing) > 0 {
			status = "insufficient"
			break
		}
		if limit := sc.limit(); limit > 0 && need.operations > 0 && state != "" {
			usage, e := readScopeUsage(state)
			if e != nil {
				return e
			}
			if n := usage.Scopes[sc.file.ScopeID]; n+need.operations > limit {
				status, used = "budget_spent", n
				break
			}
		}
		return nil
	default:
		if effs := writeOnly(need.effects); len(effs) > 0 {
			missing["effects"] = effs
		}
		if need.dangerous {
			missing["dangerous"] = true
		}
		if len(need.targets) > 0 {
			missing["target"] = need.targets[0]
		}
		if len(need.destinations) > 0 {
			missing["destinations"] = need.destinations
		}
		news, existing, _, _ := splitOutputs(need.outputs)
		if len(news) > 0 {
			missing["outputs"] = news
		}
		if len(existing) > 0 {
			missing["roots"] = existing
		}
	}
	return a.scopeRefusal(sc, status, need, missing, used)
}

// limit is the scope's limits.max_operations, 0 when it has none.
func (s scopeState) limit() int {
	if s.file == nil || s.file.Limits == nil || s.file.Limits.MaxOperations == nil {
		return 0
	}
	return *s.file.Limits.MaxOperations
}

// reparseRefusal refuses a write whose local path passes through a junction or
// other reparse point (C2): what the path leads to cannot be checked against the
// scope, and no grant can make it checkable.
func (a *App) reparseRefusal(sc scopeState, need scopeNeed, path, reason string) *Error {
	key := "outputs"
	if _, err := os.Lstat(path); err == nil {
		key = "roots"
	}
	e := a.scopeRefusal(sc, "insufficient", need, map[string]any{key: []string{path}}, 0)
	if s, ok := e.Details["scope"].(map[string]any); ok {
		delete(s, "grant_example") // no grant covers it
	}
	e.Message = "The output path cannot be checked against the scope (" + reason + "), so nothing was sent."
	e.Details["next_action"] = "Write to a path without junctions or other reparse points; do not change the scope to work around it."
	return e
}

// ---------------------------------------------------------------- scope budget

// scopeUsage is <state>/scope-usage.json (A4, SEC-011): the operations confirmed
// under each scope_id. It is the durable count limits.max_operations is checked
// against: pruning the journal never touches it, so a long-lived scope's budget
// does not refill. Deleting it resets every budget, as a fresh state directory does
// (SEC-013).
type scopeUsage struct {
	V      int            `json:"v"`
	Scopes map[string]int `json:"scopes"`
}

const scopeUsageFile = "scope-usage.json"

// readScopeUsage reads the budget count; a missing file counts nothing, and a file
// that cannot be read or trusted refuses the write (fail closed).
func readScopeUsage(state string) (scopeUsage, *Error) {
	usage := scopeUsage{V: 1, Scopes: map[string]int{}}
	path := filepath.Join(state, scopeUsageFile)
	broken := func(err error) *Error {
		return ioError("The scope's operation count cannot be read; nothing was sent.",
			fmt.Sprintf("Ask the user to check %s; deleting it resets the operation count of every scope.", path), err)
	}
	b, err := readFileRetry(path)
	if errors.Is(err, fs.ErrNotExist) {
		return usage, nil
	}
	if err != nil || len(b) > 1<<20 {
		return usage, broken(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var stored scopeUsage
	if err := dec.Decode(&stored); err != nil || stored.V != 1 {
		return usage, broken(err)
	}
	for id, n := range stored.Scopes {
		if n < 0 {
			return usage, broken(nil)
		}
		usage.Scopes[id] = n
	}
	return usage, nil
}

// writeScopeUsage replaces the budget count atomically.
func writeScopeUsage(state string, usage scopeUsage) *Error {
	b, err := json.Marshal(usage)
	if err != nil {
		return internalError("The scope's operation count cannot be encoded.")
	}
	if err := writeFileAtomic(state, scopeUsageFile, append(b, '\n')); err != nil {
		return ioError("The scope's operation count cannot be written; nothing was sent.",
			fmt.Sprintf("Check that %s is writable.", state), err)
	}
	return nil
}

// lockScopeUsage takes the budget lock (<state>/locks/scope-usage.lock), which
// serialises every change of scope-usage.json.
func lockScopeUsage(state, owner string) (func(), *Error) {
	dir := filepath.Join(state, "locks")
	if e := ensurePrivateDir(dir); e != nil {
		return nil, e
	}
	unlock, _, err := acquireLock(filepath.Join(dir, "scope-usage.lock"), owner, targetLockWait)
	switch {
	case err == nil:
		return unlock, nil
	case errors.Is(err, errLockBusy):
		return nil, ioError("The scope's operation count is locked by another process; nothing was sent.",
			"Run the same command again in a few seconds; the confirm token stays valid until it expires.", nil)
	}
	return nil, ioError("The scope's operation count cannot be locked; nothing was sent.", fmt.Sprintf("Check that %s is writable.", dir), err)
}

// refundScopeUsage gives back the budget of n items of a batch that were never
// dispatched (skipped after a stop). Best effort: when it fails, the budget stays
// spent, the safe side.
func refundScopeUsage(state, scopeID string, n int) {
	if n <= 0 || scopeID == "" {
		return
	}
	unlock, e := lockScopeUsage(state, "")
	if e != nil {
		return
	}
	defer unlock()
	usage, e := readScopeUsage(state)
	if e != nil {
		return
	}
	usage.Scopes[scopeID] = max(usage.Scopes[scopeID]-n, 0)
	_ = writeScopeUsage(state, usage)
}

func writeOnly(effects []string) []string {
	var out []string
	for _, e := range effects {
		if e != string(Observe) {
			out = append(out, e)
		}
	}
	return out
}

// scopeRefusal builds E_FORBIDDEN with details.scope (BLUEPRINT 4.5). The example
// grant is the whole file after the change: for a readable scope (insufficient,
// expired, revoked, budget spent) it is the current file with only what is missing
// added, so saving it never takes back what was granted; for a missing or invalid
// file it is a new grant for this write.
func (a *App) scopeRefusal(sc scopeState, status string, need scopeNeed, missing map[string]any, used int) *Error {
	if _, ok := missing["dangerous"]; !ok {
		missing["dangerous"] = false
	}
	if _, ok := missing["effects"]; !ok {
		missing["effects"] = []string{}
	}
	var example map[string]any
	if sc.file != nil && sc.status != "invalid" && sc.status != "missing" {
		example = a.extendedGrant(sc, status, need, used)
	} else {
		example = a.newGrant(need)
	}
	details := map[string]any{"status": status, "path": sc.path, "scope_id": nil, "missing": missing, "grant_example": example}
	if id := sc.id(); id != nil {
		details["scope_id"] = *id
	}
	var message, next string
	switch status {
	case "missing":
		message = "No scope grants this write, so nothing was sent."
		next = "Ask the user to grant this scope by creating %s with the content of details.scope.grant_example, adjusted to what they allow; do not create or edit it yourself."
	case "invalid":
		message = "The scope file is invalid (" + sc.reason + "), so nothing was sent."
		next = "Ask the user to fix %s; details.scope.grant_example shows a valid grant for this write. Do not edit it yourself."
	case "expired":
		message = "The scope has expired, so nothing was sent."
		next = "Ask the user to renew the scope in %s (details.scope.grant_example is the file with a new expires_at); do not edit it yourself."
	case "revoked":
		message = "The scope was revoked, so nothing was sent."
		next = "The user revoked the scope in %s: ask them whether to grant it again (details.scope.grant_example shows it granted); do not edit it yourself."
	case "budget_spent":
		message = "The scope's operation budget is spent, so nothing was sent."
		next = "Ask the user to raise limits.max_operations in %s (details.scope.grant_example shows the file after the change); do not edit it yourself."
	default:
		message = "The scope does not cover this write (details.scope.missing), so nothing was sent."
		next = "Ask the user to add what details.scope.missing lists to %s (details.scope.grant_example shows the whole file after the change); do not edit it yourself."
	}
	return Forbidden(message, map[string]any{
		"scope":          details,
		"dispatch_state": "not_started",
		"next_action":    fmt.Sprintf(next, sc.path),
	})
}

// maxExampleTargets caps the targets an example grant adds.
const maxExampleTargets = 5

// newGrant is an example scope file for this write alone.
func (a *App) newGrant(need scopeNeed) map[string]any {
	now := a.now()
	effects := nonNil(writeOnly(need.effects))
	sort.Strings(effects)
	example := map[string]any{
		"scope_id":   fmt.Sprintf("scp_%s_%s", now.Format("2006-01-02"), a.toolName()),
		"tool":       a.toolName(),
		"granted_by": "<your name>",
		"granted_at": timeString(now),
		"expires_at": timeString(now.Add(7 * 24 * time.Hour)),
		"effects":    effects,
		"dangerous":  need.dangerous,
	}
	var sel []any
	for _, t := range need.targets {
		if len(sel) == maxExampleTargets {
			break
		}
		sel = append(sel, map[string]any{"key": t})
	}
	if len(sel) > 0 {
		example["targets"] = sel
	}
	if len(need.destinations) > 0 {
		example["destinations"] = need.destinations
	}
	news, existing, _, _ := splitOutputs(need.outputs)
	if dirs := outputDirs(news); len(dirs) > 0 {
		example["outputs"] = dirs
	}
	if dirs := outputDirs(existing); len(dirs) > 0 {
		example["roots"] = dirs
	}
	return example
}

// extendedGrant is the current scope file with what this write needs added: the
// union of effects, targets, destinations and outputs, dangerous when needed, a new
// expires_at only when the scope expired, a raised operation budget when it is
// spent, and the same scope_id.
func (a *App) extendedGrant(sc scopeState, status string, need scopeNeed, used int) map[string]any {
	f := sc.file
	effects := append([]string{}, f.Effects...)
	for _, e := range writeOnly(need.effects) {
		if !contains(effects, e) {
			effects = append(effects, e)
		}
	}
	sort.Strings(effects)
	example := map[string]any{
		"scope_id":   f.ScopeID,
		"tool":       f.Tool,
		"granted_by": f.GrantedBy,
		"granted_at": f.GrantedAt,
		"expires_at": nil,
		"effects":    effects,
		"dangerous":  f.Dangerous || need.dangerous,
	}
	if f.ExpiresAt != nil {
		example["expires_at"] = *f.ExpiresAt
	}
	if status == "expired" {
		example["expires_at"] = timeString(a.now().Add(7 * 24 * time.Hour))
	}
	if f.Targets != nil {
		sel := make([]any, 0, len(*f.Targets))
		for _, t := range *f.Targets {
			o := map[string]any{"key": t.Key}
			if t.Match != "" {
				o["match"] = t.Match
			}
			sel = append(sel, o)
		}
		added := 0
		for _, t := range need.targets {
			covered := false
			for _, s := range *f.Targets {
				if subtreeMatch(s, t) {
					covered = true
					break
				}
			}
			if !covered && added < maxExampleTargets {
				sel = append(sel, map[string]any{"key": t})
				added++
			}
		}
		example["targets"] = sel
	}
	dests := append([]string{}, f.Destinations...)
	for _, d := range need.destinations {
		if !destinationAllowed(d, f.Destinations) && !contains(dests, d) {
			dests = append(dests, d)
		}
	}
	if len(dests) > 0 {
		example["destinations"] = dests
	}
	// New files go to outputs and overwritten ones to roots (C1).
	outs := append([]string{}, f.Outputs...)
	roots := append([]string{}, f.Roots...)
	news, existing, resolved, _ := splitOutputs(need.outputs)
	var newFiles, overwritten []string
	for _, p := range news {
		if !resolvedWithin(resolved[p], f.Outputs) {
			newFiles = append(newFiles, p)
		}
	}
	for _, p := range existing {
		if r, ok := resolved[p]; !ok || !resolvedWithin(r, f.Roots) {
			overwritten = append(overwritten, p)
		}
	}
	for _, d := range outputDirs(newFiles) {
		if !contains(outs, d) {
			outs = append(outs, d)
		}
	}
	for _, d := range outputDirs(overwritten) {
		if !contains(roots, d) {
			roots = append(roots, d)
		}
	}
	if len(outs) > 0 {
		example["outputs"] = outs
	}
	if len(roots) > 0 {
		example["roots"] = roots
	}
	if f.Limits != nil {
		limits := map[string]any{}
		if f.Limits.MaxOperations != nil {
			limits["max_operations"] = *f.Limits.MaxOperations
			if status == "budget_spent" {
				limits["max_operations"] = max(*f.Limits.MaxOperations, used+need.operations)
			}
		}
		example["limits"] = limits
	}
	if f.Notes != "" {
		example["notes"] = f.Notes
	}
	return example
}

// outputDirs is the directories of the given output paths, absolute and unique.
func outputDirs(paths []string) []string {
	var dirs []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if d := filepath.Dir(abs); !contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// authorizationSummary is context.authorization (CLI-035).
func (a *App) authorizationSummary(sc scopeState) map[string]any {
	out := map[string]any{"status": sc.status, "scope_id": nil, "path": sc.path, "effects": []string{}, "dangerous": false,
		"expires_at": nil}
	if sc.status == "invalid" {
		return out
	}
	if sc.file != nil {
		out["scope_id"] = sc.file.ScopeID
		out["effects"] = nonNil(sc.file.Effects)
		out["dangerous"] = sc.file.Dangerous
		if sc.file.ExpiresAt != nil {
			out["expires_at"] = *sc.file.ExpiresAt
		}
	}
	return out
}
