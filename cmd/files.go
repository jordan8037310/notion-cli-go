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

// Flag vars for the file-upload subcommands.
var (
	blocksAddFileName string
)

// newFileClient constructs a *utils.FileClient using the CLI's shared
// config loader. Surfaces utils.ErrMissingAPIKey when NOTION_API_KEY
// resolves empty.
func newFileClient() (*utils.FileClient, error) {
	apiKey, _ := utils.SetAPIConfig()
	if apiKey == "" {
		return nil, fmt.Errorf("files client: %w", utils.ErrMissingAPIKey)
	}
	c := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))
	return utils.NewFileClient(c), nil
}

// blocksAddImageCmd uploads a local image via the Notion file-upload
// endpoint and prints the resulting FileRef. Appending it as an image
// block on the configured page is deferred to a follow-up — the block
// PATCH is not issued by this command today.
var blocksAddImageCmd = &cobra.Command{
	Use:   "add-image <path>",
	Short: "Upload an image (block append is deferred)",
	Long: `Upload a local image file to Notion and print the resulting
FileRef. Appending the upload as an image block on the page configured
via NOTION_PAGE_ID is deferred to a follow-up; this command currently
returns the file id and name only.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fc, err := newFileClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		ref, err := fc.Upload(context.Background(), args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("add-image: %w", err))
		}
		if globalJSON {
			return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), ref))
		}
		color.Green("Uploaded image %s (id=%s) — block append deferred", ref.Name, ref.ID)
		return nil
	},
}

// blocksAddFileCmd mirrors blocksAddImageCmd for non-image files. Like
// add-image, block append is deferred to a follow-up; today this only
// uploads and prints the FileRef. The optional --name flag overrides
// the displayed filename in Notion.
var blocksAddFileCmd = &cobra.Command{
	Use:   "add-file <path>",
	Short: "Upload a file (block append is deferred)",
	Long: `Upload a local file to Notion and print the resulting FileRef.
Appending the upload as a file block on the page configured via
NOTION_PAGE_ID is deferred to a follow-up; this command currently
returns the file id and name only.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fc, err := newFileClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		// Pass --name through to UploadAs so Notion stores the file
		// under the caller's label (used in the create-request filename
		// and the multipart "file" part name). When --name is empty,
		// UploadAs falls back to the source path's basename.
		ref, err := fc.UploadAs(context.Background(), args[0], blocksAddFileName)
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("add-file: %w", err))
		}
		name := ref.Name
		if globalJSON {
			// Emit an action envelope so the caller's --name is visible
			// in the JSON stream. ref itself is nested so consumers can
			// still pick the raw upload response off it; `name` on the
			// envelope is the name the caller asked for (the value
			// displayed in Notion), which can differ from ref.Name when
			// --name overrides it.
			return jsonErrorOr(cmd, emitOK(cmd.OutOrStdout(), map[string]interface{}{
				"action": "add-file",
				"id":     ref.ID,
				"name":   name,
				"ref":    ref,
			}))
		}
		color.Green("Uploaded file %s (id=%s) — block append deferred", name, ref.ID)
		return nil
	},
}

// setPageImage is the shared body of `pages set-icon` and
// `pages set-cover`. field is "icon" or "cover".
//
// Order matters: the page is fetched BEFORE the upload. Both commands
// used to upload first and print a success line naming the page without
// ever contacting it, so a typo'd or unshared page id exited 0 having
// done nothing to that page (issue #82). Validating first also avoids
// leaving an orphaned file upload behind when the id is wrong.
func setPageImage(cmd *cobra.Command, field, pageID, path string) error {
	apiKey, _ := utils.SetAPIConfig()
	if apiKey == "" {
		return jsonErrorOr(cmd, fmt.Errorf("set-%s: %w", field, utils.ErrMissingAPIKey))
	}
	client := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))
	pc := utils.NewPageClient(client)
	ctx := context.Background()

	if _, err := pc.Get(ctx, pageID); err != nil {
		return jsonErrorOr(cmd, fmt.Errorf("set-%s: page %s: %w", field, pageID, err))
	}

	ref, err := utils.NewFileClient(client).Upload(ctx, path)
	if err != nil {
		return jsonErrorOr(cmd, fmt.Errorf("set-%s: %w", field, err))
	}

	set := pc.SetIcon
	if field == "cover" {
		set = pc.SetCover
	}
	if err := set(ctx, pageID, ref); err != nil {
		return jsonErrorOr(cmd, fmt.Errorf("set-%s: %w", field, err))
	}

	if globalJSON {
		return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), map[string]interface{}{
			"ok":      true,
			"page_id": pageID,
			"field":   field,
			"ref":     ref,
		}))
	}
	color.Green("Set %s on page %s (file id=%s)", field, pageID, ref.ID)
	return nil
}

// pagesSetIconCmd uploads a local image and sets it as the page's icon.
var pagesSetIconCmd = &cobra.Command{
	Use:   "set-icon <page-id> <path>",
	Short: "Upload a local image and set it as the page icon",
	Long: `Upload a local image to Notion and set it as the page's icon.

The page id is verified before the upload, so a bad or unshared id
fails with the API's own error instead of silently succeeding.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setPageImage(cmd, "icon", args[0], args[1])
	},
}

// pagesSetCoverCmd uploads a local image and sets it as the page's cover.
var pagesSetCoverCmd = &cobra.Command{
	Use:   "set-cover <page-id> <path>",
	Short: "Upload a local image and set it as the page cover",
	Long: `Upload a local image to Notion and set it as the page's cover.

The page id is verified before the upload, so a bad or unshared id
fails with the API's own error instead of silently succeeding.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setPageImage(cmd, "cover", args[0], args[1])
	},
}

func init() {
	blocksCmd.AddCommand(blocksAddImageCmd)
	blocksCmd.AddCommand(blocksAddFileCmd)
	pagesCmd.AddCommand(pagesSetIconCmd)
	pagesCmd.AddCommand(pagesSetCoverCmd)

	blocksAddFileCmd.Flags().StringVar(&blocksAddFileName, "name", "", "Override the filename displayed in Notion")
}
