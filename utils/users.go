// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

// UserClient is the typed resource client for the Notion users API.
//
// TODO: flesh out with List, Retrieve, and Me methods (see MCP-parity
// tracking issues). Today this is a deliberate stub so the shape of the
// refactor is visible without expanding the scope of issue #1.
type UserClient struct {
	c *Client
}

// NewUserClient wraps a *Client with user-resource methods.
func NewUserClient(c *Client) *UserClient {
	return &UserClient{c: c}
}
