// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// search command flags. These are package-level vars so cobra can bind them
// during init(); resetSearchFlags() is called at the top of Run so repeated
// invocations within a single process (tests, REPLs) don't inherit stale
// values.
//
// The JSON switch is the persistent --json flag on rootCmd (globalJSON);
// search used to own a local --json flag but now shares the global one so
// all commands behave consistently.
var (
	searchType     string
	searchLimit    int
	searchPageSize int
)

// searchCmd implements `notioncli search` — a workspace-wide query against
// the Notion search API. See issue #4.
var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search pages and databases across the workspace",
	Long: `Search matching pages and databases accessible to the integration.

Examples:
  notioncli search "roadmap"
  notioncli search "roadmap" --type databases
  notioncli search "" --limit 50
  notioncli search "roadmap" --json > results.ndjson

By default every matching result is fetched (pagination is followed until
exhausted). Pass --limit N to cap the total number returned. With --json,
one JSON object per matching result is emitted to stdout, newline-delimited,
passing through the raw Notion response object unchanged.`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          runSearch,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// runSearch is broken out so tests can call it with a preconfigured
// *cobra.Command and bypass the Execute() output shim. RunE returns an error
// for the same reason — tests assert on it rather than on os.Exit.
func runSearch(cmd *cobra.Command, args []string) error {
	query := ""
	if len(args) == 1 {
		query = args[0]
	}

	filter, err := buildSearchFilter(searchType)
	if err != nil {
		emitSearchError(cmd, err)
		return err
	}

	if err := validateSearchPageSize(searchPageSize); err != nil {
		emitSearchError(cmd, err)
		return err
	}

	notionAPIKey, _ := utils.SetAPIConfig()
	client := utils.NewSearchClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

	req := utils.SearchRequest{
		Query:    query,
		Filter:   filter,
		PageSize: searchPageSize,
	}

	results, err := client.SearchAll(context.Background(), req, searchLimit)
	if err != nil {
		emitSearchError(cmd, fmt.Errorf("search: %w", err))
		return err
	}

	if globalJSON {
		return emitSearchJSON(cmd, results)
	}
	return emitSearchTable(cmd, results)
}

// buildSearchFilter translates the user-facing --type value into the
// Notion API filter object. Empty string means "no filter". Invalid
// values return an error pointing at the allowed set.
//
// Notion-Version 2026-03-11 returns `data_source` objects from
// /v1/search instead of the legacy `database` shape — verified live
// (Facet Interactive workspace: 81 data_source results, 0 database
// results). The CLI keeps `--type databases` as the user-facing alias
// because that's the term users still reach for, but the wire filter
// follows what the API actually emits. See issue #79.
func buildSearchFilter(typeFlag string) (*utils.SearchFilter, error) {
	switch typeFlag {
	case "":
		return nil, nil
	case "page", "pages":
		return &utils.SearchFilter{Property: "object", Value: "page"}, nil
	case "database", "databases", "data_source", "data_sources":
		return &utils.SearchFilter{Property: "object", Value: "data_source"}, nil
	}
	return nil, fmt.Errorf("invalid --type %q (want pages|databases)", typeFlag)
}

// validateSearchPageSize enforces the Notion API bounds client-side so we
// don't spend a round-trip on a value the server will reject. 0 means
// "use the server default" and is allowed. Valid range otherwise is 1-100.
func validateSearchPageSize(pageSize int) error {
	if pageSize == 0 {
		return nil
	}
	if pageSize < 1 || pageSize > 100 {
		return fmt.Errorf("invalid --page-size %d (want 1-100, or 0 for server default)", pageSize)
	}
	return nil
}

// emitSearchJSON writes search results to stdout as the Notion API
// returned them (Raw pass-through, no envelope). Compact NDJSON by
// default; a single pretty-printed JSON array when --pretty is set.
// The Raw pass-through preserves any Notion fields the CLI does not
// model — we deliberately avoid re-marshalling the typed struct for
// the compact case so unknown keys survive the round-trip.
func emitSearchJSON(cmd *cobra.Command, results []utils.SearchResult) error {
	out := cmd.OutOrStdout()
	if globalPretty {
		// Pretty-print a single JSON array so the output is a single
		// valid JSON document. We unmarshal Raw into interface{} so
		// the re-marshal preserves every field the API returned.
		arr := make([]interface{}, 0, len(results))
		for _, r := range results {
			raw := r.Raw
			if len(raw) == 0 {
				buf, err := json.Marshal(r)
				if err != nil {
					return fmt.Errorf("marshal result: %w", err)
				}
				raw = buf
			}
			var obj interface{}
			if err := json.Unmarshal(raw, &obj); err != nil {
				return fmt.Errorf("unmarshal result: %w", err)
			}
			arr = append(arr, obj)
		}
		return emitJSON(out, arr)
	}
	// Compact NDJSON: write the Raw bytes verbatim, one per line.
	for _, r := range results {
		raw := r.Raw
		if len(raw) == 0 {
			// Defensive: if Raw didn't make it through (e.g. synthesized in a
			// test), re-marshal from the typed fields so the stream is never
			// silently empty.
			buf, err := json.Marshal(r)
			if err != nil {
				return fmt.Errorf("marshal result: %w", err)
			}
			raw = buf
		}
		if _, err := fmt.Fprintln(out, string(raw)); err != nil {
			return fmt.Errorf("write result: %w", err)
		}
	}
	return nil
}

// emitSearchTable renders the human-readable table: icon, title, type, url,
// last-edited. Empty results print a friendly yellow notice and return nil
// (this is not an error — the API responded successfully).
func emitSearchTable(cmd *cobra.Command, results []utils.SearchResult) error {
	out := cmd.OutOrStdout()
	if len(results) == 0 {
		color.Yellow("No results.")
		return nil
	}
	fmt.Fprintln(out)
	for _, r := range results {
		icon := r.Icon.Display()
		if icon == "" {
			icon = "-"
		}
		title := extractSearchTitle(r)
		if title == "" {
			title = "(untitled)"
		}
		title = truncateRunes(title, 60)
		edited := formatSearchTime(r.LastEditedTime)
		fmt.Fprintf(out, "  %s  %-60s  %-8s  %s  %s\n", icon, title, r.Object, r.URL, edited)
	}
	fmt.Fprintln(out)
	color.Cyan("  %d result(s)", len(results))
	return nil
}

// emitSearchError writes a single-line JSON error object to stderr when
// --json is set (keeps the stdout stream valid NDJSON for piping), or a
// red human-readable message otherwise. Output is routed through
// cmd.ErrOrStderr() so tests can capture the stream. Does not call
// os.Exit; the RunE return value drives the final exit code via cobra.
func emitSearchError(cmd *cobra.Command, err error) {
	errOut := cmd.ErrOrStderr()
	if globalJSON {
		emitError(errOut, err)
		return
	}
	fmt.Fprintln(errOut, color.RedString("Error: %v", err))
}

// extractSearchTitle digs into the raw Notion payload to find the object's
// display title. Pages put it at properties.title.title[].plain_text;
// databases put it at title[].plain_text. Falls back to "" on any miss.
//
// Every run is concatenated. A title carrying mixed formatting, a mention
// or a link is split by Notion into several rich-text runs, so reading
// only the first one truncated the label — issue #77. This matches what
// pagePlainTitle / databasePlainTitle / findPageTitleText already do.
func extractSearchTitle(r utils.SearchResult) string {
	// Database shape: top-level `title` array of rich text.
	if len(r.Title) > 0 {
		var rt []plainRun
		if err := json.Unmarshal(r.Title, &rt); err == nil && len(rt) > 0 {
			return joinPlainText(rt)
		}
	}
	// Page shape: properties.title.title[].plain_text. The key is typically
	// "title" but Notion allows the primary-title property to be renamed,
	// so we scan every property looking for a "title" type.
	if len(r.Properties) > 0 {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(r.Properties, &props); err == nil {
			for _, raw := range props {
				var p struct {
					Type  string     `json:"type"`
					Title []plainRun `json:"title"`
				}
				if err := json.Unmarshal(raw, &p); err != nil {
					continue
				}
				if p.Type == "title" && len(p.Title) > 0 {
					return joinPlainText(p.Title)
				}
			}
		}
	}
	return ""
}

// plainRun is the minimal shape of a Notion rich-text run: just the
// pre-rendered plain_text. Both title shapes above decode into a slice
// of these.
type plainRun struct {
	PlainText string `json:"plain_text"`
}

// joinPlainText concatenates the plain_text of every run. Notion splits a
// title across runs at every formatting, mention or link boundary, so only
// the concatenation is the whole title.
func joinPlainText(runs []plainRun) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.PlainText)
	}
	return sb.String()
}

// truncateRunes shortens s to at most max *runes*, appending "..." when
// truncation occurs. Rune-safe so multi-byte characters (emoji, CJK) are
// never cut mid-codepoint. max must be >= 4 to leave room for the ellipsis.
func truncateRunes(s string, max int) string {
	if max < 4 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}

// formatSearchTime renders a Notion ISO-8601 timestamp as YYYY-MM-DD HH:MM
// in UTC. Unparseable values pass through as-is so we never hide API data.
func formatSearchTime(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// resetSearchFlags clears any persistent flag state from a previous run.
// Exposed for tests.
func resetSearchFlags() {
	searchType = ""
	searchLimit = 0
	searchPageSize = 0
	resetGlobalOutputFlags()
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().StringVar(&searchType, "type", "", "Restrict results to pages|databases")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 0, "Maximum total results to return (0 = all)")
	searchCmd.Flags().IntVar(&searchPageSize, "page-size", 0, "Notion API page size (1-100, 0 = server default)")
}
