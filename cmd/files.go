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

// Flag vars for the file-upload subcommands. Package-level to match the
// existing blocks.go / pages.go pattern and let cobra bind them via init().
var (
	blocksAddFileName string
)

// newFileClient constructs a *utils.FileClient using the CLI's shared config
// loader. Mirrors cmd/pages.go's newPageClient — one helper per resource so
// missing-key handling is identical across subcommands. Surfaces
// utils.ErrMissingAPIKey when NOTION_API_KEY resolves empty so operators get
// a clear configuration error instead of a downstream 401.
func newFileClient() (*utils.FileClient, error) {
	apiKey, _ := utils.SetAPIConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("files client: %w", utils.ErrMissingAPIKey)
	}
	c := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))
	return utils.NewFileClient(c), nil
}

// blocksAddImageCmd is wired under `blocks` (not a new top-level group)
// because the user-facing intent is appending a block — the upload is an
// implementation detail. Today it returns the stub error; once #11 lands
// the `utils.FileClient.Upload` switches to the real two-step flow and
// this subcommand begins appending an image block referencing the
// resulting file_upload_id.
var blocksAddImageCmd = &cobra.Command{
	Use:   "add-image <path>",
	Short: "Upload an image and append it as a block (pending API version bump)",
	Long: `Upload a local image file and append it as an image block on the
Notion page configured via NOTION_PAGE_ID.

The Notion file-upload endpoints require a newer Notion-Version than this
CLI currently pins; see issue #11 for the tracking bump. Today the command
validates the path client-side and returns a clear error referencing #11.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fc, err := newFileClient()
		if err != nil {
			return err
		}
		ref, err := fc.Upload(context.Background(), args[0])
		if err != nil {
			return fmt.Errorf("add-image: %w", err)
		}
		// Unreachable on the pinned version — kept so the happy-path
		// output shape ships with the stub and #11's flip does not
		// touch the cmd layer.
		color.Green("Uploaded image %s (id=%s)", ref.Name, ref.ID)
		return nil
	},
}

// blocksAddFileCmd mirrors blocksAddImageCmd for non-image files. The
// optional --name flag overrides the displayed filename in Notion;
// otherwise the base name of the path is used.
var blocksAddFileCmd = &cobra.Command{
	Use:   "add-file <path>",
	Short: "Upload a file and append it as a block (pending API version bump)",
	Long: `Upload a local file and append it as a file block on the Notion
page configured via NOTION_PAGE_ID.

The Notion file-upload endpoints require a newer Notion-Version than this
CLI currently pins; see issue #11 for the tracking bump. Today the command
validates the path client-side and returns a clear error referencing #11.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fc, err := newFileClient()
		if err != nil {
			return err
		}
		ref, err := fc.Upload(context.Background(), args[0])
		if err != nil {
			return fmt.Errorf("add-file: %w", err)
		}
		name := blocksAddFileName
		if name == "" {
			name = ref.Name
		}
		color.Green("Uploaded file %s (id=%s)", name, ref.ID)
		return nil
	},
}

// pagesSetIconCmd uploads an image and PATCHes it as the page icon. Stub-
// only today; the PATCH body ({"icon": {"type": "file_upload", ...}}) is
// deferred to issue #11 since the reference format depends on the same
// version bump as the upload endpoint.
var pagesSetIconCmd = &cobra.Command{
	Use:   "set-icon <page-id> <path>",
	Short: "Upload an image and set it as a page icon (pending API version bump)",
	Long: `Upload a local image and set it as the icon for a Notion page.

The Notion file-upload endpoints require a newer Notion-Version than this
CLI currently pins; see issue #11 for the tracking bump. Today the command
validates the path client-side and returns a clear error referencing #11.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fc, err := newFileClient()
		if err != nil {
			return err
		}
		ref, err := fc.Upload(context.Background(), args[1])
		if err != nil {
			return fmt.Errorf("set-icon: %w", err)
		}
		color.Green("Set icon on page %s (file id=%s)", args[0], ref.ID)
		return nil
	},
}

// pagesSetCoverCmd uploads an image and PATCHes it as the page cover. Same
// stub posture as set-icon.
var pagesSetCoverCmd = &cobra.Command{
	Use:   "set-cover <page-id> <path>",
	Short: "Upload an image and set it as a page cover (pending API version bump)",
	Long: `Upload a local image and set it as the cover for a Notion page.

The Notion file-upload endpoints require a newer Notion-Version than this
CLI currently pins; see issue #11 for the tracking bump. Today the command
validates the path client-side and returns a clear error referencing #11.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fc, err := newFileClient()
		if err != nil {
			return err
		}
		ref, err := fc.Upload(context.Background(), args[1])
		if err != nil {
			return fmt.Errorf("set-cover: %w", err)
		}
		color.Green("Set cover on page %s (file id=%s)", args[0], ref.ID)
		return nil
	},
}

func init() {
	// Register under the existing `blocks` and `pages` parents so the UX
	// matches the issue spec (notioncli blocks add-image, etc.). Those
	// parent commands are declared in blocks.go / pages.go respectively;
	// we only attach the new children here so the scope of this change
	// stays localized to a single new file.
	blocksCmd.AddCommand(blocksAddImageCmd)
	blocksCmd.AddCommand(blocksAddFileCmd)
	pagesCmd.AddCommand(pagesSetIconCmd)
	pagesCmd.AddCommand(pagesSetCoverCmd)

	blocksAddFileCmd.Flags().StringVar(&blocksAddFileName, "name", "", "Override the filename displayed in Notion")
}
