// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Flag vars for the pages subcommands. Keeping them package-level mirrors
// blocks.go's pattern and lets cobra bind them via init().
var (
	pagesCreateParent    string
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
  notioncli pages create --parent <page-or-db-id> --title "New page"
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
	Run: func(cmd *cobra.Command, args []string) {
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		page, err := pc.Get(context.Background(), args[0])
		if err != nil {
			color.Red("Error getting page: %v", err)
			return
		}
		printPage(page)
	},
}

// pagesCreateCmd creates a new page under the provided parent.
var pagesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new page under --parent",
	Run: func(cmd *cobra.Command, args []string) {
		if pagesCreateParent == "" {
			color.Red("Error: --parent is required")
			return
		}
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		page, err := pc.Create(context.Background(), utils.CreatePageRequest{
			Parent: utils.PageParent{PageID: pagesCreateParent},
			Title:  pagesCreateTitle,
		})
		if err != nil {
			color.Red("Error creating page: %v", err)
			return
		}
		color.Green("Created page %s", page.ID)
		printPage(page)
	},
}

// pagesUpdateCmd patches title and/or --property key=value pairs on a page.
var pagesUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a page's title and/or properties",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		req := utils.UpdatePageRequest{Title: pagesUpdateTitle}
		if len(pagesUpdateProps) > 0 {
			req.Properties = map[string]interface{}{}
			for _, raw := range pagesUpdateProps {
				key, val, err := parseProperty(raw)
				if err != nil {
					color.Red("Error: %v", err)
					return
				}
				req.Properties[key] = val
			}
		}
		page, err := pc.Update(context.Background(), args[0], req)
		if err != nil {
			color.Red("Error updating page: %v", err)
			return
		}
		color.Green("Updated page %s", page.ID)
		printPage(page)
	},
}

// pagesArchiveCmd archives a page.
var pagesArchiveCmd = &cobra.Command{
	Use:   "archive <id>",
	Short: "Archive a page",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		if err := pc.Archive(context.Background(), args[0]); err != nil {
			color.Red("Error archiving page: %v", err)
			return
		}
		color.Green("Archived page %s", args[0])
	},
}

// pagesUnarchiveCmd unarchives a page.
var pagesUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <id>",
	Short: "Unarchive a page",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		if err := pc.Unarchive(context.Background(), args[0]); err != nil {
			color.Red("Error unarchiving page: %v", err)
			return
		}
		color.Green("Unarchived page %s", args[0])
	},
}

// pagesMoveCmd reparents a page.
var pagesMoveCmd = &cobra.Command{
	Use:   "move <id>",
	Short: "Move a page to a new parent (--parent)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if pagesMoveParent == "" {
			color.Red("Error: --parent is required")
			return
		}
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		if err := pc.Move(context.Background(), args[0], pagesMoveParent); err != nil {
			color.Red("Error moving page: %v", err)
			return
		}
		color.Green("Moved page %s → parent %s", args[0], pagesMoveParent)
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
one by (1) fetching the source page's children, (2) creating a new page
under --parent with the source's title, and (3) appending those children.

Limitations:
  - Only top-level blocks are copied. Nested blocks where has_children=true
    are NOT recursed into.
  - Child databases are not re-created.
  - Property values from database-parented sources are not carried over.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if pagesDuplicateParent == "" {
			color.Red("Error: --parent is required")
			return
		}
		pc, err := newPageClient()
		if err != nil {
			color.Red("Error: %v", err)
			return
		}
		page, err := pc.Duplicate(context.Background(), args[0], pagesDuplicateParent)
		if err != nil {
			color.Red("Error duplicating page: %v", err)
			return
		}
		color.Green("Duplicated page %s → %s", args[0], page.ID)
		printPage(page)
	},
}

// printPage writes a human-readable JSON blob to stdout. This is the v1
// output format for every pages subcommand — a proper --json vs. table split
// will land with the wider --json rollout on the roadmap.
func printPage(page *utils.Page) {
	if page == nil {
		return
	}
	b, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		color.Red("Error formatting page: %v", err)
		return
	}
	fmt.Println(string(b))
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

	pagesCreateCmd.Flags().StringVar(&pagesCreateParent, "parent", "", "Parent page or database ID (required)")
	pagesCreateCmd.Flags().StringVar(&pagesCreateTitle, "title", "", "Title for the new page")

	pagesUpdateCmd.Flags().StringVar(&pagesUpdateTitle, "title", "", "New title for the page")
	pagesUpdateCmd.Flags().StringArrayVar(&pagesUpdateProps, "property", nil, "Set a property as key=value (repeatable)")

	pagesMoveCmd.Flags().StringVar(&pagesMoveParent, "parent", "", "New parent page ID (required)")

	pagesDuplicateCmd.Flags().StringVar(&pagesDuplicateParent, "parent", "", "Parent page ID for the duplicate (required)")
}
