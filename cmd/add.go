package cmd

import (
	"fmt"
	"notioncli/utils"

	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add <task>",
	Short: "Add a new task",
	Long:  `Add a new task to the Notion ToDo task list page`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := args[0]
		notionAPIKey, pageID := utils.SetAPIConfig()
		if err := utils.AddNewToDoItem(notionAPIKey, pageID, text); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("add: %w", err))
		}
		if globalJSON {
			return emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "add",
				"text":   text,
				"type":   "to_do",
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Task %s added.\n", text)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(addCmd)
	checkCmd.Flags().String("text", "", "Text for the new task")
}
