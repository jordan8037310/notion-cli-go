// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var blockType string

// blocksCmd represents the blocks command
var blocksCmd = &cobra.Command{
	Use:   "blocks",
	Short: "Manage all block types on a Notion page",
	Long: `The blocks command allows you to work with all Notion block types,
not just to-do items. Supported block types:

  paragraph, heading_1, heading_2, heading_3,
  bulleted_list_item, numbered_list_item, to_do,
  toggle, quote, callout, divider, code

Examples:
  notioncli blocks list              # List all blocks
  notioncli blocks list --type to_do # List only to-do blocks
  notioncli blocks add "Hello"       # Add a paragraph
  notioncli blocks add "Title" -t heading_1  # Add a heading
  notioncli blocks delete 5          # Delete block at index 5`,
}

// blocksListCmd lists all blocks
var blocksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all blocks on the page",
	Long:  `List all blocks on the Notion page with their type and content.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		notionAPIKey, pageID := utils.SetAPIConfig()
		// In --json mode skip the timezone lookup (it is only needed to
		// format timestamps for human output) and emit raw Notion block
		// objects as NDJSON.
		if globalJSON {
			blocks, err := utils.GetAllBlocks(notionAPIKey, pageID, blockType)
			if err != nil {
				return jsonErrorOr(cmd, fmt.Errorf("blocks list: %w", err))
			}
			return jsonErrorOr(cmd, emitList(cmd.OutOrStdout(), blocks))
		}

		localTimezone, err := utils.GetLocalTimeZone()
		if err != nil {
			color.Red("Error getting timezone: %v", err)
			return nil
		}

		formatted, typeCounts, err := utils.FormatAllBlocks(
			notionAPIKey,
			pageID,
			localTimezone,
			blockType,
		)
		if err != nil {
			color.Red("Error: %v", err)
			return nil
		}

		if len(formatted) == 0 {
			if blockType != "" {
				color.Yellow("No blocks of type '%s' found.", blockType)
			} else {
				color.Yellow("No blocks found on this page.")
			}
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout())
		for _, line := range formatted {
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
		fmt.Fprintln(cmd.OutOrStdout())

		// Print summary
		var summary []string
		for t, count := range typeCounts {
			summary = append(summary, fmt.Sprintf("%d %s", count, t))
		}
		sort.Strings(summary)

		total := len(formatted)
		color.Cyan("  %d blocks: %s\n", total, strings.Join(summary, ", "))
		return nil
	},
}

// blocksAddCmd adds a new block
var blocksAddCmd = &cobra.Command{
	Use:   "add [text]",
	Short: "Add a new block to the page",
	Long: `Add a new block of the specified type. Default type is 'paragraph'.

Supported types:
  paragraph, heading_1, heading_2, heading_3,
  bulleted_list_item, numbered_list_item, to_do,
  toggle, quote, callout, divider, code

Examples:
  notioncli blocks add "Hello world"           # Add paragraph
  notioncli blocks add "Section Title" -t heading_1
  notioncli blocks add "" -t divider           # Add divider (no text needed)
  notioncli blocks add "Buy milk" -t to_do`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := args[0]

		// Validate block type
		if blockType == "" {
			blockType = "paragraph"
		}

		if !utils.IsValidBlockType(blockType) {
			err := fmt.Errorf("unsupported block type %q (supported: %s)",
				blockType, strings.Join(utils.GetSupportedBlockTypeNames(), ", "))
			if globalJSON {
				return jsonErrorOr(cmd, err)
			}
			color.Red("Error: %v", err)
			return nil
		}

		notionAPIKey, pageID := utils.SetAPIConfig()

		if err := utils.AddBlock(notionAPIKey, pageID, blockType, text); err != nil {
			if globalJSON {
				return jsonErrorOr(cmd, fmt.Errorf("blocks add: %w", err))
			}
			color.Red("Error adding block: %v", err)
			return nil
		}

		if globalJSON {
			return emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "add",
				"type":   blockType,
				"text":   text,
			})
		}

		icon := utils.SupportedBlockTypes[blockType].Icon
		if blockType == "divider" {
			color.Green("Added %s divider", icon)
		} else {
			color.Green("Added %s %s: %s", icon, blockType, text)
		}
		return nil
	},
}

// blocksDeleteCmd deletes a block by index
var blocksDeleteCmd = &cobra.Command{
	Use:   "delete [number]",
	Short: "Delete a block by its index number",
	Long: `Delete any block from the page by its index number.
Use 'notioncli blocks list' to see block numbers.

Example:
  notioncli blocks delete 3`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		order, err := strconv.Atoi(args[0])
		if err != nil {
			wrapped := fmt.Errorf("blocks delete: %q is not a valid number: %w", args[0], err)
			if globalJSON {
				return jsonErrorOr(cmd, wrapped)
			}
			color.Red("Error: '%s' is not a valid number", args[0])
			return nil
		}

		notionAPIKey, pageID := utils.SetAPIConfig()

		if err := utils.DeleteBlock(notionAPIKey, pageID, order); err != nil {
			if globalJSON {
				return jsonErrorOr(cmd, fmt.Errorf("blocks delete: %w", err))
			}
			color.Red("Error deleting block: %v", err)
			return nil
		}

		if globalJSON {
			return emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "delete",
				"order":  order,
			})
		}
		color.Green("Deleted block %d", order)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(blocksCmd)
	blocksCmd.AddCommand(blocksListCmd)
	blocksCmd.AddCommand(blocksAddCmd)
	blocksCmd.AddCommand(blocksDeleteCmd)

	// Flags for list command
	blocksListCmd.Flags().StringVarP(&blockType, "type", "t", "", "Filter by block type")

	// Flags for add command
	blocksAddCmd.Flags().StringVarP(&blockType, "type", "t", "paragraph", "Block type to add")
}
