// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Flag vars for the pages subcommands. Keeping them package-level mirrors
// blocks.go's pattern and lets cobra bind them via init().
var (
	pagesCreateParent    string
	pagesCreateParentDB  string
	pagesCreateTitle     string
	pagesUpdateTitle     string
	pagesUpdateProps     []string
	pagesMoveParent      string
	pagesDuplicateParent string
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

// parseProperty splits "key=value" from the --property flag. Values are sent
// as plain-text rich_text entries because without a schema lookup we cannot
// infer the target property type; callers that need typed properties should
// use the lower-level PageClient.Update with a full Properties map.
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
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, err := buildCreateParent(pagesCreateParent, pagesCreateParentDB)
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		page, err := pc.Create(context.Background(), utils.CreatePageRequest{
			Parent: parent,
			Title:  pagesCreateTitle,
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
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		req := utils.UpdatePageRequest{Title: pagesUpdateTitle}
		if len(pagesUpdateProps) > 0 {
			req.Properties = map[string]interface{}{}
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
func printPage(w io.Writer, page *utils.Page) {
	if page == nil {
		return
	}
	if w == nil {
		w = os.Stdout
	}
	b, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		color.Red("Error formatting page: %v", err)
		return
	}
	fmt.Fprintln(w, string(b))
}

func init() {
	rootCmd.AddCommand(pagesCmd)
	pagesCmd.AddCommand(pagesGetCmd)
	pagesCmd.AddCommand(pagesCreateCmd)
	pagesCmd.AddCommand(pagesUpdateCmd)
	pagesCmd.AddCommand(pagesArchiveCmd)
	pagesCmd.AddCommand(pagesUnarchiveCmd)
	pagesCmd.AddCommand(pagesMoveCmd)
	pagesCmd.AddCommand(pagesDuplicateCmd)

	pagesCreateCmd.Flags().StringVar(&pagesCreateParent, "parent", "", "Parent page ID (mutually exclusive with --parent-database)")
	pagesCreateCmd.Flags().StringVar(&pagesCreateParentDB, "parent-database", "", "Parent database ID for database-parented pages (mutually exclusive with --parent)")
	pagesCreateCmd.Flags().StringVar(&pagesCreateTitle, "title", "", "Title for the new page")

	pagesUpdateCmd.Flags().StringVar(&pagesUpdateTitle, "title", "", "New title for the page")
	pagesUpdateCmd.Flags().StringArrayVar(&pagesUpdateProps, "property", nil, "Set a property as key=value (repeatable)")

	pagesMoveCmd.Flags().StringVar(&pagesMoveParent, "parent", "", "New parent page ID (required)")

	pagesDuplicateCmd.Flags().StringVar(&pagesDuplicateParent, "parent", "", "Parent page ID for the duplicate (required)")
}
