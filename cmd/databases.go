// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Flag vars for the databases subcommands. Keeping them package-level mirrors
// pages.go's pattern and lets cobra bind them via init().
var (
	dbQueryFilterJSON string
	dbQuerySortJSON   string
	dbQueryLimit      int
	dbCreateParent    string
	dbCreateTitle     string
	dbCreatePropsFile string
	dbUpdatePropsFile string
	dbUpdateTitle     string
)

// databasesCmd is the parent of every `notioncli databases …` subcommand.
var databasesCmd = &cobra.Command{
	Use:   "databases",
	Short: "Manage Notion databases (get, query, create, update)",
	Long: `Work with Notion databases: retrieve schema, run queries with
filters and sorts, create new databases under a page parent, and update
the schema of an existing database.

Examples:
  notioncli databases get <db-id>
  notioncli databases query <db-id> --filter-json ./filter.json --sort-json ./sort.json --limit 50
  notioncli databases create --parent <page-id> --title "My DB" --properties-json ./schema.json
  notioncli databases update <db-id> --properties-json ./schema.json`,
}

// newDatabaseClient builds a DatabaseClient using the CLI's standard config
// loading so every subcommand shares identical client construction. Returns
// utils.ErrMissingAPIKey (wrapped) when NOTION_API_KEY resolves empty so
// callers get a clear configuration error instead of a downstream 401.
// Mirrors newPageClient in pages.go.
func newDatabaseClient() (*utils.DatabaseClient, error) {
	apiKey, _ := utils.SetAPIConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("databases client: %w", utils.ErrMissingAPIKey)
	}
	c := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))
	return utils.NewDatabaseClient(c), nil
}

// readJSONFile reads path and returns its contents as a json.RawMessage after
// verifying the payload parses as valid JSON. Malformed files surface a clear
// error instead of being silently passed through to Notion, which would
// respond with an opaque 400.
func readJSONFile(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var probe interface{}
	if err := json.Unmarshal(buf, &probe); err != nil {
		return nil, fmt.Errorf("parse %s as JSON: %w", path, err)
	}
	return json.RawMessage(buf), nil
}

// readPropertiesFile reads a --properties-json file and decodes it into the
// map[string]DatabaseProperty shape CreateDatabaseRequest/UpdateDatabaseRequest
// expect. Malformed files surface a clear error.
func readPropertiesFile(path string) (map[string]utils.DatabaseProperty, error) {
	if path == "" {
		return nil, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var out map[string]utils.DatabaseProperty
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, fmt.Errorf("parse %s as properties JSON: %w", path, err)
	}
	return out, nil
}

// databasesGetCmd retrieves a database by ID.
var databasesGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Retrieve a database by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dc, err := newDatabaseClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		db, err := dc.Get(context.Background(), args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("get database: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), db))
		}
		printDatabase(cmd.OutOrStdout(), db)
		return nil
	},
}

// databasesQueryCmd queries a database, paginating through all pages unless
// --limit is set. --filter-json and --sort-json accept raw Notion
// filter/sort JSON; see https://developers.notion.com/reference/post-database-query-filter.
var databasesQueryCmd = &cobra.Command{
	Use:   "query <id>",
	Short: "Query a database, paginating results",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filter, err := readJSONFile(dbQueryFilterJSON)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("query database: %w", err))
		}
		sort, err := readJSONFile(dbQuerySortJSON)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("query database: %w", err))
		}
		dc, err := newDatabaseClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		results, err := dc.QueryAll(context.Background(), args[0], filter, sort, dbQueryLimit)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("query database: %w", err))
		}
		if globalJSON {
			// NDJSON: one JSON object per page row. Matches the shape
			// printQueryResults already emits when --json was the default.
			out := cmd.OutOrStdout()
			for _, p := range results {
				if err := emitJSON(out, p); err != nil {
					return jsonErrorOr(cmd, err)
				}
			}
			return nil
		}
		printQueryResults(cmd.OutOrStdout(), results)
		return nil
	},
}

// databasesCreateCmd creates a new database under --parent.
var databasesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new database under --parent",
	RunE: func(cmd *cobra.Command, args []string) error {
		if dbCreateParent == "" {
			return jsonErrorOr(cmd, fmt.Errorf("create database: --parent is required"))
		}
		props, err := readPropertiesFile(dbCreatePropsFile)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create database: %w", err))
		}
		dc, err := newDatabaseClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		db, err := dc.Create(context.Background(), utils.CreateDatabaseRequest{
			Parent:     utils.PageParent{PageID: dbCreateParent},
			Title:      dbCreateTitle,
			Properties: props,
		})
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create database: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), db))
		}
		color.Green("Created database %s", db.ID)
		printDatabase(cmd.OutOrStdout(), db)
		return nil
	},
}

// databasesUpdateCmd patches the title and/or schema on an existing database.
// At least one of --title or --properties-json must be provided; the
// underlying PATCH rejects empty bodies.
var databasesUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a database's title and/or schema",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		props, err := readPropertiesFile(dbUpdatePropsFile)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("update database: %w", err))
		}
		if dbUpdateTitle == "" && len(props) == 0 {
			return jsonErrorOr(cmd, fmt.Errorf("update database: at least one of --title or --properties-json is required"))
		}
		dc, err := newDatabaseClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		db, err := dc.Update(context.Background(), args[0], utils.UpdateDatabaseRequest{
			Title:      dbUpdateTitle,
			Properties: props,
		})
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("update database: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), db))
		}
		color.Green("Updated database %s", db.ID)
		printDatabase(cmd.OutOrStdout(), db)
		return nil
	},
}

// printDatabase writes a human-readable JSON blob to the given writer. This
// matches printPage's v1 output shape in pages.go — a proper --json vs. table
// split lands with the wider --json rollout.
func printDatabase(w io.Writer, db *utils.Database) {
	if db == nil {
		return
	}
	if w == nil {
		w = os.Stdout
	}
	b, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		color.Red("Error formatting database: %v", err)
		return
	}
	fmt.Fprintln(w, string(b))
}

// printQueryResults emits one JSON object per row, newline-delimited. This
// keeps the output pipe-friendly for jq and matches the NDJSON shape the
// search command adopted in issue #4.
func printQueryResults(w io.Writer, pages []utils.Page) {
	if w == nil {
		w = os.Stdout
	}
	if len(pages) == 0 {
		// Route the empty-results banner through the supplied writer so
		// NDJSON consumers piping through jq don't see banner noise on
		// stdout. color.YellowString returns the ANSI-coloured string
		// without writing directly; fmt.Fprintln honours the cmd writer.
		fmt.Fprintln(w, color.YellowString("No results."))
		return
	}
	for _, p := range pages {
		b, err := json.Marshal(p)
		if err != nil {
			color.Red("Error formatting page %s: %v", p.ID, err)
			continue
		}
		fmt.Fprintln(w, string(b))
	}
}

// resetDatabasesFlags wipes the package-level flag vars between tests. cobra
// persists bound flag values across executions.
func resetDatabasesFlags() {
	dbQueryFilterJSON = ""
	dbQuerySortJSON = ""
	dbQueryLimit = 0
	dbCreateParent = ""
	dbCreateTitle = ""
	dbCreatePropsFile = ""
	dbUpdatePropsFile = ""
	dbUpdateTitle = ""
}

func init() {
	rootCmd.AddCommand(databasesCmd)
	databasesCmd.AddCommand(databasesGetCmd)
	databasesCmd.AddCommand(databasesQueryCmd)
	databasesCmd.AddCommand(databasesCreateCmd)
	databasesCmd.AddCommand(databasesUpdateCmd)

	databasesQueryCmd.Flags().StringVar(&dbQueryFilterJSON, "filter-json", "", "Path to a JSON file with the Notion filter object")
	databasesQueryCmd.Flags().StringVar(&dbQuerySortJSON, "sort-json", "", "Path to a JSON file with the Notion sorts array")
	databasesQueryCmd.Flags().IntVar(&dbQueryLimit, "limit", 0, "Maximum total results to return (0 = all)")

	databasesCreateCmd.Flags().StringVar(&dbCreateParent, "parent", "", "Parent page ID (required)")
	databasesCreateCmd.Flags().StringVar(&dbCreateTitle, "title", "", "Title for the new database")
	databasesCreateCmd.Flags().StringVar(&dbCreatePropsFile, "properties-json", "", "Path to a JSON file with the database schema")

	databasesUpdateCmd.Flags().StringVar(&dbUpdateTitle, "title", "", "New title for the database")
	databasesUpdateCmd.Flags().StringVar(&dbUpdatePropsFile, "properties-json", "", "Path to a JSON file with the updated schema")
}
