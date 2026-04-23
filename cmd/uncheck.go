// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"notioncli/utils"
	"strconv"

	"github.com/spf13/cobra"
)

var uncheckCmd = &cobra.Command{
	Use:   "uncheck <item order>",
	Short: "Mark a task as incomplete",
	Long:  `Mark a ToDo task as incomplete, e.g., check 1 (marks the first ToDo in the list incomplete)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		order, err := strconv.Atoi(args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("uncheck: parse order %q: %w", args[0], err))
		}
		notionAPIKey, pageID := utils.SetAPIConfig()
		if err := utils.MarkToDoBlockUnChecked(notionAPIKey, pageID, order); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("uncheck: mark task %d incomplete: %w", order, err))
		}
		if globalJSON {
			return emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "uncheck",
				"order":  order,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Task %d marked incomplete.\n", order)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uncheckCmd)
	uncheckCmd.Flags().Int("order", 0, "numeric order of the task to mark as complete")
}
