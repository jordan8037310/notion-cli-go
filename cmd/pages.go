// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Flag vars for the pages subcommands. Keeping them package-level mirrors
// blocks.go's pattern and lets cobra bind them via init().
var (
	pagesCreateParent       string
	pagesCreateParentDB     string
	pagesCreateTitle        string
	pagesCreateProps        string
	pagesCreateChildren     string
	pagesCreateFromText     string
	pagesCreateManyFrom     string
	pagesCreateManyParent   string
	pagesCreateManyParentDB string
	pagesCreateFromMD       string
	pagesAppendMDFrom       string
	pagesAppendMDPrepend    bool
	pagesReplaceMDFrom      string
	pagesCreateManyOnErr    string
	pagesUpdateProps2       string
	pagesUpdateTitle        string
	pagesUpdateProps        []string
	pagesMoveParent         string
	pagesDuplicateParent    string
)

// pagesCmd is the parent of every `notioncli pages …` subcommand.
var pagesCmd = &cobra.Command{
	Use:   "pages",
	Short: "Manage Notion pages (get, create, update, archive, move, duplicate)",
	Long: `Work with Notion pages directly: retrieve, create, update,
archive/unarchive, move between parents, and duplicate.

Examples:
  notioncli pages get <page-id>
  notioncli pages create --parent <page-id> --title "New page"
  notioncli pages create --parent-database <db-id> --title "New row"
  notioncli pages update <id> --title "Renamed"
  notioncli pages archive <id>
  notioncli pages unarchive <id>
  notioncli pages move <id> --parent <new-parent>
  notioncli pages duplicate <id> --parent <parent-id>`,
}

// newPageClient builds a PageClient using the CLI's standard config loading
// so every subcommand shares identical client construction. Returns
// utils.ErrMissingAPIKey (wrapped) when NOTION_API_KEY resolves empty so
// callers get a clear configuration error instead of a downstream 401.
func newPageClient() (*utils.PageClient, error) {
	apiKey, _ := utils.SetAPIConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("pages client: %w", utils.ErrMissingAPIKey)
	}
	c := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))
	return utils.NewPageClient(c), nil
}

// buildCreateParent resolves the (--parent, --parent-database) flag pair into
// a utils.PageParent suitable for CreatePageRequest. Exactly one of the two
// must be set; both empty or both populated is a usage error. The returned
// parent has only the matching id field set so the JSON encoder emits the
// `page_id` or `database_id` discriminator Notion expects, never both.
func buildCreateParent(pageID, databaseID string) (utils.PageParent, error) {
	switch {
	case pageID == "" && databaseID == "":
		return utils.PageParent{}, fmt.Errorf("create page: --parent or --parent-database is required")
	case pageID != "" && databaseID != "":
		return utils.PageParent{}, fmt.Errorf("create page: --parent and --parent-database are mutually exclusive")
	case databaseID != "":
		return utils.PageParent{DatabaseID: databaseID}, nil
	default:
		return utils.PageParent{PageID: pageID}, nil
	}
}

// parseProperty splits "key=value" from the --property flag and emits the
// Notion property payload that matches the value's intended type. Three
// forms are accepted, in priority order:
//
//  1. Raw JSON object pass-through:
//     Key={"select":{"name":"Done"}}
//     Anything starting with `{` is parsed as JSON and used verbatim.
//     Power-user escape hatch — the caller already knows the wire shape.
//
//  2. Type-prefixed shorthand:
//     Key=<type>:<value>
//     where <type> is one of: status, select, multi_select, number,
//     checkbox, date, url, email, phone (alias: phone_number), text,
//     title. Multi-select splits on commas, date accepts ISO 8601 plus
//     the `start..end` range form, number parses as float64.
//
//  3. Bare value: Key=Value
//     Falls back to rich_text. Preserves existing scripts that rely on
//     the historical default.
//
// Without this typed surface, every property that isn't title/rich_text
// (status, select, multi_select, number, date, checkbox, url, email,
// phone) 400s with "<key> is expected to be <type>" — see issue #51.
func parseProperty(raw string) (string, map[string]interface{}, error) {
	idx := strings.Index(raw, "=")
	if idx < 1 {
		return "", nil, fmt.Errorf("invalid --property %q, expected key=value", raw)
	}
	key := strings.TrimSpace(raw[:idx])
	value := raw[idx+1:]
	if key == "" {
		return "", nil, fmt.Errorf("invalid --property %q, key is empty", raw)
	}

	// 1. Raw JSON pass-through. The user already typed a Notion-shaped
	// payload and just wants it forwarded. We require it to be a JSON
	// object (not array/string/number) so a bare value of "{}" is the
	// only ambiguity, which is intentional — empty object is a valid
	// "clear this property" payload on Notion's side.
	if strings.HasPrefix(strings.TrimSpace(value), "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(value), &obj); err != nil {
			return "", nil, fmt.Errorf("invalid --property %q: looks like JSON but failed to parse: %w", raw, err)
		}
		return key, obj, nil
	}

	// 2. Type-prefixed shorthand. Only the first colon is treated as the
	// separator so values may contain colons (e.g. URLs, ISO times).
	if pIdx := strings.Index(value, ":"); pIdx > 0 {
		typ := value[:pIdx]
		rest := value[pIdx+1:]
		if payload, ok, err := buildTypedProperty(typ, rest); ok {
			if err != nil {
				return "", nil, fmt.Errorf("invalid --property %q: %w", raw, err)
			}
			return key, payload, nil
		}
	}

	// 3. Bare value → rich_text (back-compat).
	payload := map[string]interface{}{
		"rich_text": []map[string]interface{}{
			{
				"type": "text",
				"text": map[string]interface{}{"content": value},
			},
		},
	}
	return key, payload, nil
}

// buildTypedProperty translates a (type, value) pair into the property
// payload Notion expects. The bool return distinguishes "type is one we
// handle" (true, with maybe an err for malformed input) from "type
// prefix didn't match anything we know about" (false, no err) so the
// caller can fall through to the rich_text default for prefixes that
// happen to look like type:value but aren't.
func buildTypedProperty(typ, value string) (map[string]interface{}, bool, error) {
	switch typ {
	case "status":
		return map[string]interface{}{"status": map[string]interface{}{"name": value}}, true, nil
	case "select":
		return map[string]interface{}{"select": map[string]interface{}{"name": value}}, true, nil
	case "multi_select":
		// Comma-split; trim whitespace around each option name so
		// "a, b ,c" → ["a","b","c"]. An empty value (multi_select:)
		// emits an empty list which clears the property on Notion.
		var items []map[string]interface{}
		if value != "" {
			for _, name := range strings.Split(value, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				items = append(items, map[string]interface{}{"name": name})
			}
		}
		if items == nil {
			items = []map[string]interface{}{}
		}
		return map[string]interface{}{"multi_select": items}, true, nil
	case "number":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, true, fmt.Errorf("number value %q is not a valid float: %w", value, err)
		}
		return map[string]interface{}{"number": n}, true, nil
	case "checkbox":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, true, fmt.Errorf("checkbox value %q is not a boolean (try true/false): %w", value, err)
		}
		return map[string]interface{}{"checkbox": b}, true, nil
	case "date":
		// ISO 8601 single date or "start..end" range. We don't validate
		// the date format ourselves — Notion does that server-side and
		// surfaces a precise error. Empty value clears the property.
		if value == "" {
			return map[string]interface{}{"date": nil}, true, nil
		}
		if start, end, ok := strings.Cut(value, ".."); ok {
			return map[string]interface{}{"date": map[string]interface{}{"start": start, "end": end}}, true, nil
		}
		return map[string]interface{}{"date": map[string]interface{}{"start": value}}, true, nil
	case "url":
		return map[string]interface{}{"url": value}, true, nil
	case "email":
		return map[string]interface{}{"email": value}, true, nil
	case "phone", "phone_number":
		return map[string]interface{}{"phone_number": value}, true, nil
	case "text":
		// Explicit alias for the rich_text default — useful when the
		// value happens to start with "{" or contain "<known>:" and the
		// caller wants to force plain-text interpretation.
		return map[string]interface{}{
			"rich_text": []map[string]interface{}{
				{"type": "text", "text": map[string]interface{}{"content": value}},
			},
		}, true, nil
	case "title":
		return map[string]interface{}{
			"title": []map[string]interface{}{
				{"type": "text", "text": map[string]interface{}{"content": value}},
			},
		}, true, nil
	}
	return nil, false, nil
}

// pagesGetCmd retrieves a page by ID.
var pagesGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Retrieve a page by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		page, err := pc.Get(context.Background(), args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("get page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), page))
		}
		printPage(cmd.OutOrStdout(), page)
		// GET /v1/pages/{id} silently caps page and person references at
		// 25 per property. Notion gives no flag, so a property sitting
		// at exactly the cap is *probably* truncated — say so rather
		// than let the user compute over a partial set (issue #104).
		if truncated := utils.TruncatedProperties(page.Properties); len(truncated) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"note: %s reached the %d-reference cap and may be truncated; "+
					"read them in full with `notioncli pages property %s <property-id>`\n",
				strings.Join(truncated, ", "), utils.MaxPageReferencesInline, args[0])
		}
		return nil
	},
}

// pagesCreateCmd creates a new page under the provided parent.
//
// Notion distinguishes page parents (parent.page_id) from database parents
// (parent.database_id) at the wire level, so the CLI surfaces them as two
// mutually-exclusive flags: --parent for a page parent, --parent-database
// for a database row. Earlier versions accepted any id under --parent and
// always serialised it as page_id, which 400'd against database parents
// with "parent is expected to be database" — see PR #50 review.
var pagesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new page under --parent (page) or --parent-database",
	Long: `Create a page.

--title is a shortcut for the title property. For any other property type —
relation, people, status, multi-select, date, files — use --properties-json,
which is passed to Notion verbatim, so the full property system is reachable
without a flag per type.

--children-json seeds the page body with a JSON array of blocks.
--from-text seeds it from a text file, one paragraph per non-empty line.
That is NOT a markdown parser: headings, lists and emphasis are written as
literal text (see issue #45).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, err := buildCreateParent(pagesCreateParent, pagesCreateParentDB)
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		// --children-json, --from-text and --from-markdown all fill the
		// same body slot, so taking one silently would discard another's
		// file.
		if n := countSet(pagesCreateChildren, pagesCreateFromText, pagesCreateFromMD); n > 1 {
			return jsonErrorOr(cmd, fmt.Errorf(
				"create page: --children-json, --from-text and --from-markdown all set the page body; pass only one"))
		}
		props, err := readPagePropertiesFile(pagesCreateProps)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create page: %w", err))
		}
		children, err := readChildrenFile(pagesCreateChildren)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create page: %w", err))
		}
		if pagesCreateFromText != "" {
			if children, err = blocksFromPlainText(pagesCreateFromText); err != nil {
				return jsonErrorOr(cmd, fmt.Errorf("create page: %w", err))
			}
		}
		markdown, mdTitle, err := readMarkdownFile(pagesCreateFromMD)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create page: %w", err))
		}
		title := pagesCreateTitle
		if mdTitle != "" {
			// Notion drops a leading H1 on create without using it as the
			// title (see utils.SplitLeadingHeading). Promote it when the
			// caller gave no --title; warn, rather than guess, when they
			// did — silently discarding either one would be worse.
			if title == "" {
				title = mdTitle
			} else {
				errorLine(cmd, "warning: %s opens with %q, which Notion drops on create; --title %q wins",
					pagesCreateFromMD, "# "+mdTitle, title)
			}
		}

		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		page, err := pc.Create(cmd.Context(), utils.CreatePageRequest{
			Parent:     parent,
			Title:      title,
			Properties: props,
			Children:   children,
			Markdown:   markdown,
		})
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), page))
		}
		color.Green("Created page %s", page.ID)
		printPage(cmd.OutOrStdout(), page)
		return nil
	},
}

// pagesUpdateCmd patches title and/or --property key=value pairs on a page.
//
// Note: --property key=value values are always encoded as rich_text today.
// Typed properties (status, select, number, date, checkbox, url) will 400
// against that shape; callers that need a typed property should use the
// PageClient.Update API directly until a typed flag shape lands.
var pagesUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a page's title and/or properties",
	Long: `Update a page.

--property key=value is a convenience for STRING-valued properties only.
For any other type — relation, people, status, multi-select, date, files —
use --properties-json, which is passed to Notion verbatim.

When both are given, --properties-json is the base and --property overlays
it, so a script can reuse one template and vary a field per invocation.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		req := utils.UpdatePageRequest{Title: pagesUpdateTitle}

		// --properties-json is the base; --property overlays it. An
		// explicit flag on the command line beating a file is the least
		// surprising precedence, and it lets a script reuse one JSON
		// template while varying a field per invocation.
		jsonProps, err := readPagePropertiesFile(pagesUpdateProps2)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("update page: %w", err))
		}
		if len(jsonProps) > 0 {
			req.Properties = jsonProps
		}
		if len(pagesUpdateProps) > 0 {
			if req.Properties == nil {
				req.Properties = map[string]interface{}{}
			}
			for _, raw := range pagesUpdateProps {
				key, val, err := parseProperty(raw)
				if err != nil {
					return jsonErrorOr(cmd, err)
				}
				req.Properties[key] = val
			}
		}
		page, err := pc.Update(context.Background(), args[0], req)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("update page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), page))
		}
		color.Green("Updated page %s", page.ID)
		printPage(cmd.OutOrStdout(), page)
		return nil
	},
}

// pagesAppendMarkdownCmd adds markdown to a page without disturbing what
// is already on it.
//
// Body edits live on their own commands rather than as flags on `pages
// update` for two reasons: they hit a different endpoint
// (PATCH /pages/{id}/markdown, not PATCH /pages/{id}), so folding them in
// would make one command issue two calls with a partial-failure story;
// and the replacing form is destructive, which belongs in the command
// name where it cannot be missed.
var pagesAppendMarkdownCmd = &cobra.Command{
	Use:   "append-markdown <id>",
	Short: "Append a markdown file to a page's body",
	Long: `Append markdown to a page.

The markdown is parsed by Notion, not by this CLI, so the full dialect it
supports lands as real blocks — headings, lists, to-dos, code fences with
their language, quotes, dividers and inline emphasis.

--prepend inserts at the top of the page instead of the bottom. Nothing
already on the page is removed either way; see 'pages replace-markdown'
for that.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		md, err := readMarkdownBody(pagesAppendMDFrom)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("append markdown: %w", err))
		}
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		out, err := pc.AppendMarkdown(cmd.Context(), args[0], md, pagesAppendMDPrepend)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("append markdown: %w", err))
		}
		return emitMarkdownResult(cmd, out, "Appended markdown to page %s")
	},
}

// pagesReplaceMarkdownCmd replaces a page's whole body.
var pagesReplaceMarkdownCmd = &cobra.Command{
	Use:   "replace-markdown <id>",
	Short: "Replace a page's entire body with a markdown file",
	Long: `Replace everything on a page with the contents of a markdown file.

This is destructive: every block currently on the page is removed. Child
pages and child databases are NOT deleted — Notion gates those behind a
separate flag this command deliberately does not send, so losing a
sub-page can never be a side effect of editing text.

Use 'pages append-markdown' to add to a page instead.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		md, err := readMarkdownBody(pagesReplaceMDFrom)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("replace markdown: %w", err))
		}
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		out, err := pc.ReplaceMarkdown(cmd.Context(), args[0], md)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("replace markdown: %w", err))
		}
		return emitMarkdownResult(cmd, out, "Replaced body of page %s")
	},
}

// emitMarkdownResult reports what Notion made of the markdown it was
// sent. Both write endpoints echo the re-rendered page back, so the
// human path prints the same truncation and unknown-block warnings the
// read path does rather than a bare success line.
func emitMarkdownResult(cmd *cobra.Command, out *utils.PageMarkdown, format string) error {
	if globalJSON {
		return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), out))
	}
	color.Green(format, out.ID)
	if out.Truncated {
		errorLine(cmd, "warning: Notion truncated the rendered page")
	}
	if len(out.UnknownBlockIDs) > 0 {
		errorLine(cmd, "warning: %d block(s) could not be rendered as markdown: %s",
			len(out.UnknownBlockIDs), strings.Join(out.UnknownBlockIDs, ", "))
	}
	fmt.Fprint(cmd.OutOrStdout(), out.Markdown)
	if !strings.HasSuffix(out.Markdown, "\n") {
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}

// readMarkdownBody reads a markdown file for the body-editing commands.
// Unlike create, no leading-H1 promotion happens here: the page already
// has a title, and silently hoisting a heading out of the body would be
// an edit the caller did not ask for.
func readMarkdownBody(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--from is required")
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(buf)) == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return string(buf), nil
}

// readMarkdownFile reads a --from-markdown file for `pages create` and
// splits off a leading H1 for the caller to promote to the page title.
//
// See utils.SplitLeadingHeading: Notion silently drops a leading H1 on
// create and does not use it as the title either, so forwarding a normal
// markdown file verbatim loses its most important line.
func readMarkdownFile(path string) (body, title string, err error) {
	if path == "" {
		return "", "", nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(buf)) == "" {
		return "", "", fmt.Errorf("%s is empty; omit --from-markdown instead of passing an empty file", path)
	}
	heading, rest, found := utils.SplitLeadingHeading(string(buf))
	if !found {
		return string(buf), "", nil
	}
	return rest, heading, nil
}

// countSet returns how many of the given flag values are non-empty. Used
// to reject mutually exclusive body sources.
func countSet(values ...string) int {
	n := 0
	for _, v := range values {
		if v != "" {
			n++
		}
	}
	return n
}

// pagesArchiveCmd archives a page.
var pagesArchiveCmd = &cobra.Command{
	Use:   "archive <id>",
	Short: "Archive a page",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		if err := pc.Archive(context.Background(), args[0]); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("archive page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "archive",
				"id":     args[0],
			}))
		}
		color.Green("Archived page %s", args[0])
		return nil
	},
}

// pagesUnarchiveCmd unarchives a page.
var pagesUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <id>",
	Short: "Unarchive a page",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		if err := pc.Unarchive(context.Background(), args[0]); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("unarchive page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "unarchive",
				"id":     args[0],
			}))
		}
		color.Green("Unarchived page %s", args[0])
		return nil
	},
}

// pagesMoveCmd reparents a page. Only page-parent moves are supported; to
// move a page into a database parent, use the lower-level PageClient.Update
// with a PageParent{DatabaseID: ...}.
var pagesMoveCmd = &cobra.Command{
	Use:   "move <id>",
	Short: "Move a page to a new page parent (--parent). Use the API for database parents.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if pagesMoveParent == "" {
			return jsonErrorOr(cmd, fmt.Errorf("move page: --parent is required"))
		}
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		if err := pc.Move(context.Background(), args[0], pagesMoveParent); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("move page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "move",
				"id":     args[0],
				"parent": pagesMoveParent,
			}))
		}
		color.Green("Moved page %s → parent %s", args[0], pagesMoveParent)
		return nil
	},
}

// pagesDuplicateCmd emulates a Notion page duplicate.
//
// Notion has no native duplicate endpoint; this command performs a
// best-effort copy by fetching the source's top-level blocks, creating a new
// page under --parent, and appending those blocks. Nested databases and
// blocks with has_children=true are NOT recursively copied.
var pagesDuplicateCmd = &cobra.Command{
	Use:   "duplicate <id>",
	Short: "Duplicate a page under --parent (top-level blocks only)",
	Long: `Duplicate a Notion page under a new parent.

Notion does not expose a native duplicate endpoint. This command emulates
one by (1) fetching the source page and its children, (2) creating a new
page under --parent with the source's title, and (3) appending those
children.

Limitations:
  - Only top-level blocks are copied. Nested blocks where has_children=true
    are NOT recursed into.
  - Unsupported top-level block types (image, file, video, bookmark, embed,
    child_page, child_database, table, column_list, equation, synced_block)
    are silently skipped.
  - Child databases are not re-created.
  - Property values from database-parented sources are not carried over.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if pagesDuplicateParent == "" {
			return jsonErrorOr(cmd, fmt.Errorf("duplicate page: --parent is required"))
		}
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		page, err := pc.Duplicate(context.Background(), args[0], pagesDuplicateParent)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("duplicate page: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), page))
		}
		color.Green("Duplicated page %s → %s", args[0], page.ID)
		printPage(cmd.OutOrStdout(), page)
		return nil
	},
}

// printPage writes a human-readable JSON blob to the given writer. This is
// the v1 output format for every pages subcommand — a proper --json vs.
// table split will land with the wider --json rollout on the roadmap. The
// writer is threaded through cmd.OutOrStdout() so tests can capture output
// via cmd.SetOut.
// pageSpec is one entry of a create-many input file.
//
// The parent keys deliberately mirror the flag names on `pages create`
// rather than Notion's wire shape. A single "parent": "<id>" cannot say
// whether the id is a page or a database, and guessing wrong writes the
// row into the wrong surface; naming the two keys separately removes the
// ambiguity using vocabulary the caller already knows from the flags.
type pageSpec struct {
	Parent     string                   `json:"parent"`
	ParentDB   string                   `json:"parent_database"`
	Title      string                   `json:"title"`
	Properties map[string]interface{}   `json:"properties"`
	Children   []map[string]interface{} `json:"children"`
}

var pagesCreateManyCmd = &cobra.Command{
	Use:   "create-many",
	Short: "Create many pages from a JSON array or JSONL file",
	Long: `Create one page per entry in --from, in file order.

The file is either a JSON array or a JSONL stream (one object per line);
the form is detected from the first non-space byte, so both work without
a flag. Each entry:

  {"parent": "<page-id>", "parent_database": "<db-id>",
   "title": "...", "properties": {...}, "children": [...]}

"parent" is a PAGE id and "parent_database" is a DATABASE id — the same
split as the flags of the same name, because one "parent" id cannot say
which surface it names. --parent / --parent-database supply the default
for entries that give neither.

properties and children are passed to Notion verbatim, exactly as with
'pages create --properties-json'.

Notion has no bulk-create endpoint, so pages are POSTed one at a time and
each is reported as it lands. --on-error abort (the default) stops at the
first failure; --on-error continue attempts every entry and reports the
failures at the end. Under either, the pages that were created are still
written to stdout: a partial import has to be reportable, or the operator
cannot tell what not to re-run.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var abort bool
		switch pagesCreateManyOnErr {
		case "abort", "":
			abort = true
		case "continue":
			abort = false
		default:
			return jsonErrorOr(cmd, fmt.Errorf(
				"create pages: invalid --on-error %q (want abort|continue)", pagesCreateManyOnErr))
		}
		if pagesCreateManyFrom == "" {
			return jsonErrorOr(cmd, fmt.Errorf("create pages: --from is required"))
		}
		specs, err := readPageSpecsFile(pagesCreateManyFrom, pagesCreateManyParent, pagesCreateManyParentDB)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create pages: %w", err))
		}
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}

		// Stream each outcome as it happens. At Notion's few-per-second
		// ceiling a large import runs for minutes, and a command that
		// prints nothing until the end is indistinguishable from a hang.
		out, errW := cmd.OutOrStdout(), cmd.ErrOrStderr()
		total := len(specs)
		onEach := func(i int, page *utils.Page, err error) {
			switch {
			case err != nil && globalJSON:
				// stderr stays line-delimited JSON in --json mode (#64),
				// so a per-entry failure is an envelope, not a red line.
				// N failures produce N envelopes plus the closing summary
				// one — every line still parses, and a partial import is
				// exactly N+1 distinct facts.
				emitError(errW, err)
			case err != nil:
				fmt.Fprintln(errW, color.RedString("[%d/%d] %v", i+1, total, err))
			case globalJSON:
				_ = emitJSON(out, page)
			default:
				fmt.Fprintf(out, "[%d/%d] created %s\n", i+1, total, page.ID)
			}
		}

		created, errs := pc.CreateMany(cmd.Context(), specs, abort, onEach)
		if !globalJSON {
			color.New(color.FgGreen).Fprintf(out, "Created %d of %d page(s)\n", len(created), total)
		}
		if len(errs) > 0 {
			// Non-zero exit even under --on-error continue: the successes
			// are already on stdout, and a script must be able to tell a
			// partial import from a clean one without diffing counts.
			return jsonErrorOr(cmd, fmt.Errorf(
				"create pages: %d of %d entries failed (first: %v)", len(errs), total, errs[0]))
		}
		return nil
	},
}

// readPageSpecsFile parses a create-many input file into create requests.
//
// Both a JSON array and a JSONL stream are accepted, decided by the first
// non-space byte. json.Decoder handles the stream case natively, so the
// two forms share one loop rather than one parser each.
func readPageSpecsFile(path, defaultParent, defaultParentDB string) ([]utils.CreatePageRequest, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	trimmed := bytes.TrimSpace(buf)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}

	var raw []pageSpec
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, fmt.Errorf("parse %s as a JSON array of page specs: %w", path, err)
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		for n := 1; ; n++ {
			var spec pageSpec
			if err := dec.Decode(&spec); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return nil, fmt.Errorf("parse %s as JSONL, entry %d: %w", path, n, err)
			}
			raw = append(raw, spec)
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s contains no page specs", path)
	}

	specs := make([]utils.CreatePageRequest, 0, len(raw))
	for i, r := range raw {
		parent, parentDB := r.Parent, r.ParentDB
		// The flags are a default, not an override: an entry that names
		// its own parent keeps it, so one file can span several parents
		// while --parent-database still covers the common single-target
		// import.
		if parent == "" && parentDB == "" {
			parent, parentDB = defaultParent, defaultParentDB
		}
		pp, err := buildCreateParent(parent, parentDB)
		if err != nil {
			// buildCreateParent phrases its errors for the flags; entry
			// N of a file needs to say which entry and which keys.
			return nil, fmt.Errorf("entry %d (%s): set \"parent\" or \"parent_database\" on the entry, or pass --parent/--parent-database as a default (%v)",
				i+1, specLabel(r), err)
		}
		specs = append(specs, utils.CreatePageRequest{
			Parent:     pp,
			Title:      r.Title,
			Properties: r.Properties,
			Children:   r.Children,
		})
	}
	return specs, nil
}

// specLabel names an entry in a parse error, preferring its title.
func specLabel(r pageSpec) string {
	if r.Title != "" {
		return r.Title
	}
	return "untitled"
}

func printPage(w io.Writer, page *utils.Page) {
	if page == nil {
		return
	}
	if w == nil {
		w = os.Stdout
	}
	b, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, color.RedString("Error formatting page: %v", err))
		return
	}
	fmt.Fprintln(w, string(b))
}

// pagesPropertyCmd reads a single page property in full.
//
// GET /v1/pages/{id} caps page and person references at 25 per property and
// gives no signal when it truncates — so `pages get` on a relation with 40
// entries reported the first 25 as though they were everything, and any
// script computing over relations got a wrong answer that looked right.
// This is the documented escape hatch (issue #104).
var pagesPropertyCmd = &cobra.Command{
	Use:   "property <page-id> <property-id>",
	Short: "Read one page property in full, past the 25-reference cap",
	Long: `Read a single page property, following pagination.

` + "`pages get`" + ` returns at most 25 page or person references per
property and does not say when it truncated. Use this to read a large
relation or people property completely. Property ids come from
` + "`pages get <id> --json`" + ` (each property carries an "id").`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		items, err := pc.GetPropertyItem(context.Background(), args[0], args[1])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("pages property: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitList(cmd.OutOrStdout(), items))
		}
		w := cmd.OutOrStdout()
		for _, it := range items {
			fmt.Fprintln(w, string(it))
		}
		color.Cyan("  %d item(s)", len(items))
		return nil
	},
}

// pagesMarkdownCmd renders a whole page as markdown.
//
// This is the only single command that returns a page's COMPLETE content.
// `blocks list` returns top-level blocks and never recurses, so toggles,
// columns and tables hide everything beneath them — and say nothing about
// it. "Read this Notion page" is the most common thing anyone wants from a
// Notion CLI and was, until now, not possible in one call (issue #109).
var pagesMarkdownCmd = &cobra.Command{
	Use:   "markdown <page-id>",
	Short: "Render a whole page as markdown, including nested content",
	Long: `Render a page as markdown via Notion's own renderer.

Unlike ` + "`blocks list`" + `, this includes nested content — toggles,
columns, tables, synced blocks. Notion reports when it truncated the output
or could not render particular blocks, and both are surfaced on stderr
rather than dropped.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		md, err := pc.GetMarkdown(context.Background(), args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("pages markdown: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), md))
		}
		fmt.Fprintln(cmd.OutOrStdout(), md.Markdown)

		// Report incompleteness rather than let the user assume they have
		// the whole page. Warnings go to stderr so `pages markdown x > out.md`
		// still produces clean markdown.
		if md.Truncated {
			fmt.Fprintln(cmd.ErrOrStderr(),
				"warning: Notion truncated this page's markdown — the output above is incomplete")
		}
		if n := len(md.UnknownBlockIDs); n > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: %d block(s) could not be rendered as markdown and are missing above: %s\n",
				n, strings.Join(md.UnknownBlockIDs, ", "))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pagesCmd)
	pagesCmd.AddCommand(pagesGetCmd)
	pagesCmd.AddCommand(pagesPropertyCmd)
	pagesCmd.AddCommand(pagesMarkdownCmd)
	pagesCmd.AddCommand(pagesCreateCmd)
	pagesCmd.AddCommand(pagesCreateManyCmd)
	pagesCmd.AddCommand(pagesAppendMarkdownCmd)
	pagesCmd.AddCommand(pagesReplaceMarkdownCmd)
	pagesCmd.AddCommand(pagesUpdateCmd)
	pagesCmd.AddCommand(pagesArchiveCmd)
	pagesCmd.AddCommand(pagesUnarchiveCmd)
	pagesCmd.AddCommand(pagesMoveCmd)
	pagesCmd.AddCommand(pagesDuplicateCmd)

	pagesCreateCmd.Flags().StringVar(&pagesCreateParent, "parent", "", "Parent page ID (mutually exclusive with --parent-database)")
	pagesCreateCmd.Flags().StringVar(&pagesCreateParentDB, "parent-database", "", "Parent database ID for database-parented pages (mutually exclusive with --parent)")
	pagesCreateCmd.Flags().StringVar(&pagesCreateProps, "properties-json", "", "Path to a JSON object of Notion property values, passed through verbatim")
	pagesCreateCmd.Flags().StringVar(&pagesCreateChildren, "children-json", "", "Path to a JSON array of blocks for the page body")
	pagesCreateCmd.Flags().StringVar(&pagesCreateFromText, "from-text", "", "Path to a text file; each non-empty line becomes a paragraph block (not a markdown parser — see #45)")
	pagesCreateCmd.Flags().StringVar(&pagesCreateFromMD, "from-markdown", "", "Path to a markdown file; parsed by Notion into real blocks. A leading '# Heading' becomes the page title")
	pagesCreateCmd.Flags().StringVar(&pagesCreateTitle, "title", "", "Title for the new page")

	pagesAppendMarkdownCmd.Flags().StringVar(&pagesAppendMDFrom, "from", "", "Path to the markdown file to append (required)")
	pagesAppendMarkdownCmd.Flags().BoolVar(&pagesAppendMDPrepend, "prepend", false, "Insert at the top of the page instead of the bottom")
	pagesReplaceMarkdownCmd.Flags().StringVar(&pagesReplaceMDFrom, "from", "", "Path to the markdown file to replace the page body with (required)")

	pagesCreateManyCmd.Flags().StringVar(&pagesCreateManyFrom, "from", "", "Path to a JSON array or JSONL file of page specs (required)")
	pagesCreateManyCmd.Flags().StringVar(&pagesCreateManyOnErr, "on-error", "abort", "What to do when an entry fails: abort|continue")
	pagesCreateManyCmd.Flags().StringVar(&pagesCreateManyParent, "parent", "", "Default parent page ID for entries that name no parent")
	pagesCreateManyCmd.Flags().StringVar(&pagesCreateManyParentDB, "parent-database", "", "Default parent database ID for entries that name no parent")

	pagesUpdateCmd.Flags().StringVar(&pagesUpdateTitle, "title", "", "New title for the page")
	pagesUpdateCmd.Flags().StringVar(&pagesUpdateProps2, "properties-json", "", "Path to a JSON object of Notion property values, passed through verbatim; covers types --property cannot")
	pagesUpdateCmd.Flags().StringArrayVar(&pagesUpdateProps, "property", nil,
		`Set a property (repeatable). Three forms:
  Key=Value                        rich_text (back-compat)
  Key=<type>:<value>               typed: status, select, multi_select,
                                     number, checkbox, date, url, email,
                                     phone, text, title
  Key={"select":{"name":"Done"}}   raw JSON pass-through

Examples:
  --property "Status=status:Done"
  --property "Brand=select:FacetInteractive.com"
  --property "Tags=multi_select:alpha,beta"
  --property "Count=number:42"
  --property "Done=checkbox:true"
  --property "Due=date:2026-05-01..2026-05-08"`)

	pagesMoveCmd.Flags().StringVar(&pagesMoveParent, "parent", "", "New parent page ID (required)")

	pagesDuplicateCmd.Flags().StringVar(&pagesDuplicateParent, "parent", "", "Parent page ID for the duplicate (required)")
}

// readPagePropertiesFile reads a --properties-json file into the loose
// property map POST/PATCH /v1/pages expects.
//
// The payload is passed through VERBATIM. Notion's property-value system
// has ~20 shapes (relation, people, status, rollup, formula, files, …) and
// modelling them here would be a large typed surface that goes stale every
// time Notion adds one — the reason issue #40 asks for a JSON passthrough
// rather than a flag per type.
func readPagePropertiesFile(path string) (map[string]interface{}, error) {
	if path == "" {
		return nil, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(buf)) == 0 {
		return nil, fmt.Errorf("%s is empty; omit --properties-json instead of passing an empty file", path)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("parse %s as a properties object: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s contains no properties", path)
	}
	return out, nil
}

// readChildrenFile reads a --children-json file into the block array the
// create body expects. Also passed through verbatim, for the same reason.
func readChildrenFile(path string) ([]map[string]interface{}, error) {
	if path == "" {
		return nil, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(buf)) == 0 {
		return nil, fmt.Errorf("%s is empty; omit --children-json instead of passing an empty file", path)
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("parse %s as a JSON array of blocks: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s contains no blocks", path)
	}
	return out, nil
}

// blocksFromPlainText turns a text file into one paragraph block per
// non-empty line.
//
// This is deliberately NOT a markdown parser. It does not read headings,
// lists, links or emphasis — every line becomes a paragraph verbatim. Real
// markdown-to-blocks conversion is issue #45; promising it here would mean
// silently dropping the formatting a user wrote. The flag is named
// --from-text rather than #40's proposed --from-markdown for exactly that
// reason: the name should not claim a fidelity the code does not have.
//
// Blank lines are skipped: Notion renders an empty paragraph as visible
// dead space, and a file's blank lines are almost always separators rather
// than content.
func blocksFromPlainText(path string) ([]map[string]interface{}, error) {
	if path == "" {
		return nil, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var blocks []map[string]interface{}
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		blocks = append(blocks, map[string]interface{}{
			"object": "block",
			"type":   "paragraph",
			"paragraph": map[string]interface{}{
				"rich_text": []map[string]interface{}{
					{"type": "text", "text": map[string]interface{}{"content": line}},
				},
			},
		})
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("%s has no non-empty lines", path)
	}
	return blocks, nil
}
