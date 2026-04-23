// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// usersJSON toggles JSON output on the users subcommands. It is a
// package-level var so each subcommand can bind its own --json flag.
var usersJSON bool

// usersCmd is the parent command for user-related operations on the
// Notion API.
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
	Run: func(cmd *cobra.Command, args []string) {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewUserClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		users, err := client.List(context.Background())
		if err != nil {
			color.Red("Error listing users: %v", err)
			osExit(1)
			return
		}

		if usersJSON {
			if err := writeJSON(cmd.OutOrStdout(), users); err != nil {
				color.Red("Error encoding JSON: %v", err)
				osExit(1)
				return
			}
			return
		}

		if len(users) == 0 {
			color.Yellow("No users returned.")
			return
		}
		for _, u := range users {
			fmt.Fprintln(cmd.OutOrStdout(), formatUser(u))
		}
	},
}

// usersGetCmd retrieves a single user by id.
var usersGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Retrieve a user by id",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewUserClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		user, err := client.Get(context.Background(), args[0])
		if err != nil {
			color.Red("Error getting user: %v", err)
			osExit(1)
			return
		}

		if usersJSON {
			if err := writeJSON(cmd.OutOrStdout(), user); err != nil {
				color.Red("Error encoding JSON: %v", err)
				osExit(1)
				return
			}
			return
		}
		fmt.Fprintln(cmd.OutOrStdout(), formatUser(*user))
	},
}

// usersWhoamiCmd prints the bot user associated with the current token.
var usersWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the current integration's bot user",
	Run: func(cmd *cobra.Command, args []string) {
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewUserClient(utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL())))

		user, err := client.Me(context.Background())
		if err != nil {
			color.Red("Error retrieving self: %v", err)
			osExit(1)
			return
		}

		if usersJSON {
			if err := writeJSON(cmd.OutOrStdout(), user); err != nil {
				color.Red("Error encoding JSON: %v", err)
				osExit(1)
				return
			}
			return
		}
		fmt.Fprintln(cmd.OutOrStdout(), formatUser(*user))
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

// writeJSON encodes v as indented JSON to w. Broken out so the users
// subcommands share a single encoder configuration.
func writeJSON(w interface{ Write(p []byte) (n int, err error) }, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Ensure os is referenced even if osExit is unused in this file's test
// configuration; keeps godoc-importable tools happy without an import
// side-effect.
var _ = os.Stdout

func init() {
	rootCmd.AddCommand(usersCmd)
	usersCmd.AddCommand(usersListCmd)
	usersCmd.AddCommand(usersGetCmd)
	usersCmd.AddCommand(usersWhoamiCmd)

	usersListCmd.Flags().BoolVar(&usersJSON, "json", false, "Emit JSON instead of human-readable output")
	usersGetCmd.Flags().BoolVar(&usersJSON, "json", false, "Emit JSON instead of human-readable output")
	usersWhoamiCmd.Flags().BoolVar(&usersJSON, "json", false, "Emit JSON instead of human-readable output")
}
