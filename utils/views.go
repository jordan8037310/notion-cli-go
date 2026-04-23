// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrViewsNotSupported is returned by ViewClient methods when the pinned
// Notion API version does not expose the views / data-sources endpoints.
// The MCP server's notion-create-view and notion-update-view are backed
// by a newer Notion API surface than the 2022-06-28 version this CLI
// currently pins. Issue #11 tracks the version bump that will enable a
// real implementation; until then this client surfaces a typed error so
// callers can check for it with errors.Is.
var ErrViewsNotSupported = errors.New("views are not supported on Notion-Version " + NotionAPIVersion + "; will be enabled by issue #11")

// ValidViewTypes is the closed set of view types accepted by
// CreateViewRequest.Validate. It mirrors the MCP notion-create-view
// surface so callers can rely on the same vocabulary once #11 swaps in
// a real HTTP implementation.
var ValidViewTypes = []string{"table", "board", "list", "gallery", "calendar", "timeline"}

// View is a placeholder for the Notion view object. The concrete shape
// depends on a newer Notion API version than the CLI currently pins, so
// the fields here are deliberately minimal — do not rely on them yet.
// Config is a json.RawMessage so callers can round-trip arbitrary
// view-configuration payloads byte-for-byte (preserving key order and
// avoiding the float64 coercion of numeric IDs) once #11 lands.
type View struct {
	Object     string          `json:"object"`
	ID         string          `json:"id"`
	Name       string          `json:"name,omitempty"`
	Type       string          `json:"type,omitempty"`
	DatabaseID string          `json:"database_id,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// CreateViewRequest is the body for a future POST to the views endpoint.
// DatabaseID and Name are required. Type must be one of ValidViewTypes.
// Config is an optional free-form payload that the upstream API accepts
// to override column order, filters, sorts, etc.
//
// The field shape is chosen so that issue #11's implementation swap can
// marshal this struct directly to the Notion API body with nothing more
// than `json.Marshal`.
type CreateViewRequest struct {
	DatabaseID string          `json:"database_id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// Validate returns a non-nil error if the request is missing a required
// field or carries an unknown Type. Validation runs before the stub
// sentinel so callers always see the most actionable error first (e.g.
// "empty name" beats "views not supported").
func (r CreateViewRequest) Validate() error {
	if r.DatabaseID == "" {
		return errors.New("create view: database_id is required")
	}
	if r.Name == "" {
		return errors.New("create view: name is required")
	}
	if r.Type == "" {
		return errors.New("create view: type is required")
	}
	for _, t := range ValidViewTypes {
		if r.Type == t {
			return nil
		}
	}
	return fmt.Errorf("create view: invalid type %q (want one of %v)", r.Type, ValidViewTypes)
}

// UpdateViewRequest is the body for a future PATCH to the views
// endpoint. All fields are optional — Validate only rejects the
// zero-value "nothing to update" case so callers don't silently hit the
// wire with a no-op.
type UpdateViewRequest struct {
	Name   string          `json:"name,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// Validate returns a non-nil error when the update request carries no
// meaningful fields. An empty Name combined with an absent, empty, or
// null Config is treated as "nothing to update" so callers don't
// silently hit the wire with a no-op PATCH. A Config of `{}` or `[]` is
// also rejected for the same reason — the caller clearly intended to
// send something, but didn't.
func (r UpdateViewRequest) Validate() error {
	if r.Name == "" && isEmptyRawJSON(r.Config) {
		return errors.New("update view: at least one of name or config is required")
	}
	return nil
}

// isEmptyRawJSON reports whether a json.RawMessage carries no useful
// payload: nil, zero-length, literal null, {} or [] (with whitespace).
func isEmptyRawJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	trimmed := bytes.TrimSpace(raw)
	switch string(trimmed) {
	case "", "null", "{}", "[]":
		return true
	}
	return false
}

// ViewClient is the typed resource client for the Notion views API.
//
// The methods currently return ErrViewsNotSupported because the pinned
// Notion-Version does not expose the views / data-sources endpoints.
// The client shape is kept stable so issue #11 can flip the
// implementation without breaking callers — method signatures, request
// types, and response types will not change.
type ViewClient struct {
	c *Client
}

// NewViewClient wraps a *Client with view-resource methods.
func NewViewClient(c *Client) *ViewClient {
	return &ViewClient{c: c}
}

// checkAuth ensures the underlying Client has a non-empty API key. Every
// HTTP-calling method on ViewClient calls this before issuing a request
// so missing-credential errors surface as ErrMissingAPIKey rather than
// as an opaque 401 from Notion. ErrMissingAPIKey is defined in pages.go
// and shared across typed resource clients.
func (v *ViewClient) checkAuth() error {
	if v == nil || v.c == nil || v.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// Create would POST a new view under the given database. On the pinned
// Notion-Version it validates the request and then returns
// ErrViewsNotSupported. Validation runs first so bad input produces a
// precise error instead of the sentinel.
func (v *ViewClient) Create(ctx context.Context, req CreateViewRequest) (*View, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if err := v.checkAuth(); err != nil {
		return nil, err
	}
	return nil, ErrViewsNotSupported
}

// Update would PATCH an existing view by ID. On the pinned
// Notion-Version it validates the request and then returns
// ErrViewsNotSupported.
func (v *ViewClient) Update(ctx context.Context, id string, req UpdateViewRequest) (*View, error) {
	if id == "" {
		return nil, errors.New("update view: id is required")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if err := v.checkAuth(); err != nil {
		return nil, err
	}
	return nil, ErrViewsNotSupported
}
