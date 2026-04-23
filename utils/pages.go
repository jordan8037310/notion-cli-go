// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

// PageClient is the typed resource client for the Notion pages API.
//
// TODO(#5): flesh out with Retrieve, Create, Update, Archive methods.
// Today this is a deliberate stub so the shape of the refactor is visible
// without expanding the scope of issue #1.
type PageClient struct {
	c *Client
}

// NewPageClient wraps a *Client with page-resource methods.
func NewPageClient(c *Client) *PageClient {
	return &PageClient{c: c}
}
