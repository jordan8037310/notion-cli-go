// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
)

// Team is the Notion team object. The 2026-03-11 API exposes a minimal
// surface for workspace teams: an object discriminator, an ID, and a
// human-readable name. Additional fields may be added by Notion in
// future versions; json.Unmarshal ignores unknown keys so this struct
// stays forward-compatible.
type Team struct {
	Object string `json:"object"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
}

// TeamList is a single page of teams returned by GET /v1/teams. The
// pagination envelope matches every other Notion list endpoint: results
// plus (has_more, next_cursor) for cursor-based follow-ups.
type TeamList struct {
	Object     string `json:"object"`
	Results    []Team `json:"results"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// TeamClient is the typed resource client for the Notion teams API.
//
// **Status (2026-05): teams API is unavailable on Notion-Version
// 2026-03-11.** Live calls to GET /v1/teams return 400
// `invalid_request_url` regardless of integration scope. The API was
// either removed, renamed (possibly to /v1/teamspaces), or moved to an
// enterprise-only namespace — Notion's docs do not currently document
// any working path.
//
// Until Notion re-exposes the endpoint we surface ErrTeamsNotSupported
// from every method on this client so callers (notably `notioncli teams
// list`) get a typed, clear failure mode instead of an opaque API 400.
// Tests that exercise the legacy /v1/teams shape continue to use the
// httptest mocks in teams_test.go, but the production methods short-
// circuit before reaching the network in normal use.
//
// Re-enable by reverting the ErrTeamsNotSupported guards in ListPage
// and List once a working endpoint is identified — the request shape
// is preserved in the helper functions below for that future restoration.
type TeamClient struct {
	c *Client
}

// ErrTeamsNotSupported is returned by every TeamClient method while the
// Notion teams API path is unknown on Notion-Version 2026-03-11. See
// the TeamClient godoc for context. Match with errors.Is in callers.
var ErrTeamsNotSupported = fmt.Errorf("notion teams API unavailable on Notion-Version 2026-03-11 (issue #37)")

// NewTeamClient wraps a *Client with team-resource methods.
func NewTeamClient(c *Client) *TeamClient {
	return &TeamClient{c: c}
}

// checkAuth ensures the underlying Client has a non-empty API key so
// missing-credential errors surface as ErrMissingAPIKey rather than an
// opaque 401 from Notion. ErrMissingAPIKey is defined in pages.go and
// shared across typed resource clients.
func (t *TeamClient) checkAuth() error {
	if t == nil || t.c == nil || t.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// ListPage returns a single page of teams. Currently short-circuits
// with ErrTeamsNotSupported — see the TeamClient godoc for the
// underlying API status. The auth check still runs first so a missing
// API key continues to surface as ErrMissingAPIKey (consistent with
// every other typed client).
func (t *TeamClient) ListPage(ctx context.Context, cursor string) (*TeamList, error) {
	if err := t.checkAuth(); err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return nil, fmt.Errorf("list teams: %w", ErrTeamsNotSupported)
}

// List returns every team visible to the integration. Currently a
// stub returning ErrTeamsNotSupported — see the TeamClient godoc for
// context.
func (t *TeamClient) List(ctx context.Context) ([]Team, error) {
	if err := t.checkAuth(); err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return nil, fmt.Errorf("list teams: %w", ErrTeamsNotSupported)
}
