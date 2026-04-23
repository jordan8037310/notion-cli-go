// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"fmt"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// teamsCmd is the parent command for team-related operations. Requires
// Notion-Version 2026-03-11 or newer (the /v1/teams endpoint was
// introduced in that release).
var teamsCmd = &cobra.Command{
	Use:   "teams",
	Short: "Manage Notion workspace teams",
	Long: `The teams command lists workspace teams visible to the integration.
Requires Notion-Version 2026-03-11 or newer.`,
}

// teamsListCmd lists every team visible to the integration, following
// pagination under the hood.
var teamsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every team in the workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewTeamClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		teams, err := client.List(context.Background())
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("teams list: %w", err))
		}

		if globalJSON {
			// NDJSON: one team object per line. Stable typed shape.
			out := cmd.OutOrStdout()
			for _, team := range teams {
				if err := emitJSON(out, team); err != nil {
					return jsonErrorOr(cmd, err)
				}
			}
			return nil
		}

		if len(teams) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No teams found.")
			return nil
		}
		for _, team := range teams {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", team.ID, team.Name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(teamsCmd)
	teamsCmd.AddCommand(teamsListCmd)
}
