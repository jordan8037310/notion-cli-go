// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	// blockType backs --type on `blocks add` only. It MUST NOT be shared
	// with any other command's flag: StringVarP writes its default into
	// the bound variable at *registration* time, not at parse time, so a
	// second registration silently overwrites the first command's zero
	// value. That is issue #88 — `blocks list` inherited add's
	// "paragraph" default and filtered every listing down to paragraphs
	// with no error.
	blockType string
	// blocksListType backs --type on `blocks list`. Separate storage from
	// blockType by design; see the note above.
	blocksListType string
	blockURL       string
	blockCaption   string
	blockFileID    string
	blockLanguage  string
	// blocksAddRichTextJSON backs the --rich-text-json flag on
	// `blocks add`. When non-empty, the command reads the file, parses
	// it via utils.ParseRichTextJSON, and dispatches through
	// utils.AddRichTextBlock instead of the single-segment AddBlock
	// path. Mutually exclusive with the positional text arg.
	blocksAddRichTextJSON string
)

// blocksCmd represents the blocks command
var blocksCmd = &cobra.Command{
	Use:   "blocks",
	Short: "Manage all block types on a Notion page",
	Long: `The blocks command allows you to work with all Notion block types,
not just to-do items. Supported block types:

  paragraph, heading_1, heading_2, heading_3,
  bulleted_list_item, numbered_list_item, to_do,
  toggle, quote, callout, divider, code,
  image, file, video, embed, bookmark, equation

Layout / structural types (table, table_row, synced_block, column_list,
column) are surfaced by ` + "`blocks list`" + ` but cannot be created through
` + "`blocks add`" + ` — they require children or references and will be
addable via a JSON-payload command in a future release.

Examples:
  notioncli blocks list              # List all blocks
  notioncli blocks list --type to_do # List only to-do blocks
  notioncli blocks add "Hello"       # Add a paragraph
  notioncli blocks add "Title" -t heading_1  # Add a heading
  notioncli blocks add "https://example.com/pic.png" -t image
  notioncli blocks add "caption" -t bookmark --url https://example.com
  notioncli blocks add "E=mc^2" -t equation
  notioncli blocks delete 5          # Delete block at index 5`,
}

// blocksListCmd lists all blocks
var blocksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all blocks on the page",
	Long:  `List all blocks on the Notion page with their type and content.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Suppress cobra's default "Error: ..." + usage block on
		// non-nil RunE return so the colored errors below are the
		// only error the user sees. Without this, cobra would
		// double-print every error (once via color.Red, once via
		// cobra) and dump the help block under each failure. Closes
		// the exit-code half of #74.
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true

		notionAPIKey, _ := utils.SetAPIConfig()
		pageID, err := resolvePageID()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("blocks list: %w", err))
		}
		// In --json mode skip the timezone lookup (it is only needed to
		// format timestamps for human output) and emit raw Notion block
		// objects as NDJSON.
		if globalJSON {
			// Emit Notion's own bytes, not a re-marshalled Block. The
			// typed struct models only the block types the human path
			// renders, so re-encoding it silently empties child_page,
			// child_database, synced_block metadata and any newer shape
			// — issue #86.
			//
			// blocksListType (not blockType) is the list command's own
			// filter variable — see issue #88.
			_, raws, err := utils.GetAllBlocksRaw(notionAPIKey, pageID, blocksListType)
			if err != nil {
				return jsonErrorOr(cmd, fmt.Errorf("blocks list: %w", err))
			}
			return jsonErrorOr(cmd, emitList(cmd.OutOrStdout(), raws))
		}

		localTimezone, err := utils.GetLocalTimeZone()
		if err != nil {
			wrapped := fmt.Errorf("blocks list: resolve local time zone: %w", err)
			color.Red("Error getting timezone: %v", err)
			return wrapped
		}

		// --resolve-mentions (persistent) opts into page-title
		// expansion for the snippet column. When off, buildPageResolver
		// returns utils.NoPageResolver{} and the output stays
		// byte-identical to the pre-resolver rendering.
		resolver := buildPageResolver(notionAPIKey)
		formatted, typeCounts, err := utils.FormatAllBlocksWithResolver(
			notionAPIKey,
			pageID,
			localTimezone,
			blocksListType,
			resolver,
		)
		if err != nil {
			wrapped := fmt.Errorf("blocks list: %w", err)
			color.Red("Error: %v", err)
			return wrapped
		}

		if len(formatted) == 0 {
			if blocksListType != "" {
				color.Yellow("No blocks of type '%s' found.", blocksListType)
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
  toggle, quote, callout, divider, code,
  image, file, video, embed, bookmark, equation

Media (image/file/video) and URL (embed/bookmark) blocks read their URL from
the positional text argument by default, or from --url when supplied. Media
blocks can reference an uploaded file via --file-upload-id instead.

Layout / structural types (table, table_row, synced_block, column_list,
column) require children or references and cannot be created here yet.

Use --rich-text-json FILE to supply a Notion rich-text array (annotations,
mentions, and inline equations preserved). Mutually exclusive with the
positional text argument.

Examples:
  notioncli blocks add "Hello world"           # Add paragraph
  notioncli blocks add "Section Title" -t heading_1
  notioncli blocks add "" -t divider           # Add divider (no text needed)
  notioncli blocks add "Buy milk" -t to_do
  notioncli blocks add "https://example.com/pic.png" -t image
  notioncli blocks add "" -t image --file-upload-id abc-123
  notioncli blocks add "caption" -t bookmark --url https://example.com
  notioncli blocks add "E=mc^2" -t equation
  notioncli blocks add --rich-text-json spec.json -t paragraph`,
	// Args enforcement is bespoke: either --rich-text-json FILE or a single
	// positional text arg must be supplied, never both. cobra's built-in
	// Args helpers don't model the "XOR" so we express it inline below.
	Args: func(cmd *cobra.Command, args []string) error {
		if blocksAddRichTextJSON != "" {
			if len(args) > 0 {
				return fmt.Errorf("--rich-text-json is mutually exclusive with a positional text argument")
			}
			return nil
		}
		return cobra.MinimumNArgs(1)(cmd, args)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// Suppress cobra's default "Error: ..." + usage block on non-nil
		// return so the colored color.Red lines below are the only error
		// the user sees. Without this, cobra would double-print every
		// error (once via color.Red, once via cobra's own handler) and
		// dump the help block under each failure.
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true

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
			return err
		}

		if !utils.IsAddableBlockType(blockType) {
			err := fmt.Errorf("block type %q cannot be created via `blocks add` (needs children or a reference); use a JSON-payload command in a future release", blockType)
			if globalJSON {
				return jsonErrorOr(cmd, err)
			}
			color.Red("Error: %v", err)
			return err
		}

		notionAPIKey, _ := utils.SetAPIConfig()
		pageID, err := resolvePageID()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("blocks add: %w", err))
		}

		// Rich-text JSON path: read the file, parse, and dispatch
		// through the multi-segment write API. Mutually exclusive with
		// the positional text arg (enforced by Args func above).
		if blocksAddRichTextJSON != "" {
			raw, err := os.ReadFile(blocksAddRichTextJSON)
			if err != nil {
				wrapped := fmt.Errorf("blocks add: read --rich-text-json %q: %w", blocksAddRichTextJSON, err)
				if globalJSON {
					return jsonErrorOr(cmd, wrapped)
				}
				color.Red("Error: %v", wrapped)
				return wrapped
			}
			rt, err := utils.ParseRichTextJSON(raw)
			if err != nil {
				wrapped := fmt.Errorf("blocks add: %w", err)
				if globalJSON {
					return jsonErrorOr(cmd, wrapped)
				}
				color.Red("Error: %v", wrapped)
				return wrapped
			}
			if err := utils.AddRichTextBlock(notionAPIKey, pageID, blockType, rt, blocksAddOptions()...); err != nil {
				wrapped := fmt.Errorf("blocks add: %w", err)
				if globalJSON {
					return jsonErrorOr(cmd, wrapped)
				}
				color.Red("Error adding block: %v", err)
				return wrapped
			}
			if globalJSON {
				return emitOK(cmd.OutOrStdout(), map[string]interface{}{
					"action":   "add",
					"type":     blockType,
					"segments": len(rt),
				})
			}
			icon := utils.SupportedBlockTypes[blockType].Icon
			color.Green("Added %s %s: %d rich-text segment(s)", icon, blockType, len(rt))
			return nil
		}

		text := args[0]
		opts := blocksAddOptions()

		if err := utils.AddBlock(notionAPIKey, pageID, blockType, text, opts...); err != nil {
			wrapped := fmt.Errorf("blocks add: %w", err)
			if globalJSON {
				return jsonErrorOr(cmd, wrapped)
			}
			color.Red("Error adding block: %v", err)
			return wrapped
		}

		if globalJSON {
			payload := map[string]interface{}{
				"action": "add",
				"type":   blockType,
				"text":   text,
			}
			if blockURL != "" {
				payload["url"] = blockURL
			}
			if blockCaption != "" {
				payload["caption"] = blockCaption
			}
			if blockFileID != "" {
				payload["file_upload_id"] = blockFileID
			}
			return emitOK(cmd.OutOrStdout(), payload)
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

// blocksAddOptions translates the --url / --caption / --file-upload-id /
// --language cmd-level flags into the utils.BlockOption slice consumed by
// utils.AddBlock. Kept separate so the flag wiring is easy to extend and
// trivial to unit-test in isolation.
func blocksAddOptions() []utils.BlockOption {
	var opts []utils.BlockOption
	if blockURL != "" {
		opts = append(opts, utils.WithURL(blockURL))
	}
	if blockCaption != "" {
		opts = append(opts, utils.WithCaption(blockCaption))
	}
	if blockFileID != "" {
		opts = append(opts, utils.WithFileUploadID(blockFileID))
	}
	if blockLanguage != "" {
		opts = append(opts, utils.WithLanguage(blockLanguage))
	}
	return opts
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
		// Same silence-pattern as blocksListCmd / blocksAddCmd —
		// closes the exit-code half of #74.
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true

		order, err := strconv.Atoi(args[0])
		if err != nil {
			wrapped := fmt.Errorf("blocks delete: %q is not a valid number: %w", args[0], err)
			if globalJSON {
				return jsonErrorOr(cmd, wrapped)
			}
			color.Red("Error: '%s' is not a valid number", args[0])
			return wrapped
		}

		notionAPIKey, _ := utils.SetAPIConfig()
		pageID, err := resolvePageID()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("blocks delete: %w", err))
		}

		if err := utils.DeleteBlock(notionAPIKey, pageID, order); err != nil {
			wrapped := fmt.Errorf("blocks delete: %w", err)
			if globalJSON {
				return jsonErrorOr(cmd, wrapped)
			}
			color.Red("Error deleting block: %v", err)
			return wrapped
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
	blocksListCmd.Flags().StringVarP(&blocksListType, "type", "t", "", "Filter by block type")

	// Flags for add command
	blocksAddCmd.Flags().StringVarP(&blockType, "type", "t", "paragraph", "Block type to add")
	blocksAddCmd.Flags().StringVar(&blockURL, "url", "", "URL for image/file/video/embed/bookmark blocks (overrides positional text)")
	blocksAddCmd.Flags().StringVar(&blockCaption, "caption", "", "Optional caption for image/file/video/embed/bookmark blocks")
	blocksAddCmd.Flags().StringVar(&blockFileID, "file-upload-id", "", "Notion file_upload id for image/file/video blocks (overrides --url and positional text when set; see files upload)")
	blocksAddCmd.Flags().StringVar(&blockLanguage, "language", "plain text", "Language for code blocks")
	blocksAddCmd.Flags().StringVar(&blocksAddRichTextJSON, "rich-text-json", "",
		"Path to a JSON file containing a Notion rich-text array (preserves annotations, mentions, equations)")
}
