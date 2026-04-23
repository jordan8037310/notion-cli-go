// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"errors"
)

// ErrTeamsNotSupported is returned by TeamClient methods when the pinned
// Notion API version does not expose a teams endpoint. The MCP server's
// notion-get-teams is implemented against a newer Notion API surface than
// the 2022-06-28 version this CLI currently pins. Issue #11 tracks the
// version bump that will enable a real implementation; until then this
// client surfaces a typed error so callers can check for it with
// errors.Is.
var ErrTeamsNotSupported = errors.New("teams are not supported on Notion-Version " + NotionAPIVersion + "; will be enabled by issue #11")

// Team is a placeholder for the Notion team object. The concrete shape
// depends on a newer Notion API version than the CLI currently pins, so
// the fields here are deliberately minimal — do not rely on them yet.
type Team struct {
	Object string `json:"object"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
}

// TeamList is a single page of teams. Populated once issue #11 bumps the
// pinned Notion-Version and a real endpoint is available.
type TeamList struct {
	Object     string `json:"object"`
	Results    []Team `json:"results"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// TeamClient is the typed resource client for the Notion teams API.
//
// The methods currently return ErrTeamsNotSupported because the pinned
// Notion-Version does not expose a teams endpoint. The client shape is
// kept stable so issue #11 can flip the implementation without breaking
// callers.
type TeamClient struct {
	c *Client
}

// NewTeamClient wraps a *Client with team-resource methods.
func NewTeamClient(c *Client) *TeamClient {
	return &TeamClient{c: c}
}

// List would return every team visible to the integration. On the pinned
// Notion-Version it returns ErrTeamsNotSupported.
func (t *TeamClient) List(ctx context.Context) ([]Team, error) {
	return nil, ErrTeamsNotSupported
}

// ListPage would return a single page of teams. On the pinned
// Notion-Version it returns ErrTeamsNotSupported.
func (t *TeamClient) ListPage(ctx context.Context, cursor string) (*TeamList, error) {
	return nil, ErrTeamsNotSupported
}
