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
	"strings"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// Flag vars for the views subcommands. Package-level mirrors the pattern
// used by pages.go/blocks.go so cobra can bind them in init().
var (
	viewsCreateName       string
	viewsCreateType       string
	viewsCreateConfigFile string
	viewsUpdateName       string
	viewsUpdateConfigFile string
)

// viewsCmd is the parent command for view-related operations. Requires
// Notion-Version 2026-03-11 or newer (data-source views endpoint).
var viewsCmd = &cobra.Command{
	Use:   "views",
	Short: "Manage Notion database views",
	Long: `Create and update Notion database views.

Examples:
  notioncli views create <database-id> --name "Backlog" --type board
  notioncli views create <database-id> --name "Q2" --type timeline --config-json q2.json
  notioncli views update <view-id> --name "Renamed"
  notioncli views update <view-id> --config-json new-config.json`,
}

// newViewClient builds a ViewClient using the CLI's standard config
// loading so every subcommand shares identical client construction.
// Returns utils.ErrMissingAPIKey (wrapped) when NOTION_API_KEY resolves
// empty so callers see a configuration error instead of a downstream 401.
func newViewClient() (*utils.ViewClient, error) {
	apiKey, _ := utils.SetAPIConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("views client: %w", utils.ErrMissingAPIKey)
	}
	c := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))
	return utils.NewViewClient(c), nil
}

// readConfigJSON reads a JSON config file from disk and returns its
// bytes as a json.RawMessage so the view-configuration payload passes
// through unchanged (preserving key order and avoiding float64 coercion
// of numeric IDs). An empty path returns (nil, nil) so callers can use
// the helper unconditionally without branching on the optional flag.
// The bytes are validated as JSON syntax so a malformed file surfaces a
// parse error at the CLI layer rather than after the wire call.
func readConfigJSON(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config-json: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read config-json: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("config-json %q is empty", path)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("parse config-json: invalid JSON in %q", path)
	}
	return json.RawMessage(raw), nil
}

// viewsCreateCmd creates a new view on the given database. The command
// is wired with RunE so the returned error drives cobra's exit code —
// the shell sees a non-zero status without any call to os.Exit from
// this file. Errors originate from ViewClient.Create (validation,
// config-file parsing, or the underlying POST /v1/data_sources/{id}/views
// call) and are wrapped before returning.
var viewsCreateCmd = &cobra.Command{
	Use:   "create <database-id>",
	Short: "Create a new view on a database",
	Long: `Create a new view on the given database (data source).

The --type flag must be one of: table, board, list, gallery, calendar,
timeline. The optional --config-json flag points at a JSON file whose
contents are forwarded verbatim as the view's configuration payload.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if viewsCreateName == "" {
			return fmt.Errorf("create view: --name is required")
		}
		if viewsCreateType == "" {
			return fmt.Errorf("create view: --type is required")
		}
		config, err := readConfigJSON(viewsCreateConfigFile)
		if err != nil {
			return err
		}
		// Validate the request before paying the env/config cost of
		// building the client so bad input (e.g. --type bogus) surfaces
		// as an input error rather than behind an auth/config failure.
		req := utils.CreateViewRequest{
			DatabaseID: args[0],
			Name:       viewsCreateName,
			Type:       viewsCreateType,
			Config:     config,
		}
		if err := req.Validate(); err != nil {
			return err
		}
		vc, err := newViewClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		view, err := vc.Create(context.Background(), req)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("create view: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), view))
		}
		printView(cmd.OutOrStdout(), view)
		return nil
	},
}

// viewsUpdateCmd patches an existing view by ID.
var viewsUpdateCmd = &cobra.Command{
	Use:   "update <view-id>",
	Short: "Update an existing view",
	Long: `Update an existing view's name and/or configuration.

At least one of --name or --config-json must be provided. The
--config-json flag points at a JSON file whose contents are forwarded
verbatim as the view's configuration payload.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if viewsUpdateName == "" && viewsUpdateConfigFile == "" {
			return fmt.Errorf("update view: at least one of --name or --config-json is required")
		}
		config, err := readConfigJSON(viewsUpdateConfigFile)
		if err != nil {
			return err
		}
		// Validate the request (and the id) before building the client
		// so bad input surfaces as an input error rather than behind an
		// auth/config failure.
		if args[0] == "" {
			return fmt.Errorf("update view: id is required")
		}
		req := utils.UpdateViewRequest{
			Name:   viewsUpdateName,
			Config: config,
		}
		if err := req.Validate(); err != nil {
			return err
		}
		vc, err := newViewClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		view, err := vc.Update(context.Background(), args[0], req)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("update view: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), view))
		}
		printView(cmd.OutOrStdout(), view)
		return nil
	},
}

// printView writes a human-readable JSON blob for the given View. This
// is the v1 output format; a proper --json vs table split will land
// with the wider --json rollout on the roadmap. Nil views are a no-op
// so the caller can unconditionally invoke this after a successful API
// call without a pre-check.
func printView(w io.Writer, view *utils.View) {
	if view == nil {
		return
	}
	if w == nil {
		w = os.Stdout
	}
	b, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "Error formatting view: %v\n", err)
		return
	}
	fmt.Fprintln(w, string(b))
}

func init() {
	rootCmd.AddCommand(viewsCmd)
	viewsCmd.AddCommand(viewsCreateCmd)
	viewsCmd.AddCommand(viewsUpdateCmd)

	viewsCreateCmd.Flags().StringVar(&viewsCreateName, "name", "", "Display name for the new view (required)")
	// The suffix is generated from utils.ValidViewTypes so the help
	// text can't drift from the validator's accepted set.
	viewsCreateCmd.Flags().StringVar(&viewsCreateType, "type", "", "View type: "+strings.Join(utils.ValidViewTypes, "|")+" (required)")
	viewsCreateCmd.Flags().StringVar(&viewsCreateConfigFile, "config-json", "", "Path to a JSON file with extra view configuration")

	viewsUpdateCmd.Flags().StringVar(&viewsUpdateName, "name", "", "New display name for the view")
	viewsUpdateCmd.Flags().StringVar(&viewsUpdateConfigFile, "config-json", "", "Path to a JSON file with updated view configuration")
}
