// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/pflag"
)

// Diagnostics (CLI-102): <PREFIX>_DEBUG turns on "debug: <message>" lines on
// stderr. They never go to stdout and never change the output or the exit code;
// there is no --verbose or --debug flag. The library itself logs only the state
// directory, the scope file, the settings file and active profile, each stage of
// update and the release tags it skips.
//
// Debug lines and emitted errors are redacted with one set of values (secretValues,
// SEC-021, D1): secret flags, secret file inputs, Secret settings and the values a
// tool registers with App.AddSecret, each also URL-escaped.

// debugOn reports whether <PREFIX>_DEBUG is set to anything but "" or "0".
func (a *App) debugOn() bool {
	v := strings.TrimSpace(a.env(a.envPrefix() + "_DEBUG"))
	return v != "" && v != "0"
}

// Debugf writes "debug: <message>" to stderr when <PREFIX>_DEBUG is set to anything
// but "" or "0". Secret values (secretValues) are replaced by ***, and line breaks
// are escaped so every line starts with "debug:" (D11). It is safe to call from
// several goroutines (Q22): each line is written whole.
func (a *App) Debugf(format string, args ...any) {
	if !a.debugOn() {
		return
	}
	msg := redactWith(fmt.Sprintf(format, args...), a.secretValues())
	msg = strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(msg)
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = fmt.Fprintf(a.stderr(), "debug: %s\n", msg)
}

// debugOnce is Debugf for the library's own lines: each distinct line once per App.
func (a *App) debugOnce(format string, args ...any) {
	if !a.debugOn() {
		return
	}
	line := fmt.Sprintf(format, args...)
	a.mu.Lock()
	if a.debugSeen == nil {
		a.debugSeen = map[string]bool{}
	}
	seen := a.debugSeen[line]
	a.debugSeen[line] = true
	a.mu.Unlock()
	if !seen {
		a.Debugf("%s", line)
	}
}

// secretValues lists the values debug lines and errors never show, longest first:
// the running command's secret flags (Meta.Secrets with their twins, B2) and
// secret file inputs, the Secret settings' values from their flag, the environment
// or their default, and the values registered with AddSecret; each also in its URL
// query-escaped form, its path-escaped form, and its path-escaped form that keeps
// "/" (Python's urllib.parse.quote, P17).
func (a *App) secretValues() []string {
	set := map[string]bool{}
	add := func(v string) {
		for _, form := range []string{v, url.QueryEscape(v), url.PathEscape(v), strings.ReplaceAll(url.PathEscape(v), "%2F", "/")} {
			if form != "" {
				set[form] = true
			}
		}
	}
	addFlag := func(f *pflag.Flag) {
		if f == nil || !f.Changed {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				add(v)
			}
		}
		add(f.Value.String())
	}
	var cmdFlags func(string) *pflag.Flag
	if st := a.run; st != nil && st.cmd != nil {
		cmd := st.cmd
		cmdFlags = func(name string) *pflag.Flag { return lookupFlag(cmd, name) }
		if m := a.metaFor(cmd); m != nil {
			for _, name := range m.Secrets {
				addFlag(lookupFlag(cmd, name))
				for file, bound := range m.FileInputs {
					if bound != name && file != name {
						continue
					}
					if fi := st.files[file]; fi != nil {
						add(string(fi.content))
						add(strings.TrimSpace(string(fi.content)))
					}
				}
			}
		}
	}
	for _, s := range a.Settings {
		if !s.Secret {
			continue
		}
		if s.Flag != "" && cmdFlags != nil {
			addFlag(cmdFlags(s.Flag))
		}
		add(a.env(a.envPrefix() + "_" + s.envName()))
		add(s.Default)
	}
	a.mu.Lock()
	registered := append([]string(nil), a.secrets...)
	a.mu.Unlock()
	for _, v := range registered {
		add(v)
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// errorSecrets is secretValues without values shorter than 4 characters, which
// would mangle the text of an error without protecting anything (D1, D2).
func (a *App) errorSecrets() []string {
	var out []string
	for _, v := range a.secretValues() {
		if utf8.RuneCountInString(v) >= 4 {
			out = append(out, v)
		}
	}
	return out
}

// redactWith replaces every secret value in s with ***.
func redactWith(s string, secrets []string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
}

// structuralDetails are error detail keys whose values the library generates from
// fixed sets (ids, tokens, times, enum values): they carry no secret, and redacting
// a coincidental match would break them.
var structuralDetails = map[string]bool{"dispatch_state": true, "problem": true, "conflict": true, "operation_id": true,
	"active_operation": true, "confirm_token": true, "expires_at": true, "stage": true, "skill_sync_status": true,
	"install_method": true, "signature_status": true, "status": true, "action": true}

// redactValue returns a copy of a JSON-like value with every secret replaced by ***
// in every string, at any depth (D1); the values of structural keys are kept.
func redactValue(v any, secrets []string) any {
	switch x := v.(type) {
	case string:
		return redactWith(x, secrets)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if structuralDetails[k] {
				out[k] = val
				continue
			}
			out[k] = redactValue(val, secrets)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactValue(val, secrets)
		}
		return out
	case []string:
		out := make([]string, len(x))
		for i, val := range x {
			out[i] = redactWith(val, secrets)
		}
		return out
	case nil, bool, int, int64, float64, json.Number:
		return v
	}
	if g, err := toGeneric(v); err == nil {
		return redactValue(g, secrets)
	}
	return v
}
