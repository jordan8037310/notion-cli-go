// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// CommentParent identifies the page or block a top-level comment is attached
// to. Only one of PageID or BlockID is typically populated on responses; when
// constructing a CreateCommentRequest, exactly one must be set.
type CommentParent struct {
	Type    string `json:"type,omitempty"`
	PageID  string `json:"page_id,omitempty"`
	BlockID string `json:"block_id,omitempty"`
}

// CommentUser is the abbreviated user object returned on comment.created_by.
// The Notion API returns richer user objects, but the CLI only needs the id
// for human output today; the remainder is kept in a raw map if ever needed.
type CommentUser struct {
	Object string `json:"object"`
	ID     string `json:"id"`
}

// Comment mirrors the subset of the Notion comment object the CLI renders.
// Unknown fields are ignored — callers who want the raw JSON body should use
// the --json flag on the CLI, which prints the server payload verbatim.
type Comment struct {
	Object         string        `json:"object"`
	ID             string        `json:"id"`
	Parent         CommentParent `json:"parent"`
	DiscussionID   string        `json:"discussion_id"`
	CreatedTime    string        `json:"created_time"`
	LastEditedTime string        `json:"last_edited_time"`
	CreatedBy      CommentUser   `json:"created_by"`
	RichText       []RichText    `json:"rich_text"`
}

// CommentList is a single page of comment results, as returned by
// GET /v1/comments?block_id=....
type CommentList struct {
	Object     string    `json:"object"`
	Results    []Comment `json:"results"`
	NextCursor string    `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
}

// CreateCommentRequest models the body accepted by POST /v1/comments.
//
// Exactly one of Parent (with PageID or BlockID) or DiscussionID must be
// set; the Notion API rejects bodies with both. Validate before calling
// Create — see (CommentClient).Create.
type CreateCommentRequest struct {
	Parent       *CommentParent `json:"parent,omitempty"`
	DiscussionID string         `json:"discussion_id,omitempty"`
	RichText     []RichText     `json:"rich_text"`
}

// CommentClient is the typed resource client for the Notion comments API.
type CommentClient struct {
	c *Client
}

// NewCommentClient wraps a *Client with comment-resource methods.
func NewCommentClient(c *Client) *CommentClient {
	return &CommentClient{c: c}
}

// ListPage fetches a single page of comments for the given block or page id.
// Pass an empty cursor to start from the first page; subsequent calls should
// use the NextCursor value from the previous response.
//
// Callers who want every comment in one call should use List, which handles
// pagination internally.
func (cc *CommentClient) ListPage(ctx context.Context, blockID, cursor string) (*CommentList, error) {
	if blockID == "" {
		return nil, fmt.Errorf("block id is required")
	}
	q := url.Values{}
	q.Set("block_id", blockID)
	if cursor != "" {
		q.Set("start_cursor", cursor)
	}
	path := "/comments?" + q.Encode()

	req, err := cc.c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cc.c.do(req)
	if err != nil {
		return nil, err
	}
	var list CommentList
	if err := decodeInto(resp, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// List returns every comment attached to the given block or page, following
// pagination until the API reports has_more=false. Returns an empty slice
// (not nil) when the target has no comments.
func (cc *CommentClient) List(ctx context.Context, blockID string) ([]Comment, error) {
	if blockID == "" {
		return nil, fmt.Errorf("block id is required")
	}
	var all []Comment
	cursor := ""
	for {
		page, err := cc.ListPage(ctx, blockID, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if all == nil {
		all = []Comment{}
	}
	return all, nil
}

// Create posts a new comment. The request must identify the target in one
// (and only one) of two ways:
//
//   - Parent set with PageID or BlockID — creates a top-level comment.
//   - DiscussionID set — appends to an existing discussion thread.
//
// Requests with neither or both are rejected client-side before the API
// call so the failure mode is predictable and testable without network.
func (cc *CommentClient) Create(ctx context.Context, req CreateCommentRequest) (*Comment, error) {
	if err := validateCreateCommentRequest(req); err != nil {
		return nil, err
	}
	httpReq, err := cc.c.newRequest(ctx, http.MethodPost, "/comments", req)
	if err != nil {
		return nil, err
	}
	resp, err := cc.c.do(httpReq)
	if err != nil {
		return nil, err
	}
	var out Comment
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// validateCreateCommentRequest enforces the "exactly one target" rule for
// POST /v1/comments. Kept as a free function so tests can exercise the
// validation logic without constructing a Client.
func validateCreateCommentRequest(req CreateCommentRequest) error {
	if len(req.RichText) == 0 {
		return fmt.Errorf("rich_text is required")
	}
	hasParent := req.Parent != nil && (req.Parent.PageID != "" || req.Parent.BlockID != "")
	hasDiscussion := req.DiscussionID != ""
	if !hasParent && !hasDiscussion {
		return fmt.Errorf("create comment: either parent.page_id/block_id or discussion_id must be set")
	}
	if hasParent && hasDiscussion {
		return fmt.Errorf("create comment: parent and discussion_id are mutually exclusive")
	}
	if hasParent && req.Parent.PageID != "" && req.Parent.BlockID != "" {
		return fmt.Errorf("create comment: parent.page_id and parent.block_id are mutually exclusive")
	}
	return nil
}

// NewCommentRichText constructs a minimal rich_text slice with a single text
// run. Callers building a CreateCommentRequest from a plain string (for
// example, the CLI's --text flag) should use this to avoid hand-assembling
// the nested shape.
func NewCommentRichText(text string) []RichText {
	return []RichText{{
		Type:      "text",
		Text:      Text{Content: text},
		PlainText: text,
	}}
}
