// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrMissingAPIKey is returned by PageClient methods when the underlying
// Client was constructed without a Notion integration token. Callers can
// match it with errors.Is to distinguish configuration problems from
// transport or Notion-side failures.
var ErrMissingAPIKey = errors.New("notion api key is required")

// PageClient is the typed resource client for the Notion pages API. Build one
// with NewPageClient and reuse across calls — it is safe for concurrent use
// because it is a thin wrapper around *Client's *http.Client.
type PageClient struct {
	c *Client
}

// NewPageClient wraps a *Client with page-resource methods.
func NewPageClient(c *Client) *PageClient {
	return &PageClient{c: c}
}

// checkAuth ensures the underlying Client has a non-empty API key. Every
// HTTP-calling method on PageClient calls this before issuing a request so
// missing-credential errors surface as ErrMissingAPIKey rather than as an
// opaque 401 from Notion.
func (p *PageClient) checkAuth() error {
	if p == nil || p.c == nil || p.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// PageParent describes where a page lives. Notion-Version 2026-03-11
// accepts exactly one of database_id, data_source_id, page_id, or
// workspace=true. The zero value is invalid; callers constructing a
// CreatePageRequest must populate one of the four identifiers.
//
// On 2026-03-11 most queryable surfaces are data_sources rather than
// databases — the Create path auto-resolves which discriminator to use
// when only DatabaseID is set, by probing the schema once. Callers who
// already know the surface type can set DataSourceID directly to skip
// the probe.
type PageParent struct {
	Type         string `json:"type,omitempty"`
	DatabaseID   string `json:"database_id,omitempty"`
	DataSourceID string `json:"data_source_id,omitempty"`
	PageID       string `json:"page_id,omitempty"`
	Workspace    bool   `json:"workspace,omitempty"`
}

// Page is the envelope returned by /v1/pages endpoints. Properties is left as
// a loosely-typed map for v1 so callers can round-trip arbitrary Notion
// property shapes without losing fields. A typed property surface can land in
// a follow-up.
//
// InTrash mirrors Notion's 2026-03-11 field rename from `archived` to
// `in_trash`. The Archive/Unarchive methods on PageClient still flip this
// flag; only the wire-format key changed.
type Page struct {
	Object         string                 `json:"object"`
	ID             string                 `json:"id"`
	CreatedTime    string                 `json:"created_time"`
	LastEditedTime string                 `json:"last_edited_time"`
	InTrash        bool                   `json:"in_trash"`
	URL            string                 `json:"url"`
	Parent         PageParent             `json:"parent"`
	Properties     map[string]interface{} `json:"properties"`
}

// CreatePageRequest is the body for POST /v1/pages. Parent is required. Title
// is a convenience — when non-empty it is folded into a minimal title
// property. Properties lets callers send additional property values for
// database-parented pages (e.g. {"Status": {"status": {"name": "Done"}}}).
// Children, when non-nil, seeds the new page with block content.
type CreatePageRequest struct {
	Parent     PageParent               `json:"parent"`
	Title      string                   `json:"-"`
	Properties map[string]interface{}   `json:"properties,omitempty"`
	Children   []map[string]interface{} `json:"children,omitempty"`
}

// UpdatePageRequest is the body for PATCH /v1/pages/{id}. All fields are
// optional: unset fields are not sent. InTrash is a *bool so callers can
// distinguish "leave alone" from "explicitly restore from trash".
//
// The JSON key is `in_trash` per Notion-Version 2026-03-11, which renamed
// the prior `archived` field across all request parameters and response
// bodies.
type UpdatePageRequest struct {
	Title      string                 `json:"-"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	InTrash    *bool                  `json:"in_trash,omitempty"`
	Parent     *PageParent            `json:"parent,omitempty"`
}

// titleProperty returns the minimal Notion title property payload for the
// given plain-text title — the inner `{"title": [{...}]}` shape that goes
// under whatever property key a database has named its title column.
//
// For page parents the column is always literally "title". For database
// rows the column is whatever the schema named it (commonly "Name", but
// users routinely rename to "Project", "Client Name", etc.). Callers
// determine the right key via probeDatabaseTitlePropertyKey before
// folding this payload into the request body. See issue #60.
func titleProperty(title string) map[string]interface{} {
	return map[string]interface{}{
		"title": []map[string]interface{}{
			{
				"type": "text",
				"text": map[string]interface{}{"content": title},
			},
		},
	}
}

// findPageTitleText walks a page's loose Properties map looking for the
// entry whose Notion type is "title" (the property is unique per page —
// there is exactly one). Returns the concatenated plain_text of every
// rich-text run, so titles split across multiple runs (mentions, mixed
// formatting, links) round-trip in full. Returns ("", false) when no
// title property is present or every run is empty.
//
// Closes #60 (read-path half) and #65 (multi-run truncation) — both
// previously returned only the first non-empty run, dropping anything
// after the first segment.
//
// The helper does NOT key off the property NAME (e.g. "title", "Name",
// "Project"). It walks every entry and matches on the `type` field, so
// renamed title columns work out of the box.
func findPageTitleText(props map[string]interface{}) (string, bool) {
	for _, v := range props {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		// The schema property shape carries `"type": "title"` in both
		// pages (where the value also has a `title: [...]` rich-text
		// array) and database schemas (where `title: {}` is empty).
		// On a page we want the array; on a schema we don't reach
		// here. The presence of `[]interface{}` under `title` is the
		// strict gate.
		items, ok := m["title"].([]interface{})
		if !ok {
			continue
		}
		var sb strings.Builder
		for _, item := range items {
			run, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if pt, ok := run["plain_text"].(string); ok {
				sb.WriteString(pt)
			}
		}
		out := sb.String()
		return out, out != ""
	}
	return "", false
}

// findSchemaTitlePropertyKey scans a database (or data_source) schema
// for the property whose `type` is "title" and returns the property's
// key. Notion guarantees exactly one title property per schema; the
// key may be anything the user named the column ("Name", "Project",
// "Client Name", etc.).
//
// Used by Create/Update when the parent is a database to determine
// what key the --title shortcut should serialise under. Returns ""
// when no title property is found, leaving the caller to fall back to
// the literal "title" key (preserves the legacy behaviour for callers
// that pass a raw page id as parent).
func findSchemaTitlePropertyKey(props map[string]interface{}) string {
	for key, v := range props {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == "title" {
			return key
		}
	}
	return ""
}

// Get retrieves a page by ID via GET /v1/pages/{id}.
func (p *PageClient) Get(ctx context.Context, id string) (*Page, error) {
	page, _, err := p.GetRaw(ctx, id)
	return page, err
}

// GetRaw is Get that also returns the undecoded response body. The typed
// Page models only the fields the CLI's human output needs, so `fetch
// --json` emits these bytes instead — otherwise icon, cover, created_by,
// last_edited_by and any newer top-level key are silently dropped on the
// way out (issue #80).
func (p *PageClient) GetRaw(ctx context.Context, id string) (*Page, json.RawMessage, error) {
	if err := p.checkAuth(); err != nil {
		return nil, nil, fmt.Errorf("get page: %w", err)
	}
	if id == "" {
		return nil, nil, fmt.Errorf("get page: id is required")
	}
	req, err := p.c.newRequest(ctx, http.MethodGet, "/pages/"+id, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("get page: %w", err)
	}
	var page Page
	raw, err := decodeIntoRaw(resp, &page)
	if err != nil {
		return nil, nil, fmt.Errorf("get page: %w", err)
	}
	return &page, raw, nil
}

// Create posts a new page to POST /v1/pages. The parent must be set.
//
// When the parent is a database/data_source ID and req.Title is set,
// Create probes the schema once via DatabaseClient.Get to learn two
// things: (a) the actual surface type (database vs data_source on
// Notion-Version 2026-03-11) so we can pick the right parent
// discriminator, and (b) the title-property key (renamed columns like
// "Name" or "Client Name" are common). Both come from the same GET so
// the probe cost is bounded to one round-trip per database-parented
// create that uses --title.
//
// If the caller already supplied a title-typed property in
// req.Properties we honour that and skip the probe. Callers who set
// Parent.DataSourceID directly also skip the probe (already know the
// surface type).
//
// See issues #48 (data_source endpoint dispatch) and #60 (title
// property naming) for the underlying bugs.
func (p *PageClient) Create(ctx context.Context, req CreatePageRequest) (*Page, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	if req.Parent.DatabaseID == "" && req.Parent.DataSourceID == "" && req.Parent.PageID == "" && !req.Parent.Workspace {
		return nil, fmt.Errorf("create page: parent is required")
	}

	parent := req.Parent
	titleKey := "title"
	wantTitle := req.Title != "" && !propertiesContainTitle(req.Properties)

	// Probe only when the caller passed DatabaseID and we need either
	// the title key or surface-type discrimination. Skip when caller
	// pre-resolved by setting DataSourceID, page parents, or no title.
	if req.Parent.DatabaseID != "" && req.Parent.DataSourceID == "" && (wantTitle || true) {
		// We always probe on database parents — even without --title,
		// 2026-03-11 may need the discriminator swap for the request
		// to land. Cost is one extra GET per database-parented create.
		if probed, err := NewDatabaseClient(p.c).Get(ctx, req.Parent.DatabaseID); err == nil && probed != nil {
			if probed.Object == "data_source" {
				parent = PageParent{DataSourceID: req.Parent.DatabaseID}
			}
			if k := findSchemaTitlePropertyKey(probed.Properties); k != "" {
				titleKey = k
			}
		}
		// Probe failure (auth, transport, or no schema match) leaves
		// parent and titleKey at their defaults — worst case is the
		// pre-#60 behaviour, an opaque 400 from Notion.
	}

	body := map[string]interface{}{"parent": parent}
	props := map[string]interface{}{}
	for k, v := range req.Properties {
		props[k] = v
	}
	if wantTitle {
		props[titleKey] = titleProperty(req.Title)
	}
	if len(props) > 0 {
		body["properties"] = props
	}
	if len(req.Children) > 0 {
		body["children"] = req.Children
	}
	httpReq, err := p.c.newRequest(ctx, http.MethodPost, "/pages", body)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	var page Page
	if err := decodeInto(resp, &page); err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	return &page, nil
}

// resolveTitleKeyForExistingPage returns the key of the title-typed
// property on an existing page, by GET-ing the page once and walking
// its properties for the entry whose Notion type is "title". On any
// failure (missing page, network error, no title property) it falls
// back to literal "title" — the worst case is the pre-#60 behaviour.
//
// Used by Update when the caller passes --title without pre-resolving
// the property key. The probe is bounded to one GET per Update call
// that actually uses --title.
func (p *PageClient) resolveTitleKeyForExistingPage(ctx context.Context, id string) string {
	page, err := p.Get(ctx, id)
	if err != nil || page == nil {
		return "title"
	}
	for key, v := range page.Properties {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == "title" {
			return key
		}
	}
	return "title"
}

// propertiesContainTitle reports whether the caller already injected a
// title-typed property under any key. Used by Create/Update to skip
// the probe + auto-key when the user pre-resolved the title via the
// typed --property surface (PR #53).
func propertiesContainTitle(props map[string]interface{}) bool {
	for _, v := range props {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasArray := m["title"].([]map[string]interface{}); hasArray {
			return true
		}
		if _, hasArray := m["title"].([]interface{}); hasArray {
			return true
		}
	}
	return false
}

// Update patches a page via PATCH /v1/pages/{id}. All fields on the
// request are optional. Title, when non-empty, becomes a title property
// under the page's actual title-property key — for page-parented pages
// that's "title"; for database rows it's whatever the database schema
// named the title column ("Name", "Project", "Client Name", etc.).
//
// Resolving the right key needs one GET /v1/pages/{id} (only when
// --title is set; updates without --title still issue a single PATCH).
// We read the existing properties and find the one whose type is
// "title"; this avoids the heavier GET-page-then-GET-database probe
// the original #60 design considered. The probe failure mode falls
// back to literal "title" so the worst case is the pre-#60 behaviour.
//
// Caller-supplied req.Properties that already contain a title-typed
// entry skip the probe — power users can pre-resolve via PR #53's
// typed --property "Name=title:..." surface.
func (p *PageClient) Update(ctx context.Context, id string, req UpdatePageRequest) (*Page, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("update page: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("update page: id is required")
	}
	body := map[string]interface{}{}
	props := map[string]interface{}{}
	for k, v := range req.Properties {
		props[k] = v
	}
	if req.Title != "" && !propertiesContainTitle(props) {
		key := p.resolveTitleKeyForExistingPage(ctx, id)
		props[key] = titleProperty(req.Title)
	}
	if len(props) > 0 {
		body["properties"] = props
	}
	if req.InTrash != nil {
		body["in_trash"] = *req.InTrash
	}
	if req.Parent != nil {
		body["parent"] = *req.Parent
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("update page: no fields to update")
	}
	httpReq, err := p.c.newRequest(ctx, http.MethodPatch, "/pages/"+id, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("update page: %w", err)
	}
	var page Page
	if err := decodeInto(resp, &page); err != nil {
		return nil, fmt.Errorf("update page: %w", err)
	}
	return &page, nil
}

// setInTrash issues the in_trash PATCH for Archive/Unarchive. The wire
// format key is `in_trash` on Notion-Version 2026-03-11; the method names
// remain Archive/Unarchive to preserve the CLI verbs familiar to callers.
func (p *PageClient) setInTrash(ctx context.Context, id string, inTrash bool) error {
	if err := p.checkAuth(); err != nil {
		return fmt.Errorf("set in_trash: %w", err)
	}
	if id == "" {
		return fmt.Errorf("set in_trash: id is required")
	}
	body := map[string]interface{}{"in_trash": inTrash}
	req, err := p.c.newRequest(ctx, http.MethodPatch, "/pages/"+id, body)
	if err != nil {
		return err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return fmt.Errorf("set in_trash: %w", err)
	}
	return expectStatus(resp, http.StatusOK)
}

// Archive moves the page to the trash by setting in_trash=true.
func (p *PageClient) Archive(ctx context.Context, id string) error {
	return p.setInTrash(ctx, id, true)
}

// Unarchive restores the page from the trash by setting in_trash=false.
func (p *PageClient) Unarchive(ctx context.Context, id string) error {
	return p.setInTrash(ctx, id, false)
}

// Move reparents a page via PATCH /v1/pages/{id} with a parent update. The
// newParentID is treated as a page_id parent; to move into a database parent
// the caller should use Update with a PageParent{DatabaseID: ...}.
func (p *PageClient) Move(ctx context.Context, id, newParentID string) error {
	if err := p.checkAuth(); err != nil {
		return fmt.Errorf("move page: %w", err)
	}
	if id == "" {
		return fmt.Errorf("move page: id is required")
	}
	if newParentID == "" {
		return fmt.Errorf("move page: new parent id is required")
	}
	body := map[string]interface{}{
		"parent": PageParent{PageID: newParentID},
	}
	req, err := p.c.newRequest(ctx, http.MethodPatch, "/pages/"+id, body)
	if err != nil {
		return err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return fmt.Errorf("move page: %w", err)
	}
	return expectStatus(resp, http.StatusOK)
}

// Duplicate emulates a page copy: GET the source's children, POST a new page
// under parentID, then PATCH the source's children onto the new page. Notion
// has no native duplicate endpoint.
//
// Limitations (v1):
//   - Does NOT recurse into nested databases or blocks where has_children is
//     true. Top-level children only.
//   - Properties from a database-parented source page are NOT carried over;
//     the new page is created with a minimal title only. Callers that need
//     property parity should use Get + Create with the returned Properties.
//   - The source's title, when retrievable, is used for the new page;
//     otherwise "Copy" is used.
func (p *PageClient) Duplicate(ctx context.Context, srcID, parentID string) (*Page, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("duplicate page: %w", err)
	}
	if srcID == "" {
		return nil, fmt.Errorf("duplicate page: source id is required")
	}
	if parentID == "" {
		return nil, fmt.Errorf("duplicate page: parent id is required")
	}

	// Fetch the source page first and surface any error (including 404)
	// BEFORE creating the destination. Previous revisions swallowed this
	// error and happily created an empty "Copy" page for a bogus srcID.
	src, err := p.Get(ctx, srcID)
	if err != nil {
		return nil, fmt.Errorf("duplicate page: fetch source: %w", err)
	}

	bc := NewBlockClient(p.c)
	blocks, err := bc.GetAllBlocks(ctx, srcID, "")
	if err != nil {
		return nil, fmt.Errorf("duplicate page: fetch children: %w", err)
	}

	title := "Copy"
	if t := extractTitle(src); t != "" {
		title = t
	}

	newPage, err := p.Create(ctx, CreatePageRequest{
		Parent: PageParent{PageID: parentID},
		Title:  title,
	})
	if err != nil {
		return nil, fmt.Errorf("duplicate page: create: %w", err)
	}

	if len(blocks) == 0 {
		return newPage, nil
	}

	children := blocksToChildren(blocks)
	// Filter pass: blocksToChildren can return an empty slice when every
	// source block is a type rebuildBlock drops (image/file/child_database
	// today). Without this guard the next step PATCHes /blocks/{id}/children
	// with `children: []`, which Notion rejects — the destination page is
	// already created at that point, leaving an empty orphan. Closes the
	// edge case from #54. Treat empty-after-filter the same as zero source
	// blocks: hand back the new page with just the title.
	if len(children) == 0 {
		return newPage, nil
	}
	body := map[string]interface{}{"children": children}
	req, err := p.c.newRequest(ctx, http.MethodPatch, "/blocks/"+newPage.ID+"/children", body)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("duplicate page: append children: %w", err)
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return nil, fmt.Errorf("duplicate page: append children: %w", err)
	}
	return newPage, nil
}

// extractTitle returns the plain-text title of a page when it can be
// found in the loose property map. Returns "" if no title property is
// present. Concatenates every rich-text run so titles split across
// multiple runs (mentions, mixed formatting) round-trip in full.
func extractTitle(page *Page) string {
	if page == nil {
		return ""
	}
	out, _ := findPageTitleText(page.Properties)
	return out
}

// blocksToChildren rebuilds the minimal "children" payload for PATCH
// /blocks/{id}/children from a slice of Block. Only top-level, supported
// block types are rewritten; unsupported types are skipped so the PATCH does
// not 400 on an unknown shape.
func blocksToChildren(blocks []Block) []map[string]interface{} {
	children := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		child := rebuildBlock(b)
		if child == nil {
			continue
		}
		children = append(children, child)
	}
	return children
}

// rebuildBlock reconstructs the create-children payload for a single block.
// Returns nil for block types not supported by the v1 create-children shape
// we emit here (e.g. child_database, file, image).
func rebuildBlock(b Block) map[string]interface{} {
	switch b.Type {
	case "divider":
		return map[string]interface{}{
			"object":  "block",
			"type":    "divider",
			"divider": map[string]interface{}{},
		}
	case "to_do":
		if b.ToDo == nil {
			return nil
		}
		extra := map[string]interface{}{
			"checked": b.ToDo.Checked,
		}
		if b.ToDo.Color != "" && b.ToDo.Color != "default" {
			extra["color"] = b.ToDo.Color
		}
		return richTextChild("to_do", extra, b.ToDo.RichText)
	case "paragraph":
		return richTextFromBlock("paragraph", b.Paragraph)
	case "heading_1":
		return richTextFromBlock("heading_1", b.Heading1)
	case "heading_2":
		return richTextFromBlock("heading_2", b.Heading2)
	case "heading_3":
		return richTextFromBlock("heading_3", b.Heading3)
	case "bulleted_list_item":
		return richTextFromBlock("bulleted_list_item", b.BulletedListItem)
	case "numbered_list_item":
		return richTextFromBlock("numbered_list_item", b.NumberedListItem)
	case "toggle":
		return richTextFromBlock("toggle", b.Toggle)
	case "quote":
		return richTextFromBlock("quote", b.Quote)
	case "callout":
		return richTextFromBlock("callout", b.Callout)
	case "code":
		if b.Code == nil {
			return nil
		}
		extra := map[string]interface{}{}
		if b.Code.Language != "" {
			extra["language"] = b.Code.Language
		} else {
			extra["language"] = "plain text"
		}
		if b.Code.Color != "" && b.Code.Color != "default" {
			extra["color"] = b.Code.Color
		}
		return richTextChild("code", extra, b.Code.RichText)
	}
	return nil
}

// richTextFromBlock rebuilds the children-create payload for a block
// whose body is a RichTextBlock (paragraph, heading_1/2/3, list items,
// toggle, quote, callout). Forwards Color so duplicating a coloured
// paragraph/heading/etc. preserves the colour rather than resetting to
// default — closes #84.
//
// "default" is treated as no color so we don't bake a redundant field
// into the payload (Notion's default when the field is omitted is
// "default" anyway).
func richTextFromBlock(typ string, rtb *RichTextBlock) map[string]interface{} {
	if rtb == nil {
		return nil
	}
	var extra map[string]interface{}
	if rtb.Color != "" && rtb.Color != "default" {
		extra = map[string]interface{}{"color": rtb.Color}
	}
	return richTextChild(typ, extra, rtb.RichText)
}

func richTextChild(typ string, extra map[string]interface{}, runs []RichText) map[string]interface{} {
	inner := map[string]interface{}{
		"rich_text": richTextPayload(runs),
	}
	for k, v := range extra {
		inner[k] = v
	}
	return map[string]interface{}{
		"object": "block",
		"type":   typ,
		typ:      inner,
	}
}

// richTextPayload rebuilds the rich_text array for a child block during
// duplicate. Routes through richTextToAPI so annotations (bold/italic/
// color/...), inline links, page/user/date mentions, and inline
// equations all round-trip — closes #61. Pre-fix this helper flattened
// every run to a plain `{type:"text", text:{content:...}}` payload,
// silently dropping every non-content field.
//
// We normalise Text.Content from PlainText first because the source
// page's rich_text may have only PlainText populated (Notion's read
// response sets both, but some constructors and the resolver-rendered
// output don't). richTextToAPI itself reads Text.Content; without
// this nudge, mentions and equations still preserve correctly but a
// plain-text run could land on the wire with an empty content string.
func richTextPayload(runs []RichText) []map[string]interface{} {
	normalized := make([]RichText, len(runs))
	for i, r := range runs {
		normalized[i] = r
		if r.Mention == nil && r.Equation == nil && r.Text.Content == "" {
			normalized[i].Text.Content = r.PlainText
		}
	}
	return richTextToAPI(normalized)
}
