// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Flag state for the comments subcommands. Declared at package scope so
// cobra's StringVar bindings attach to stable addresses, matching the
// pattern used by blocks.go / list.go.
var (
	commentsListJSON     bool
	commentsCreateText   string
	commentsCreateDiscID string
	commentsCreateJSON   bool
)

// commentsCmd is the parent command for comment operations.
var commentsCmd = &cobra.Command{
	Use:   "comments",
	Short: "List and create Notion comments",
	Long: `Manage comments on a Notion page or block.

Examples:
  notioncli comments list <page-or-block-id>
  notioncli comments list <page-or-block-id> --json
  notioncli comments create <page-or-block-id> --text "hello"
  notioncli comments create <block-id> --text "reply" --discussion-id <id>`,
}

// commentsListCmd runs GET /v1/comments?block_id=<id> with pagination.
var commentsListCmd = &cobra.Command{
	Use:   "list <block-or-page-id>",
	Short: "List comments attached to a page or block",
	Long: `List every comment on the given page or block, following pagination.

Output defaults to a human-readable summary. Pass --json to emit the raw
Notion comment objects as a JSON array on stdout.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]
		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL()))
		cc := utils.NewCommentClient(client)

		comments, err := cc.List(context.Background(), target)
		if err != nil {
			color.Red("Error listing comments: %v", err)
			return
		}

		if commentsListJSON {
			if err := json.NewEncoder(os.Stdout).Encode(comments); err != nil {
				color.Red("Error encoding JSON: %v", err)
			}
			return
		}

		if len(comments) == 0 {
			color.Yellow("No comments found.")
			return
		}

		fmt.Println()
		for _, c := range comments {
			fmt.Println(formatComment(c))
		}
		fmt.Println()
		color.Cyan("  %d comment(s)", len(comments))
	},
}

// commentsCreateCmd runs POST /v1/comments. One of --discussion-id (reply)
// or the positional id (top-level) must be set.
var commentsCreateCmd = &cobra.Command{
	Use:   "create <block-or-page-id>",
	Short: "Create a comment on a page, block, or discussion",
	Long: `Create a Notion comment.

Without --discussion-id, the positional id is treated as a page or block id
and a top-level comment is posted. With --discussion-id, the positional id
is ignored (pass "-" if you have no natural id to supply) and the comment
is appended to the named discussion thread.

Note: when posting a top-level comment the id you pass is sent on the wire
as parent.block_id regardless of whether it refers to a page or a block.
The Notion comments API treats page ids as block ids for this endpoint, so
a single field serves both cases.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]
		if strings.TrimSpace(commentsCreateText) == "" {
			color.Red("Error: --text is required")
			return
		}

		req := utils.CreateCommentRequest{
			RichText: utils.NewCommentRichText(commentsCreateText),
		}
		if commentsCreateDiscID != "" {
			req.DiscussionID = commentsCreateDiscID
		} else {
			// Default to attaching as a top-level comment. The Notion API
			// accepts either page_id or block_id under parent; we send
			// block_id because the CLI surface always receives a "block or
			// page id" and Notion treats page ids as block ids for this
			// endpoint.
			req.Parent = &utils.CommentParent{BlockID: target}
		}

		notionAPIKey, _ := utils.SetAPIConfig()
		client := utils.NewClient(notionAPIKey, utils.WithBaseURL(utils.GetBaseURL()))
		cc := utils.NewCommentClient(client)

		created, err := cc.Create(context.Background(), req)
		if err != nil {
			color.Red("Error creating comment: %v", err)
			return
		}

		if commentsCreateJSON {
			if err := json.NewEncoder(os.Stdout).Encode(created); err != nil {
				color.Red("Error encoding JSON: %v", err)
			}
			return
		}

		color.Green("Created comment %s", created.ID)
	},
}

// formatComment renders a single comment as "author created-at text (id)".
// Text is truncated to 80 runes so paginated output stays scannable.
func formatComment(c utils.Comment) string {
	text := commentPlainText(c)
	if len([]rune(text)) > 80 {
		r := []rune(text)
		text = string(r[:77]) + "..."
	}
	author := c.CreatedBy.ID
	if author == "" {
		author = "(unknown)"
	}
	return fmt.Sprintf("  %s  %s  %s  (%s)", author, c.CreatedTime, text, c.ID)
}

// commentPlainText collapses a comment's rich_text runs into a single string
// for human output. Empty input yields the literal "(empty)" so the column
// layout does not collapse on bodyless comments.
func commentPlainText(c utils.Comment) string {
	if len(c.RichText) == 0 {
		return "(empty)"
	}
	var b strings.Builder
	for _, r := range c.RichText {
		b.WriteString(r.PlainText)
	}
	if b.Len() == 0 {
		return "(empty)"
	}
	return b.String()
}

func init() {
	rootCmd.AddCommand(commentsCmd)
	commentsCmd.AddCommand(commentsListCmd)
	commentsCmd.AddCommand(commentsCreateCmd)

	commentsListCmd.Flags().BoolVar(&commentsListJSON, "json", false, "Emit raw Notion objects as JSON")

	commentsCreateCmd.Flags().StringVar(&commentsCreateText, "text", "", "Comment text (required)")
	commentsCreateCmd.Flags().StringVar(&commentsCreateDiscID, "discussion-id", "", "Reply to an existing discussion thread")
	commentsCreateCmd.Flags().BoolVar(&commentsCreateJSON, "json", false, "Emit the created comment as JSON")
}
