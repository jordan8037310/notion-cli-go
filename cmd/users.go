// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"fmt"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// usersCmd is the parent command for user-related operations on the
// Notion API.
//
// JSON output is driven by the persistent --json flag on rootCmd
// (globalJSON); the users subcommands used to own local --json flags
// but now share the global one so all commands behave consistently.
var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage Notion workspace users",
	Long: `The users command works with the Notion users API.

Subcommands:
  list           List every user the integration can see
  get <id>       Retrieve a single user by id
  whoami         Show the bot user associated with NOTION_API_KEY`,
}

// usersListCmd lists every user the integration can see, following
// pagination internally.
var usersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every user in the workspace",
	Long:  `List every user the current integration can access. Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewUserClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		users, err := client.List(context.Background())
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("users list: %w", err))
		}

		if globalJSON {
			// emitList picks NDJSON or a pretty JSON array based on --pretty.
			return jsonErrorOr(cmd, emitList(cmd.OutOrStdout(), users))
		}

		if len(users) == 0 {
			color.Yellow("No users returned.")
			return nil
		}
		for _, u := range users {
			fmt.Fprintln(cmd.OutOrStdout(), formatUser(u))
		}
		return nil
	},
}

// usersGetCmd retrieves a single user by id.
var usersGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Retrieve a user by id",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewUserClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		user, err := client.Get(context.Background(), args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("users get: %w", err))
		}

		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), user))
		}
		fmt.Fprintln(cmd.OutOrStdout(), formatUser(*user))
		return nil
	},
}

// usersWhoamiCmd prints the bot user associated with the current token.
var usersWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the current integration's bot user",
	RunE: func(cmd *cobra.Command, args []string) error {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewUserClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		user, err := client.Me(context.Background())
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("users whoami: %w", err))
		}

		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), user))
		}
		fmt.Fprintln(cmd.OutOrStdout(), formatUser(*user))
		return nil
	},
}

// formatUser renders a single user as a one-line human-readable summary.
// Kept minimal so CI snapshots (none today) would be stable.
func formatUser(u utils.User) string {
	name := u.Name
	if name == "" {
		name = "(unnamed)"
	}
	kind := u.Type
	if kind == "" {
		kind = "user"
	}
	detail := ""
	switch {
	case u.Person != nil && u.Person.Email != "":
		detail = " <" + u.Person.Email + ">"
	case u.Bot != nil && u.Bot.WorkspaceName != "":
		detail = " (workspace: " + u.Bot.WorkspaceName + ")"
	}
	return fmt.Sprintf("[%s] %s%s  id=%s", kind, name, detail, u.ID)
}

func init() {
	rootCmd.AddCommand(usersCmd)
	usersCmd.AddCommand(usersListCmd)
	usersCmd.AddCommand(usersGetCmd)
	usersCmd.AddCommand(usersWhoamiCmd)
}
