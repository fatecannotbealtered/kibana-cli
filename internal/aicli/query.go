// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"strings"

	"github.com/spf13/cobra"
)

// Page is one page of a list (CLI-041). Every list says whether it is complete:
// HasMore with exactly one of NextCursor, NextOffset or ContinuationReason (and none
// of them without HasMore); Truncated with TruncatedReason, and a truncated list has
// more; Scope echoes what the list covers (filters actually applied, and a
// narrower-than-expected scope such as direct members only); Sort names the order;
// Total, when the upstream reports it, is the number of items the whole list has, so
// a count question needs no paging (CLI-044). Untrusted lists paths relative to the
// page data, for example "items[].title"; the output schema's untrusted fields are
// added automatically.
type Page struct {
	Items              any
	Count              int
	HasMore            bool
	NextCursor         string
	NextOffset         *int
	ContinuationReason string
	Truncated          bool
	TruncatedReason    string
	Scope              map[string]any
	Sort               string
	Total              *int
	Untrusted          []string
}

// List reports a page of items. Count is the number of items (0 means "count them");
// a page that breaks the completeness rules is a bug in the tool and is reported as
// E_UNKNOWN. Default fields and --fields apply to each item; the pagination keys are
// always kept.
func (a *App) List(cmd *cobra.Command, p Page) error {
	st, perr := a.runFor(cmd)
	if perr != nil {
		return perr
	}
	m := a.metaFor(cmd)
	if m != nil && m.isWrite() {
		return internalError("A write command reported a list without the write gate; it must use App.Write.")
	}
	items, err := toGeneric(p.Items)
	if err != nil {
		return internalError("The list items cannot be encoded as JSON.")
	}
	list, ok := items.([]any)
	if !ok {
		if items != nil {
			return internalError("Page.Items must be a slice.")
		}
		list = []any{}
	}
	count := p.Count
	if count == 0 {
		count = len(list)
	}
	if count != len(list) {
		return internalError("Page.Count must equal the number of items in the page (CLI-041).")
	}
	continuations := 0
	data := map[string]any{"items": list, "count": count, "has_more": p.HasMore}
	if p.NextCursor != "" {
		data["next_cursor"] = p.NextCursor
		continuations++
	}
	if p.NextOffset != nil {
		if *p.NextOffset < 0 {
			return internalError("Page.NextOffset must not be negative (CLI-041).")
		}
		data["next_offset"] = *p.NextOffset
		continuations++
	}
	if strings.TrimSpace(p.ContinuationReason) != "" {
		data["continuation_reason"] = p.ContinuationReason
		continuations++
	}
	switch {
	case p.HasMore && continuations != 1:
		return internalError("A list with has_more must give exactly one of NextCursor, NextOffset or ContinuationReason (CLI-041).")
	case !p.HasMore && continuations > 0:
		return internalError("A list without has_more gives no NextCursor, NextOffset or ContinuationReason (CLI-041).")
	}
	if p.Truncated {
		if strings.TrimSpace(p.TruncatedReason) == "" {
			return internalError("A truncated list must give TruncatedReason (CLI-041).")
		}
		if !p.HasMore {
			return internalError("A truncated list has more items: set HasMore and say how to continue (CLI-041).")
		}
		data["truncated"] = true
		data["truncated_reason"] = p.TruncatedReason
	} else if strings.TrimSpace(p.TruncatedReason) != "" {
		return internalError("Page.TruncatedReason is only for a truncated list (CLI-041).")
	}
	if p.Total != nil {
		if *p.Total < count {
			return internalError("Page.Total counts the whole list, so it is at least the page's count (CLI-041).")
		}
		data["total"] = *p.Total
	}
	if len(p.Scope) > 0 {
		data["scope"] = p.Scope
	}
	if p.Sort != "" {
		data["sort"] = p.Sort
	}
	gen, err := toGenericData(data)
	if err != nil {
		return internalError("The list cannot be encoded as JSON.")
	}
	schema := a.schemaOf(m)
	gen = a.project(st, m, schema, "page", gen)
	paths := prefixPaths("data", p.Untrusted)
	paths = append(paths, schemaUntrusted(schema, "page")...)
	st.setOut(gen, data, paths)
	return nil
}
