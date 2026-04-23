// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

// DatabaseClient is the typed resource client for the Notion databases API.
//
// TODO(#6): flesh out with Retrieve, Query, Create, Update methods.
// Today this is a deliberate stub so the shape of the refactor is visible
// without expanding the scope of issue #1.
type DatabaseClient struct {
	c *Client
}

// NewDatabaseClient wraps a *Client with database-resource methods.
func NewDatabaseClient(c *Client) *DatabaseClient {
	return &DatabaseClient{c: c}
}
