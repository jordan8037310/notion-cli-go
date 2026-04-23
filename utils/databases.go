// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DatabaseClient is the typed resource client for the Notion databases API.
// Build one with NewDatabaseClient and reuse across calls — it is safe for
// concurrent use because it is a thin wrapper around *Client's *http.Client.
type DatabaseClient struct {
	c *Client
}

// NewDatabaseClient wraps a *Client with database-resource methods.
func NewDatabaseClient(c *Client) *DatabaseClient {
	return &DatabaseClient{c: c}
}

// checkAuth ensures the underlying Client has a non-empty API key. Every
// HTTP-calling method on DatabaseClient calls this before issuing a request so
// missing-credential errors surface as ErrMissingAPIKey rather than as an
// opaque 401 from Notion. Mirrors PageClient.checkAuth.
func (d *DatabaseClient) checkAuth() error {
	if d == nil || d.c == nil || d.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// Database is the envelope returned by /v1/databases endpoints. Title,
// Properties, and Parent are loosely-typed so callers can round-trip the
// full Notion schema surface without this package having to model every
// Notion property variant. A typed property surface can land in a follow-up.
type Database struct {
	Object         string                 `json:"object"`
	ID             string                 `json:"id"`
	CreatedTime    string                 `json:"created_time"`
	LastEditedTime string                 `json:"last_edited_time"`
	Archived       bool                   `json:"archived"`
	URL            string                 `json:"url"`
	Title          []RichText             `json:"title"`
	Parent         PageParent             `json:"parent"`
	Properties     map[string]interface{} `json:"properties"`
}

// DatabaseProperty is the v1 shape for a single schema entry in a
// CreateDatabaseRequest or UpdateDatabaseRequest. The Notion property type
// surface is large and evolving, so the payload is left as a loose map for
// now. Callers construct these from a --properties-json file.
type DatabaseProperty = map[string]interface{}

// CreateDatabaseRequest is the body for POST /v1/databases. Parent.PageID is
// required by the Notion API. Title, when non-empty, is folded into a
// minimal title rich-text array. Properties maps property name → type
// descriptor and is required by the Notion API for any non-trivial database;
// see https://developers.notion.com/reference/create-a-database.
type CreateDatabaseRequest struct {
	Parent     PageParent                  `json:"parent"`
	Title      string                      `json:"-"`
	Properties map[string]DatabaseProperty `json:"properties,omitempty"`
}

// UpdateDatabaseRequest is the body for PATCH /v1/databases/{id}. All fields
// are optional: unset fields are not sent. Title, when non-empty, is folded
// into a minimal title rich-text array. Properties, when non-nil, updates
// the schema (rename, add, remove by setting a property to nil).
type UpdateDatabaseRequest struct {
	Title      string                      `json:"-"`
	Properties map[string]DatabaseProperty `json:"properties,omitempty"`
}

// QueryResponse is a single page of database query results. Mirrors the
// envelope returned by POST /v1/databases/{id}/query.
type QueryResponse struct {
	Object     string `json:"object"`
	Results    []Page `json:"results"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// titleRichText returns a minimal Notion rich-text array carrying the given
// plain-text title. Used by Create/Update to turn a --title flag into the
// payload shape Notion expects on the "title" key of the request body.
func titleRichText(title string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"type": "text",
			"text": map[string]interface{}{"content": title},
		},
	}
}

// Get retrieves a database by ID via GET /v1/databases/{id}.
func (d *DatabaseClient) Get(ctx context.Context, id string) (*Database, error) {
	if err := d.checkAuth(); err != nil {
		return nil, fmt.Errorf("get database: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("get database: id is required")
	}
	req, err := d.c.newRequest(ctx, http.MethodGet, "/databases/"+id, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get database: %w", err)
	}
	var db Database
	if err := decodeInto(resp, &db); err != nil {
		return nil, fmt.Errorf("get database: %w", err)
	}
	return &db, nil
}

// Query performs a single POST /v1/databases/{id}/query call and returns the
// immediate page of results. filter and sort are passed through untouched as
// the Notion API's filter and sorts keys — callers supply these as raw JSON
// read from a file so the full Notion filter/sort surface is accessible
// without this package modeling every option.
//
// cursor is the start_cursor to resume from; pass "" for the first page.
// pageSize is the Notion API's page_size (1-100, 0 means server default).
func (d *DatabaseClient) Query(ctx context.Context, id string, filter, sort json.RawMessage, cursor string, pageSize int) (*QueryResponse, error) {
	if err := d.checkAuth(); err != nil {
		return nil, fmt.Errorf("query database: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("query database: id is required")
	}

	body := map[string]interface{}{}
	if len(filter) > 0 {
		body["filter"] = filter
	}
	if len(sort) > 0 {
		body["sorts"] = sort
	}
	if cursor != "" {
		body["start_cursor"] = cursor
	}
	if pageSize > 0 {
		body["page_size"] = pageSize
	}

	req, err := d.c.newRequest(ctx, http.MethodPost, "/databases/"+id+"/query", body)
	if err != nil {
		return nil, err
	}
	resp, err := d.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("query database: %w", err)
	}
	var out QueryResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, fmt.Errorf("query database: %w", err)
	}
	return &out, nil
}

// QueryAll walks pagination until the server reports HasMore=false or the
// supplied limit is reached. A non-positive limit means "return everything".
// When limit is positive, the returned slice is truncated to exactly limit
// entries (or fewer if the server ran out first). Mid-pagination errors are
// wrapped with the 1-based page number so callers can see how far the walk
// got before failing. Mirrors SearchClient.SearchAll's pagination shape.
func (d *DatabaseClient) QueryAll(ctx context.Context, id string, filter, sort json.RawMessage, limit int) ([]Page, error) {
	var all []Page
	cursor := ""
	// pageSize is left at the server default (0) for now; callers that need
	// a specific page size can drop to Query and drive pagination themselves.
	for pageNum := 1; ; pageNum++ {
		resp, err := d.Query(ctx, id, filter, sort, cursor, 0)
		if err != nil {
			return nil, fmt.Errorf("query database page %d: %w", pageNum, err)
		}
		all = append(all, resp.Results...)
		if limit > 0 && len(all) >= limit {
			return all[:limit], nil
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return all, nil
		}
		cursor = resp.NextCursor
	}
}

// Create posts a new database to POST /v1/databases. Parent.PageID is
// required by the Notion API. If req.Title is non-empty it is folded into
// the body's "title" key as a minimal rich-text array.
func (d *DatabaseClient) Create(ctx context.Context, req CreateDatabaseRequest) (*Database, error) {
	if err := d.checkAuth(); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	if req.Parent.PageID == "" {
		return nil, fmt.Errorf("create database: parent page_id is required")
	}

	body := map[string]interface{}{
		"parent": PageParent{Type: "page_id", PageID: req.Parent.PageID},
	}
	if req.Title != "" {
		body["title"] = titleRichText(req.Title)
	}
	if len(req.Properties) > 0 {
		body["properties"] = req.Properties
	}

	httpReq, err := d.c.newRequest(ctx, http.MethodPost, "/databases", body)
	if err != nil {
		return nil, err
	}
	resp, err := d.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	var db Database
	if err := decodeInto(resp, &db); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	return &db, nil
}

// Update patches a database via PATCH /v1/databases/{id}. Both fields on the
// request are optional; at least one must be set or Update returns an error
// (Notion rejects empty-body patches). Title, when non-empty, is folded into
// a minimal rich-text array on the "title" key.
func (d *DatabaseClient) Update(ctx context.Context, id string, req UpdateDatabaseRequest) (*Database, error) {
	if err := d.checkAuth(); err != nil {
		return nil, fmt.Errorf("update database: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("update database: id is required")
	}

	body := map[string]interface{}{}
	if req.Title != "" {
		body["title"] = titleRichText(req.Title)
	}
	if len(req.Properties) > 0 {
		body["properties"] = req.Properties
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("update database: no fields to update")
	}

	httpReq, err := d.c.newRequest(ctx, http.MethodPatch, "/databases/"+id, body)
	if err != nil {
		return nil, err
	}
	resp, err := d.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("update database: %w", err)
	}
	var db Database
	if err := decodeInto(resp, &db); err != nil {
		return nil, fmt.Errorf("update database: %w", err)
	}
	return &db, nil
}
