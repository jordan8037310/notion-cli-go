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
// Requires Notion-Version 2026-03-11 or newer — the /v1/teams endpoint
// was introduced in that release.
type TeamClient struct {
	c *Client
}

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

// ListPage returns a single page of teams. Pass cursor="" for the first
// page; feed the returned NextCursor back in to walk subsequent pages.
func (t *TeamClient) ListPage(ctx context.Context, cursor string) (*TeamList, error) {
	if err := t.checkAuth(); err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	path := "/teams"
	if cursor != "" {
		path += "?start_cursor=" + url.QueryEscape(cursor)
	}
	req, err := t.c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	var page TeamList
	if err := decodeInto(resp, &page); err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	return &page, nil
}

// List returns every team visible to the integration, walking the
// /v1/teams pagination until HasMore is false. Returns an empty (non-nil)
// slice when the workspace has no teams.
func (t *TeamClient) List(ctx context.Context) ([]Team, error) {
	var all []Team
	cursor := ""
	for {
		page, err := t.ListPage(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}
