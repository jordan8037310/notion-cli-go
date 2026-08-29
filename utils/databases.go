// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	InTrash        bool                   `json:"in_trash"`
	URL            string                 `json:"url"`
	Title          []RichText             `json:"title"`
	Parent         PageParent             `json:"parent"`
	Properties     map[string]interface{} `json:"properties"`
	// DataSources lists the data sources this database contains. Present
	// only on GET /v1/databases/{id}, which since Notion-Version
	// 2025-09-03 returns a *container* envelope: title, icon, cover and
	// this array — and NO properties, because a schema belongs to a data
	// source, not to its container.
	//
	// Modelling it is what makes a data source id discoverable from the
	// CLI at all. Notion's own documented answer to "where do I find a
	// data_source_id" is to retrieve the parent database and read this
	// array, and until it was modelled the typed struct silently dropped
	// it — leaving `databases query` able to say "supply a data_source
	// ID" and offer no way to obtain one (issue #94).
	DataSources []DataSourceRef `json:"data_sources,omitempty"`
}

// DataSourceRef is one entry in a database's data_sources array: the id
// to query and the human-readable name to pick it by.
type DataSourceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DatabaseProperty is the v1 shape for a single schema entry in a
// CreateDatabaseRequest or UpdateDatabaseRequest. The Notion property type
// surface is large and evolving, so the payload is left as a loose map for
// now. Callers construct these from a --properties-json file.
//
// This is a type alias rather than a named type so godoc tooling surfaces
// it as map[string]interface{} at call sites; keep the descriptive name in
// struct fields and function signatures so intent is readable in context.
// A typed property surface can land in a follow-up (see issue #11 envelope).
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

// Get retrieves a database (or data_source) by ID. The id may point at
// either object type — Get probes /v1/databases/{id} first and falls
// back to /v1/data_sources/{id} on 404. On Notion-Version 2026-03-11
// every entry returned by `notioncli search` is a data_source, so the
// fallback is the common path in current workspaces. Mirrors the same
// dispatch logic in Query — see issue #48.
func (d *DatabaseClient) Get(ctx context.Context, id string) (*Database, error) {
	db, _, err := d.GetRaw(ctx, id)
	return db, err
}

// GetRaw is Get that also returns the undecoded response body of whichever
// surface answered, so `fetch --json` can emit exactly what Notion sent
// rather than a re-marshalled Database (issue #80).
func (d *DatabaseClient) GetRaw(ctx context.Context, id string) (*Database, json.RawMessage, error) {
	if err := d.checkAuth(); err != nil {
		return nil, nil, fmt.Errorf("get database: %w", err)
	}
	if id == "" {
		return nil, nil, fmt.Errorf("get database: id is required")
	}

	db, raw, err := d.getOnceRaw(ctx, "/databases/"+id)
	if err == nil {
		return db, raw, nil
	}
	if !isQueryFallbackTrigger(err) {
		return nil, nil, err
	}
	return d.getOnceRaw(ctx, "/data_sources/"+id)
}

// getOnceRaw is the shared transport for both /databases/{id} and
// /data_sources/{id}. The wire envelope is shape-compatible — both
// return a Database-like object with title/properties/parent/etc. —
// so callers can decode either into the existing Database type.
//
// It returns the undecoded response body alongside the typed Database so
// the caller can emit a loss-free --json round-trip (issue #80).
func (d *DatabaseClient) getOnceRaw(ctx context.Context, path string) (*Database, json.RawMessage, error) {
	req, err := d.c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := d.c.do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("get database: %w", err)
	}
	var db Database
	raw, err := decodeIntoRaw(resp, &db)
	if err != nil {
		return nil, nil, fmt.Errorf("get database: %w", err)
	}
	return &db, raw, nil
}

// Query performs a single query against a Notion queryable surface and
// returns the immediate page of results. filter and sort are passed
// through untouched as the Notion API's filter and sorts keys — callers
// supply these as raw JSON read from a file so the full Notion
// filter/sort surface is accessible without this package modeling every
// option.
//
// cursor is the start_cursor to resume from; pass "" for the first page.
// pageSize is the Notion API's page_size (1-100, 0 means server default).
//
// The id may be either a database id or a data_source id. On
// Notion-Version 2026-03-11 the queryable surface migrated from
// /v1/databases/{id}/query to /v1/data_sources/{id}/query — every entry
// returned by `notioncli search` in current workspaces is now a
// data_source, not a database. Query probes data_sources first and
// falls back to the legacy databases endpoint on 404 (and only on 404,
// so genuine auth/transport errors surface immediately). See issue #48.
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

	// Probe data_sources first (2026-03-11 default).
	out, dsErr := d.postQuery(ctx, "/data_sources/"+id+"/query", body)
	if dsErr == nil {
		return out, nil
	}
	if !isQueryFallbackTrigger(dsErr) {
		return nil, dsErr
	}
	// Fall back to the legacy databases endpoint for unmigrated DBs.
	out, dbErr := d.postQuery(ctx, "/databases/"+id+"/query", body)
	if dbErr == nil {
		return out, nil
	}
	// Both endpoints rejected the id. The verbatim API error is
	// noisy and unhelpful (`unexpected status 400: {"object":"error",
	// "code":"invalid_request_url",...}`) — wrap with a message that
	// points at the most likely cause (id type mismatch or unshared
	// resource) so the user has a concrete next step.
	if isQueryFallbackTrigger(dbErr) {
		return nil, fmt.Errorf("query database: id %q is not queryable as a data_source or database. If this is a *database* id, it is a container and cannot be queried directly — run `notioncli databases data-sources %s` to list its data sources and query one of those ids (or pass --data-source <id>). Otherwise confirm the id is shared with this integration and is not a page or block id. Underlying API error: %w", id, id, dbErr)
	}
	return nil, dbErr
}

// postQuery is the shared transport for both the data_sources and the
// databases query endpoints — same envelope shape, same body, only the
// path differs. Pulled out so Query's probe + fallback stays readable.
func (d *DatabaseClient) postQuery(ctx context.Context, path string, body map[string]interface{}) (*QueryResponse, error) {
	req, err := d.c.newRequest(ctx, http.MethodPost, path, body)
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

// isQueryFallbackTrigger reports whether err is the kind that should
// trigger a probe of the OTHER endpoint (data_sources ↔ databases).
// Two shapes qualify:
//
//  1. **404 object_not_found** — the id doesn't exist at this endpoint.
//     Triggered when probing `/data_sources/{id}/query` against a real
//     legacy database, or when probing `/databases/{id}` against a
//     real 2026-03-11 data_source.
//
//  2. **400 invalid_request_url** — Notion's response when the URL
//     pattern doesn't apply to the resource type behind the id (the
//     id is recognised but as a different object). Distinct from 404:
//     `/data_sources/{id}/query` against a database id returns 400,
//     not 404, so the fallback never fired without this branch. This
//     is the bug behind the "LS-36 reproducer" still failing on the
//     post-PR-#71 binary.
//
// utils.decodeInto wraps non-2xx as "unexpected status N: ..."; match
// the substring rather than a typed status code so the check survives
// error wrapping by callers.
//
// Renamed from isQueryNotFound — old name was too narrow for what we
// actually need to fall back on.
func isQueryFallbackTrigger(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "unexpected status 404") {
		return true
	}
	if strings.Contains(msg, "unexpected status 400") && strings.Contains(msg, "invalid_request_url") {
		return true
	}
	return false
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
		// The schema belongs to the database's INITIAL DATA SOURCE, not to
		// the database itself. Notion's 2025-09-03 upgrade guide is
		// explicit: "properties for the initial data source you're
		// creating now go under initial_data_source[properties]".
		// Top-level title/icon/cover still apply to the database.
		//
		// We pin 2026-03-11, so the pre-2025-09-03 top-level shape this
		// used to send is wrong for every request the CLI makes.
		body["initial_data_source"] = map[string]interface{}{
			"properties": req.Properties,
		}
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

	// Since 2025-09-03 these two live on different endpoints. Database
	// attributes (title, icon, cover, parent, in_trash) stay on
	// PATCH /v1/databases/{id}; the schema moved to the data source, and
	// the database endpoint no longer accepts `properties` at all. The
	// upgrade guide's instruction is to "switch over to the Update Data
	// Source API to modify attributes that apply to a specific data
	// source: properties".
	//
	// So a request carrying both is two calls against two different ids.
	var out *Database

	if req.Title != "" {
		db, err := d.patchDatabaseAttrs(ctx, id, map[string]interface{}{
			"title": titleRichText(req.Title),
		})
		if err != nil {
			return nil, err
		}
		out = db
	}

	if len(req.Properties) > 0 {
		db, err := d.patchDataSourceSchema(ctx, id, req.Properties)
		if err != nil {
			return nil, err
		}
		out = db
	}

	if out == nil {
		return nil, fmt.Errorf("update database: no fields to update")
	}
	return out, nil
}

// patchDatabaseAttrs issues PATCH /v1/databases/{id} for the attributes
// that still belong to the database container.
func (d *DatabaseClient) patchDatabaseAttrs(ctx context.Context, id string, body map[string]interface{}) (*Database, error) {
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

// patchDataSourceSchema issues PATCH /v1/data_sources/{id} with the
// property schema. id must be a DATA SOURCE id; a database id is a
// container and has no schema of its own, so the error names the command
// that lists the ids which do.
func (d *DatabaseClient) patchDataSourceSchema(ctx context.Context, id string, props map[string]DatabaseProperty) (*Database, error) {
	body := map[string]interface{}{"properties": props}
	httpReq, err := d.c.newRequest(ctx, http.MethodPatch, "/data_sources/"+id, body)
	if err != nil {
		return nil, err
	}
	resp, err := d.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("update data source schema: %w", err)
	}
	var db Database
	if err := decodeInto(resp, &db); err != nil {
		return nil, fmt.Errorf("update data source schema for %q: %w (a schema belongs to a data source, not to the database container — run `notioncli databases data-sources %s` to list the data source ids)", id, err, id)
	}
	return &db, nil
}
