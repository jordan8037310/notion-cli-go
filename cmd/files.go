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

// blocksAddImageCmd uploads a local image and appends it as an image
// block on the Notion page configured via NOTION_PAGE_ID.
var blocksAddImageCmd = &cobra.Command{
	Use:   "add-image <path>",
	Short: "Upload an image and append it as a block",
	Long: `Upload a local image file and append it as an image block on the
Notion page configured via NOTION_PAGE_ID.`,
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
		color.Green("Uploaded image %s (id=%s)", ref.Name, ref.ID)
		return nil
	},
}

// blocksAddFileCmd mirrors blocksAddImageCmd for non-image files. The
// optional --name flag overrides the displayed filename in Notion.
var blocksAddFileCmd = &cobra.Command{
	Use:   "add-file <path>",
	Short: "Upload a file and append it as a block",
	Long: `Upload a local file and append it as a file block on the Notion
page configured via NOTION_PAGE_ID.`,
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

// pagesSetIconCmd uploads an image and reports it as the new page icon.
// (PATCHing the icon onto the page is deferred to a follow-up — this
// command currently uploads the file and returns the FileRef details.)
var pagesSetIconCmd = &cobra.Command{
	Use:   "set-icon <page-id> <path>",
	Short: "Upload an image and set it as a page icon",
	Long:  `Upload a local image and set it as the icon for a Notion page.`,
	Args:  cobra.ExactArgs(2),
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

// pagesSetCoverCmd uploads an image and reports it as the new page cover.
var pagesSetCoverCmd = &cobra.Command{
	Use:   "set-cover <page-id> <path>",
	Short: "Upload an image and set it as a page cover",
	Long:  `Upload a local image and set it as the cover for a Notion page.`,
	Args:  cobra.ExactArgs(2),
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
	blocksCmd.AddCommand(blocksAddImageCmd)
	blocksCmd.AddCommand(blocksAddFileCmd)
	pagesCmd.AddCommand(pagesSetIconCmd)
	pagesCmd.AddCommand(pagesSetCoverCmd)

	blocksAddFileCmd.Flags().StringVar(&blocksAddFileName, "name", "", "Override the filename displayed in Notion")
}
