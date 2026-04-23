// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// PageParent describes where a page lives. Notion accepts exactly one of
// database_id, page_id, or workspace=true. The zero value is invalid; callers
// constructing a CreatePageRequest must populate one of the three identifiers.
type PageParent struct {
	Type       string `json:"type,omitempty"`
	DatabaseID string `json:"database_id,omitempty"`
	PageID     string `json:"page_id,omitempty"`
	Workspace  bool   `json:"workspace,omitempty"`
}

// Page is the envelope returned by /v1/pages endpoints. Properties is left as
// a loosely-typed map for v1 so callers can round-trip arbitrary Notion
// property shapes without losing fields. A typed property surface can land in
// a follow-up.
type Page struct {
	Object         string                 `json:"object"`
	ID             string                 `json:"id"`
	CreatedTime    string                 `json:"created_time"`
	LastEditedTime string                 `json:"last_edited_time"`
	Archived       bool                   `json:"archived"`
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
// optional: unset fields are not sent. Archived is a *bool so callers can
// distinguish "leave alone" from "explicitly unarchive".
type UpdatePageRequest struct {
	Title      string                 `json:"-"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Archived   *bool                  `json:"archived,omitempty"`
	Parent     *PageParent            `json:"parent,omitempty"`
}

// titleProperty returns the minimal Notion title property payload for the
// given plain-text title.
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

// Get retrieves a page by ID via GET /v1/pages/{id}.
func (p *PageClient) Get(ctx context.Context, id string) (*Page, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("get page: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("get page: id is required")
	}
	req, err := p.c.newRequest(ctx, http.MethodGet, "/pages/"+id, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get page: %w", err)
	}
	var page Page
	if err := decodeInto(resp, &page); err != nil {
		return nil, fmt.Errorf("get page: %w", err)
	}
	return &page, nil
}

// Create posts a new page to POST /v1/pages. The parent must be set. If
// req.Title is non-empty it is merged into Properties under the "title" key,
// overwriting any title the caller already supplied there.
func (p *PageClient) Create(ctx context.Context, req CreatePageRequest) (*Page, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	if req.Parent.DatabaseID == "" && req.Parent.PageID == "" && !req.Parent.Workspace {
		return nil, fmt.Errorf("create page: parent is required")
	}
	body := map[string]interface{}{
		"parent": req.Parent,
	}
	props := map[string]interface{}{}
	for k, v := range req.Properties {
		props[k] = v
	}
	if req.Title != "" {
		props["title"] = titleProperty(req.Title)
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

// Update patches a page via PATCH /v1/pages/{id}. All fields on the request
// are optional. Title, when non-empty, becomes a title property.
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
	if req.Title != "" {
		props["title"] = titleProperty(req.Title)
	}
	if len(props) > 0 {
		body["properties"] = props
	}
	if req.Archived != nil {
		body["archived"] = *req.Archived
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

// setArchived issues the archived PATCH for Archive/Unarchive.
func (p *PageClient) setArchived(ctx context.Context, id string, archived bool) error {
	if err := p.checkAuth(); err != nil {
		return fmt.Errorf("set archived: %w", err)
	}
	if id == "" {
		return fmt.Errorf("set archived: id is required")
	}
	body := map[string]interface{}{"archived": archived}
	req, err := p.c.newRequest(ctx, http.MethodPatch, "/pages/"+id, body)
	if err != nil {
		return err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return fmt.Errorf("set archived: %w", err)
	}
	return expectStatus(resp, http.StatusOK)
}

// Archive sets archived=true on the page.
func (p *PageClient) Archive(ctx context.Context, id string) error {
	return p.setArchived(ctx, id, true)
}

// Unarchive sets archived=false on the page.
func (p *PageClient) Unarchive(ctx context.Context, id string) error {
	return p.setArchived(ctx, id, false)
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

	bc := NewBlockClient(p.c)
	blocks, err := bc.GetAllBlocks(ctx, srcID, "")
	if err != nil {
		return nil, fmt.Errorf("duplicate page: fetch children: %w", err)
	}

	// Best-effort title lookup. A failure here is non-fatal — fall back to
	// "Copy" so Duplicate can still make progress against mocks and against
	// pages whose title is hidden from this integration.
	title := "Copy"
	if src, err := p.Get(ctx, srcID); err == nil {
		if t := extractTitle(src); t != "" {
			title = t
		}
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

// extractTitle returns the plain-text title of a page when it can be found in
// the loose property map. Returns "" if no title property is present.
func extractTitle(page *Page) string {
	if page == nil {
		return ""
	}
	for _, v := range page.Properties {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := m["title"].([]interface{})
		if !ok {
			continue
		}
		for _, item := range items {
			run, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if pt, ok := run["plain_text"].(string); ok && pt != "" {
				return pt
			}
		}
	}
	return ""
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
		return richTextChild("to_do", map[string]interface{}{
			"checked": b.ToDo.Checked,
		}, b.ToDo.RichText)
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
		return richTextChild("code", extra, b.Code.RichText)
	}
	return nil
}

func richTextFromBlock(typ string, rtb *RichTextBlock) map[string]interface{} {
	if rtb == nil {
		return nil
	}
	return richTextChild(typ, nil, rtb.RichText)
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

func richTextPayload(runs []RichText) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(runs))
	for _, r := range runs {
		content := r.Text.Content
		if content == "" {
			content = r.PlainText
		}
		out = append(out, map[string]interface{}{
			"type": "text",
			"text": map[string]interface{}{"content": content},
		})
	}
	return out
}
