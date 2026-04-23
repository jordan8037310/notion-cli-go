// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

// SearchClient is the typed resource client for the Notion search API.
//
// TODO(#8): flesh out with Query method, filter + sort options.
// Today this is a deliberate stub so the shape of the refactor is visible
// without expanding the scope of issue #1.
type SearchClient struct {
	c *Client
}

// NewSearchClient wraps a *Client with search-resource methods.
func NewSearchClient(c *Client) *SearchClient {
	return &SearchClient{c: c}
}
