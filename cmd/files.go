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

// uploadAndAppend is the shared body of `blocks add-image` and
// `blocks add-file`: upload the local file, then append a media block
// referencing it.
//
// The append used to be "deferred", so both commands uploaded and reported
// success while creating nothing — a page listed no blocks afterwards. Same
// shape as issue #82, which fixed `pages set-icon` and left these two
// behind. The plumbing arrived with that work: AddBlock already takes
// WithFileUploadID (issue #124).
//
// Order matters. resolvePageID runs BEFORE the upload so a missing or
// unresolvable --page fails without first leaving an orphaned file upload
// in the workspace — the same reasoning as set-icon's page probe.
func uploadAndAppend(cmd *cobra.Command, blockType, path, displayName string) error {
	pageID, err := resolvePageID()
	if err != nil {
		return jsonErrorOr(cmd, fmt.Errorf("blocks add-%s: %w", blockType, err))
	}
	fc, err := newFileClient()
	if err != nil {
		return jsonErrorOr(cmd, err)
	}
	ref, err := fc.UploadAs(context.Background(), path, displayName)
	if err != nil {
		return jsonErrorOr(cmd, fmt.Errorf("add-%s: %w", blockType, err))
	}

	apiKey, _ := utils.SetAPIConfig()
	if err := utils.AddBlock(apiKey, pageID, blockType, "", utils.WithFileUploadID(ref.ID)); err != nil {
		return jsonErrorOr(cmd, fmt.Errorf(
			"add-%s: uploaded %s (id=%s) but could not append the block: %w",
			blockType, ref.Name, ref.ID, err))
	}

	name := ref.Name
	if displayName != "" {
		name = displayName
	}
	if globalJSON {
		return jsonErrorOr(cmd, emitOK(cmd.OutOrStdout(), map[string]interface{}{
			"action":  "add-" + blockType,
			"id":      ref.ID,
			"name":    name,
			"page_id": pageID,
			"ref":     ref,
		}))
	}
	color.Green("Added %s block on page %s: %s (file id=%s)", blockType, pageID, name, ref.ID)
	return nil
}

// blocksAddImageCmd uploads a local image via the Notion file-upload
// endpoint and prints the resulting FileRef. Appending it as an image
// block on the configured page is deferred to a follow-up — the block
// PATCH is not issued by this command today.
var blocksAddImageCmd = &cobra.Command{
	Use:   "add-image <path>",
	Short: "Upload a local image and append it as an image block",
	Long: `Upload a local image to Notion and append it as an image block on
the target page (--page, or NOTION_PAGE_ID).

The page is resolved before the upload, so a bad target fails without
leaving an orphaned file upload behind.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return uploadAndAppend(cmd, "image", args[0], "")
	},
}

// blocksAddFileCmd mirrors blocksAddImageCmd for non-image files. Like
// add-image, block append is deferred to a follow-up; today this only
// uploads and prints the FileRef. The optional --name flag overrides
// the displayed filename in Notion.
var blocksAddFileCmd = &cobra.Command{
	Use:   "add-file <path>",
	Short: "Upload a local file and append it as a file block",
	Long: `Upload a local file to Notion and append it as a file block on the
target page (--page, or NOTION_PAGE_ID).

--name overrides the label Notion stores and displays. The page is resolved
before the upload, so a bad target fails without leaving an orphaned file
upload behind.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return uploadAndAppend(cmd, "file", args[0], blocksAddFileName)
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
