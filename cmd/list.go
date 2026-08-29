// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tasks",
	Long:  `List all tasks in the Notion page`,
	RunE: func(cmd *cobra.Command, args []string) error {
		notionAPIKey, _ := utils.SetAPIConfig()
		pageID, err := resolvePageID()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("list: %w", err))
		}

		// JSON path emits raw Notion block objects, so timestamps are
		// never humanised — resolving LOCAL_TIMEZONE on this branch
		// would just fail loudly on machines that ship without one set.
		// The human path below is the only consumer of localTimezone,
		// so the lookup moves to right before GetToDoBlocks.
		if globalJSON {
			// Use the same "visible to-do" view the human list uses:
			// to_do-typed AND non-empty rich_text. GetAllBlocks(...,
			// "to_do") would surface blank checkboxes that `list` text
			// mode hides, breaking the contract that --json mirrors the
			// human numbering. Closes the regression Codex caught on
			// PR #75 (fallout from PR #56).
			blocks, err := utils.GetVisibleToDoBlocks(notionAPIKey, pageID)
			if err != nil {
				return jsonErrorOr(cmd, fmt.Errorf("list: fetch blocks: %w", err))
			}
			return jsonErrorOr(cmd, emitList(cmd.OutOrStdout(), blocks))
		}

		localTimezone, err := utils.GetLocalTimeZone()
		if err != nil {
			return fmt.Errorf("list: resolve local time zone: %w", err)
		}
		brightWhite := color.New(color.FgHiWhite).SprintFunc()
		blocks, err := utils.GetToDoBlocks(notionAPIKey, pageID, localTimezone)
		if err != nil {
			return fmt.Errorf("list: fetch to-do blocks: %w", err)
		}
		for _, block := range blocks {
			fmt.Fprintln(cmd.OutOrStdout(), brightWhite(block))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
