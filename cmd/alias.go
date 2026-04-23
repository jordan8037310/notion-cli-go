// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// aliasStore returns the AliasStore the pages alias subcommands should use.
// The test seam aliasStoreOverride takes precedence so a test can route the
// store at a t.TempDir() file without mutating HOME. Production callers
// always land on utils.DefaultAliasStore().
func aliasStore() (*utils.AliasStore, error) {
	if aliasStoreOverride != nil {
		return aliasStoreOverride, nil
	}
	return utils.DefaultAliasStore()
}

// pagesAddAliasCmd writes a new `name -> id` mapping to the alias store.
// The id is validated against the Notion uuid shape so typos ("11111"
// instead of the full 32-hex id) fail early with a clear error instead
// of surfacing as a Notion 400 on first use.
var pagesAddAliasCmd = &cobra.Command{
	Use:   "add-alias <name> <id>",
	Short: "Add or update a named page alias in ~/.config/notioncli/pages.yaml",
	Long: `Add or update a named page alias so --page <name> can target
the given Notion page id. The id must match the 32-hex Notion uuid
shape (dashes optional). The alias file lives at
~/.config/notioncli/pages.yaml and is created on first write.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, id := args[0], args[1]
		if !utils.IsNotionID(id) {
			return jsonErrorOr(cmd, fmt.Errorf("add-alias: %q is not a valid Notion page id (want 32 hex chars, dashes optional)", id))
		}
		store, err := aliasStore()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("add-alias: %w", err))
		}
		if err := store.Set(name, id); err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("add-alias: %w", err))
		}
		if globalJSON {
			return emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "add-alias",
				"name":   name,
				"id":     id,
			})
		}
		color.Green("alias %s -> %s", name, id)
		return nil
	},
}

// pagesListAliasesCmd prints every stored alias. Human output is a
// two-column aligned table, JSON output is one NDJSON record per alias
// so pipes stay well-formed. Pretty-printing returns a single JSON
// array, matching the convention set by the other list commands.
var pagesListAliasesCmd = &cobra.Command{
	Use:   "list-aliases",
	Short: "List all named page aliases",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := aliasStore()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("list-aliases: %w", err))
		}
		aliases, err := store.All()
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("list-aliases: %w", err))
		}

		// Sort so the output is stable across runs. Both paths (JSON and
		// table) consume the same sorted slice so tests and humans see
		// identical ordering.
		type entry struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		names := make([]string, 0, len(aliases))
		for n := range aliases {
			names = append(names, n)
		}
		sort.Strings(names)
		entries := make([]entry, 0, len(names))
		for _, n := range names {
			entries = append(entries, entry{Name: n, ID: aliases[n]})
		}

		if globalJSON {
			return jsonErrorOr(cmd, emitList(cmd.OutOrStdout(), entries))
		}

		if len(entries) == 0 {
			yellow := color.New(color.FgYellow).SprintFunc()
			fmt.Fprintln(cmd.OutOrStdout(), yellow("No aliases configured. Add one with `notioncli pages add-alias <name> <id>`."))
			return nil
		}
		// tabwriter keeps the NAME and ID columns aligned regardless of
		// alias length. Minwidth/tabwidth/padding are the standard
		// defaults used elsewhere in the Go ecosystem for CLI tables.
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tID")
		for _, e := range entries {
			fmt.Fprintf(tw, "%s\t%s\n", e.Name, e.ID)
		}
		return tw.Flush()
	},
}

func init() {
	pagesCmd.AddCommand(pagesAddAliasCmd)
	pagesCmd.AddCommand(pagesListAliasesCmd)
}
