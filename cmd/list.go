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
		notionAPIKey, pageID := utils.SetAPIConfig()
		localTimezone, err := utils.GetLocalTimeZone()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("list: resolve local time zone: %w", err))
		}
		// In --json mode we want the raw Notion block objects so consumers
		// can jq on them; the formatted lines are for humans only. This
		// mirrors the blocks list split (typed objects pass-through).
		if globalJSON {
			// GetAllBlocks with "to_do" mirrors the human list by only
			// returning to-do blocks. NDJSON: one raw Notion block object
			// per line.
			blocks, err := utils.GetAllBlocks(notionAPIKey, pageID, "to_do")
			if err != nil {
				return jsonErrorOr(cmd, fmt.Errorf("list: fetch blocks: %w", err))
			}
			out := cmd.OutOrStdout()
			for _, b := range blocks {
				if err := emitJSON(out, b); err != nil {
					return jsonErrorOr(cmd, err)
				}
			}
			return nil
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
