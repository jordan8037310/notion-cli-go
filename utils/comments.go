// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

// CommentClient is the typed resource client for the Notion comments API.
//
// TODO(#9): flesh out with List + Create methods.
// Today this is a deliberate stub so the shape of the refactor is visible
// without expanding the scope of issue #1.
type CommentClient struct {
	c *Client
}

// NewCommentClient wraps a *Client with comment-resource methods.
func NewCommentClient(c *Client) *CommentClient {
	return &CommentClient{c: c}
}
