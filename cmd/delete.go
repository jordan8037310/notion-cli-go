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

var deleteCmd = &cobra.Command{
	Use:   "delete <item order>",
	Short: "Remove a task from the task list",
	Long:  `Completely delete a task, e.g., delete 2 (removes the second task)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		order, err := strconv.Atoi(args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("delete: parse order %q: %w", args[0], err))
		}
		notionAPIKey, pageID := utils.SetAPIConfig()
		if err := utils.DeleteToDoBlock(notionAPIKey, pageID, order); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("delete: remove task %d: %w", order, err))
		}
		if globalJSON {
			return emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "delete",
				"order":  order,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Task %d removed.\n", order)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().Int("order", 0, "numeric order of the task to delete")
}
