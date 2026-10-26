// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// Helpers a command calls from its own code: the run's output format (Q14),
// whether the response keeps a field (Q17), and CLI-028's durations and times (Q15).

// appsByRoot maps the root command of each App that Execute is running to its App,
// so the package functions below can reach the run from a command.
var appsByRoot sync.Map

// appFor returns the App whose Execute is running cmd's tree, or nil.
func appFor(cmd *cobra.Command) *App {
	if cmd == nil {
		return nil
	}
	if v, ok := appsByRoot.Load(cmd.Root()); ok {
		return v.(*App)
	}
	return nil
}

// OutputFormat returns the running command's output format as resolved for this run
// (Q14): json, text or raw. A command that answers with bytes in raw mode and an
// object otherwise (Meta.RawOutput) asks it before App.OK. Outside App.Execute it
// reads the --format and --json flags.
func OutputFormat(cmd *cobra.Command) string {
	if a := appFor(cmd); a != nil && a.run != nil && a.run.cmd == cmd {
		return a.run.format
	}
	if cmd == nil {
		return defaultFormat
	}
	if j, _ := cmd.Flags().GetBool("json"); j {
		return "json"
	}
	f, _ := cmd.Flags().GetString("format")
	if f = strings.ToLower(strings.TrimSpace(f)); f == "text" || f == "raw" {
		return f
	}
	return "json"
}

// WantsField reports whether the running command's response keeps the field name
// (Q17), by the rules of --fields (CLI-040): all keeps every field; a,b keeps those;
// +a keeps the default fields and a; without --fields the default fields
// (Meta.DefaultFields, else the output schema's) are kept, or every field when there
// are none; ref is always kept. A command asks it to skip a read whose only use is a
// field the response will not show. Outside App.Execute it returns true.
func WantsField(cmd *cobra.Command, name string) bool {
	a := appFor(cmd)
	if a == nil || a.run == nil {
		return true
	}
	return a.keepsField(a.run.fields, a.metaFor(cmd), name)
}

// keepsField is WantsField's rule, the one project applies (CLI-040).
func (a *App) keepsField(sel fieldSelection, m *Meta, name string) bool {
	if name == "ref" || sel.all {
		return true
	}
	defaults := a.schemaOf(m).DefaultFields
	if m != nil && len(m.DefaultFields) > 0 {
		defaults = m.DefaultFields
	}
	switch {
	case sel.set && !sel.add:
		return contains(sel.names, name)
	case sel.set && sel.add:
		return len(defaults) == 0 || contains(defaults, name) || contains(sel.names, name)
	}
	return len(defaults) == 0 || contains(defaults, name)
}

// durationPattern is a CLI-028 duration: a whole number and one unit.
var durationPattern = regexp.MustCompile(`^([0-9]+)(ms|s|m|h|d)$`)

var durationUnits = map[string]time.Duration{"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour,
	"d": 24 * time.Hour}

// ParseDuration parses a duration as CLI-028 writes it (Q15): a whole number and one
// unit, ms, s, m, h or d (500ms, 30s, 5m, 2h, 7d), for --wait and any other flag
// that takes one.
func ParseDuration(s string) (time.Duration, error) {
	m := durationPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("%q is not a duration: use a whole number and one unit, ms, s, m, h or d (500ms, 30s, 5m, 2h, 7d)", s)
	}
	unit := durationUnits[m[2]]
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n > int64(math.MaxInt64/unit) {
		return 0, fmt.Errorf("%q is too long a duration", s)
	}
	return time.Duration(n) * unit, nil
}

// ParseTime parses a time as CLI-028 writes it (Q15), for --since and --until: an
// ISO 8601 date (2026-06-01, midnight UTC), an ISO 8601 date and time
// (2026-06-01T08:00:00Z; an offset is converted to UTC), or a duration ago (7d is
// now minus 7 days). The result is in UTC, to echo in a list's scope.
func ParseTime(s string, now time.Time) (time.Time, error) {
	t := strings.TrimSpace(s)
	if durationPattern.MatchString(t) {
		d, err := ParseDuration(t)
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(-d).UTC(), nil
	}
	if v, err := time.Parse(time.DateOnly, t); err == nil {
		return v.UTC(), nil
	}
	if v, err := time.Parse(time.RFC3339Nano, t); err == nil {
		return v.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%q is not a time: use an ISO 8601 date (2026-06-01), a date and time (2026-06-01T08:00:00Z) or a time ago (7d, 24h, 30m)", s)
}
