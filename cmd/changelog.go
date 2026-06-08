package cmd

import (
	"fmt"
	"strings"

	project "github.com/fatecannotbealtered/kibana-cli"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var changelogSince string
var changelogSource = project.ChangelogMarkdown

var changelogCmd = &cobra.Command{
	Use:   "changelog",
	Short: "Show version changes from CHANGELOG.md",
	Long: `Show version changes parsed from the embedded CHANGELOG.md.

Use --since after self-update so an Agent can learn what changed before continuing.`,
	Args: cobra.NoArgs,
	RunE: runChangelog,
}

type changelogEntry struct {
	Version string              `json:"version"`
	Date    string              `json:"date,omitempty"`
	Changes map[string][]string `json:"changes"`
}

func init() {
	rootCmd.AddCommand(changelogCmd)
	changelogCmd.Flags().StringVar(&changelogSince, "since", "", "Return only entries newer than this version")
}

func runChangelog(_ *cobra.Command, _ []string) error {
	entries := filterChangelogEntries(parseChangelog(changelogSource), changelogSince)
	if jsonMode {
		printJSONSuccess(map[string]any{
			"current_version": normalizeReleaseVersion(version),
			"since":           strings.TrimSpace(changelogSince),
			"entries":         entries,
			"count":           len(entries),
		})
		return nil
	}
	if len(entries) == 0 {
		output.Info("No changelog entries.")
		return nil
	}
	for _, e := range entries {
		if e.Date != "" {
			fmt.Printf("## %s - %s\n", e.Version, e.Date)
		} else {
			fmt.Printf("## %s\n", e.Version)
		}
		for _, category := range changelogCategories() {
			items := e.Changes[category]
			if len(items) == 0 {
				continue
			}
			fmt.Printf("\n### %s\n", titleCategory(category))
			for _, item := range items {
				fmt.Printf("- %s\n", item)
			}
		}
		fmt.Println()
	}
	return nil
}

func parseChangelog(markdown string) []changelogEntry {
	var entries []changelogEntry
	var current *changelogEntry
	currentCategory := ""
	flush := func() {
		if current == nil {
			return
		}
		if changelogEntryEmpty(*current) {
			current = nil
			return
		}
		entries = append(entries, *current)
		current = nil
	}
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## [") {
			flush()
			version, date := parseChangelogHeader(trimmed)
			current = &changelogEntry{Version: version, Date: date, Changes: map[string][]string{}}
			currentCategory = ""
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			currentCategory = normalizeChangelogCategory(strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")))
			if currentCategory != "" && current.Changes[currentCategory] == nil {
				current.Changes[currentCategory] = nil
			}
			continue
		}
		if currentCategory == "" || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if item != "" {
			current.Changes[currentCategory] = append(current.Changes[currentCategory], item)
		}
	}
	flush()
	return entries
}

func parseChangelogHeader(line string) (version, date string) {
	end := strings.Index(line, "]")
	if end > len("## [") {
		version = strings.TrimSpace(line[len("## ["):end])
	}
	if sep := strings.Index(line, " - "); sep >= 0 {
		date = strings.TrimSpace(line[sep+3:])
	}
	return version, date
}

func changelogEntryEmpty(entry changelogEntry) bool {
	for _, items := range entry.Changes {
		if len(items) > 0 {
			return false
		}
	}
	return true
}

func normalizeChangelogCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, category := range changelogCategories() {
		if s == category {
			return category
		}
	}
	return ""
}

func changelogCategories() []string {
	return []string{"added", "changed", "fixed", "deprecated", "removed", "security"}
}

func titleCategory(category string) string {
	if category == "" {
		return ""
	}
	return strings.ToUpper(category[:1]) + category[1:]
}

func filterChangelogEntries(entries []changelogEntry, since string) []changelogEntry {
	since = normalizeReleaseVersion(since)
	if since == "" {
		return entries
	}
	out := make([]changelogEntry, 0, len(entries))
	for _, e := range entries {
		if strings.EqualFold(e.Version, "Unreleased") {
			out = append(out, e)
			continue
		}
		if compareVersions(normalizeReleaseVersion(e.Version), since) > 0 {
			out = append(out, e)
		}
	}
	return out
}
