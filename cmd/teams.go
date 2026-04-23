// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// teamsCmd is the parent command for team-related operations. The teams
// endpoint is not available on the pinned Notion-Version (2022-06-28);
// see issue #11 for the version bump that will enable a real listing.
var teamsCmd = &cobra.Command{
	Use:   "teams",
	Short: "Manage Notion workspace teams (pending API version bump)",
	Long: `The teams command will work once the Notion-Version pinned by
this CLI is bumped (tracked in issue #11). Today every subcommand returns
a clear "not supported" error so callers can branch on it.`,
}

// teamsListCmd surfaces utils.ErrTeamsNotSupported to the user.
var teamsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every team in the workspace",
	Run: func(cmd *cobra.Command, args []string) {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewTeamClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		// client.List currently always returns (nil, ErrTeamsNotSupported)
		// on this branch; the discard of the first return is intentional
		// and becomes load-bearing once issue #11 lands the real impl.
		if _, err := client.List(context.Background()); err != nil {
			color.Red("Error: %v", err)
			osExit(1)
			return
		}
	},
}

func init() {
	rootCmd.AddCommand(teamsCmd)
	teamsCmd.AddCommand(teamsListCmd)
}
