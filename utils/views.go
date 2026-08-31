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
	"net/http"
	"net/url"
)

// ValidViewTypes is the closed set of view types accepted by
// CreateViewRequest.Validate, mirroring the Notion API's supported
// database view surfaces.
var ValidViewTypes = []string{"table", "board", "list", "gallery", "calendar", "timeline"}

// View is the Notion view object as returned by the data-source views
// endpoints. Config is a json.RawMessage so callers can round-trip
// arbitrary view-configuration payloads byte-for-byte (preserving key
// order and avoiding the float64 coercion of numeric IDs).
type View struct {
	Object string `json:"object"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`
	// DataSourceID and Parent are what the API actually returns; the
	// response carries `data_source_id` at the top level and a
	// `parent` of type database_id. DatabaseID is retained for
	// back-compat with callers that read it.
	DataSourceID string          `json:"data_source_id,omitempty"`
	Parent       PageParent      `json:"parent,omitempty"`
	DatabaseID   string          `json:"database_id,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
}

// ViewList is a page of GET /v1/views results.
type ViewList struct {
	Object     string `json:"object"`
	Results    []View `json:"results"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// CreateViewRequest is the body for POST to a data source's views
// endpoint. DatabaseID is the data-source ID the view is created under
// (Notion's 2026-03-11 API routes this as the data_source_id path
// parameter). Name and Type are required. Config is an optional
// free-form payload the upstream API accepts to override column order,
// filters, sorts, etc.
type CreateViewRequest struct {
	// DataSourceID is the data source the view reads. Required.
	DataSourceID string `json:"-"`
	// DatabaseID is the container the view belongs to. The API requires
	// exactly one of database_id / view_id / create_database alongside
	// data_source_id; this client supplies database_id.
	DatabaseID string          `json:"-"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// Validate returns a non-nil error if the request is missing a required
// field or carries an unknown Type.
func (r CreateViewRequest) Validate() error {
	if r.DataSourceID == "" {
		return errors.New("create view: --data-source is required (the view reads a data source; `databases data-sources <db-id>` lists them)")
	}
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

// UpdateViewRequest is the body for PATCH /v1/views/{id}. All fields
// are optional — Validate only rejects the zero-value "nothing to
// update" case so callers don't silently hit the wire with a no-op.
type UpdateViewRequest struct {
	Name   string          `json:"name,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// Validate returns a non-nil error when the update request carries no
// meaningful fields. An empty Name combined with an absent, empty, or
// null Config is treated as "nothing to update". A Config of `{}` or
// `[]` is also rejected for the same reason.
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
// Requires Notion-Version 2026-03-11 or newer — the data-source views
// endpoints were introduced alongside the data-source migration.
type ViewClient struct {
	c *Client
}

// NewViewClient wraps a *Client with view-resource methods.
func NewViewClient(c *Client) *ViewClient {
	return &ViewClient{c: c}
}

// checkAuth ensures the underlying Client has a non-empty API key.
func (v *ViewClient) checkAuth() error {
	if v == nil || v.c == nil || v.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// Create POSTs a new view on the given data source via
// POST /v1/data_sources/{database_id}/views. Validates the request
// before paying the network cost so bad input produces a precise error.
func (v *ViewClient) Create(ctx context.Context, req CreateViewRequest) (*View, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if err := v.checkAuth(); err != nil {
		return nil, err
	}

	// POST /v1/views — NOT /v1/data_sources/{id}/views, which this client
	// used to call and which does not exist: the live API answers it with
	// 400 invalid_request_url, so `views create` failed 100% of the time
	// while the unit tests passed against the invented shape (issue #102).
	//
	// The ids go in the BODY. Notion requires data_source_id plus exactly
	// one of database_id / view_id / create_database — verified live, the
	// validation_error names those three by name.
	body := map[string]interface{}{
		"name":           req.Name,
		"type":           req.Type,
		"data_source_id": req.DataSourceID,
		"database_id":    req.DatabaseID,
	}
	if !isEmptyRawJSON(req.Config) {
		body["config"] = req.Config
	}

	httpReq, err := v.c.newRequest(ctx, http.MethodPost, "/views", body)
	if err != nil {
		return nil, err
	}
	resp, err := v.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}
	var view View
	if err := decodeInto(resp, &view); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}
	return &view, nil
}

// Get retrieves a single view. Added with List because `views update`
// requires a view id the CLI previously gave no way to obtain — the same
// shape of gap as the data source ids in issue #94 (issue #103).
func (v *ViewClient) Get(ctx context.Context, id string) (*View, error) {
	if id == "" {
		return nil, errors.New("get view: id is required")
	}
	if err := v.checkAuth(); err != nil {
		return nil, err
	}
	req, err := v.c.newRequest(ctx, http.MethodGet, "/views/"+id, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get view: %w", err)
	}
	var view View
	if err := decodeInto(resp, &view); err != nil {
		return nil, fmt.Errorf("get view: %w", err)
	}
	return &view, nil
}

// List walks every view on a data source. Paginated: the endpoint returns
// has_more/next_cursor like every other list surface, and reading only the
// first page is the defect pattern behind issues #55, #57 and #108.
func (v *ViewClient) List(ctx context.Context, dataSourceID string) ([]View, error) {
	if dataSourceID == "" {
		return nil, errors.New("list views: data source id is required")
	}
	if err := v.checkAuth(); err != nil {
		return nil, err
	}

	all := []View{}
	cursor := ""
	for {
		q := url.Values{}
		q.Set("data_source_id", dataSourceID)
		if cursor != "" {
			q.Set("start_cursor", cursor)
		}
		req, err := v.c.newRequest(ctx, http.MethodGet, "/views?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := v.c.do(req)
		if err != nil {
			return nil, fmt.Errorf("list views: %w", err)
		}
		var page ViewList
		if err := decodeInto(resp, &page); err != nil {
			return nil, fmt.Errorf("list views: %w", err)
		}
		all = append(all, page.Results...)
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// Update PATCHes an existing view by ID via PATCH /v1/views/{id}.
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

	body := map[string]interface{}{}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if !isEmptyRawJSON(req.Config) {
		body["config"] = req.Config
	}

	httpReq, err := v.c.newRequest(ctx, http.MethodPatch, "/views/"+id, body)
	if err != nil {
		return nil, err
	}
	resp, err := v.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("update view: %w", err)
	}
	var view View
	if err := decodeInto(resp, &view); err != nil {
		return nil, fmt.Errorf("update view: %w", err)
	}
	return &view, nil
}
