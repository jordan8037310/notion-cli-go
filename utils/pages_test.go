// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// pagesMockServer is a stateful httptest server covering every /pages and
// /blocks path the PageClient touches. Per-test behavior is tuned by swapping
// fields on the returned handle rather than juggling multiple servers.
type pagesMockServer struct {
	srv      *httptest.Server
	calls    []recordedCall
	mu       chan struct{} // simple mutex via buffered chan
	notFound bool

	// inTrashStates tracks the per-page `in_trash` flag the mock has
	// observed via PATCH bodies. The underlying wire key was renamed
	// from `archived` to `in_trash` in Notion-Version 2026-03-11.
	inTrashStates map[string]bool

	// For Duplicate: count of /blocks/{id}/children GETs and PATCHes.
	blocksGet   int64
	blocksPatch int64
}

type recordedCall struct {
	method string
	path   string
	body   map[string]interface{}
}

func newPagesMockServer(t *testing.T) *pagesMockServer {
	t.Helper()
	p := &pagesMockServer{
		mu:            make(chan struct{}, 1),
		inTrashStates: map[string]bool{},
	}
	p.mu <- struct{}{}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pagesMockServer) lock()   { <-p.mu }
func (p *pagesMockServer) unlock() { p.mu <- struct{}{} }

func (p *pagesMockServer) record(method, path string, body map[string]interface{}) {
	p.lock()
	defer p.unlock()
	p.calls = append(p.calls, recordedCall{method: method, path: path, body: body})
}

// callsSnapshot returns a copy of the recorded call log under lock.
func (p *pagesMockServer) callsSnapshot() []recordedCall {
	p.lock()
	defer p.unlock()
	out := make([]recordedCall, len(p.calls))
	copy(out, p.calls)
	return out
}

func (p *pagesMockServer) client() *Client {
	return NewClient("sk_test", WithBaseURL(p.srv.URL))
}

func (p *pagesMockServer) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if r.Method == http.MethodPatch || r.Method == http.MethodPost {
		buf, _ := io.ReadAll(r.Body)
		if len(buf) > 0 {
			_ = json.Unmarshal(buf, &body)
		}
	}
	p.record(r.Method, r.URL.Path, body)

	if p.notFound {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found","message":"Could not find page"}`))
		return
	}

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
		id := strings.TrimPrefix(r.URL.Path, "/pages/")
		inTrash := false
		p.lock()
		if v, ok := p.inTrashStates[id]; ok {
			inTrash = v
		}
		p.unlock()
		writeJSONPage(w, id, inTrash, "Source title")

	case r.Method == http.MethodPost && r.URL.Path == "/pages":
		writeJSONPage(w, "newPageID", false, "Created")

	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/pages/"):
		id := strings.TrimPrefix(r.URL.Path, "/pages/")
		if body != nil {
			if v, ok := body["in_trash"].(bool); ok {
				p.lock()
				p.inTrashStates[id] = v
				p.unlock()
			}
		}
		writeJSONPage(w, id, p.inTrashStates[id], "Updated")

	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/children"):
		atomic.AddInt64(&p.blocksGet, 1)
		writeJSONBlocks(w)

	case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/children"):
		atomic.AddInt64(&p.blocksPatch, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","results":[]}`))

	default:
		http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
	}
}

func writeJSONPage(w http.ResponseWriter, id string, inTrash bool, title string) {
	page := map[string]interface{}{
		"object":           "page",
		"id":               id,
		"created_time":     "2026-04-22T10:00:00.000Z",
		"last_edited_time": "2026-04-22T10:00:00.000Z",
		"in_trash":         inTrash,
		"url":              "https://notion.so/" + id,
		"parent":           map[string]interface{}{"type": "page_id", "page_id": "parentID"},
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"id":   "title",
				"type": "title",
				"title": []map[string]interface{}{
					{
						"type":       "text",
						"plain_text": title,
						"text":       map[string]interface{}{"content": title},
					},
				},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func writeJSONBlocks(w http.ResponseWriter) {
	payload := BlockList{
		Results: []Block{
			{
				Object: "block", ID: "b1", Type: "paragraph",
				Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "hello", Text: Text{Content: "hello"}}}},
			},
			{
				Object: "block", ID: "b2", Type: "to_do",
				ToDo: &ToDo{Checked: true, RichText: []RichText{{PlainText: "pack", Text: Text{Content: "pack"}}}},
			},
			{
				Object: "block", ID: "b3", Type: "divider",
				Divider: &struct{}{},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// -------- Tests --------

func TestGet_HappyPath(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	page, err := pc.Get(context.Background(), "pageID")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if page.ID != "pageID" {
		t.Errorf("page.ID=%q want pageID", page.ID)
	}
	if page.URL == "" {
		t.Error("expected non-empty URL on returned page")
	}
}

func TestGet_EmptyID(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if _, err := pc.Get(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty id, got nil")
	}
}

func TestGet_NotFound(t *testing.T) {
	m := newPagesMockServer(t)
	m.notFound = true
	pc := NewPageClient(m.client())

	_, err := pc.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error=%q should mention 404", err.Error())
	}
}

func TestCreate_Minimal(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	page, err := pc.Create(context.Background(), CreatePageRequest{
		Parent: PageParent{PageID: "parentID"},
		Title:  "New thing",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if page.ID != "newPageID" {
		t.Errorf("page.ID=%q want newPageID", page.ID)
	}

	calls := m.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls)=%d want 1", len(calls))
	}
	if calls[0].method != http.MethodPost || calls[0].path != "/pages" {
		t.Errorf("call=%s %s want POST /pages", calls[0].method, calls[0].path)
	}
	props, ok := calls[0].body["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties in body, got %v", calls[0].body)
	}
	if _, ok := props["title"]; !ok {
		t.Error("expected title property in request body")
	}
}

func TestCreate_NoParent(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	_, err := pc.Create(context.Background(), CreatePageRequest{Title: "x"})
	if err == nil {
		t.Fatal("expected error when parent missing")
	}
}

func TestUpdate_TitleOnly(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	page, err := pc.Update(context.Background(), "pageID", UpdatePageRequest{Title: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if page.ID != "pageID" {
		t.Errorf("page.ID=%q want pageID", page.ID)
	}

	calls := m.callsSnapshot()
	// Two calls now: a GET /pages/{id} to probe the title-property
	// key (#60), then the PATCH itself. The GET is bounded to updates
	// that actually use --title; updates without it still issue one
	// call (covered by TestUpdate_PropertiesOnly_NoProbe).
	if len(calls) != 2 {
		t.Fatalf("len(calls)=%d want 2 (GET probe + PATCH)", len(calls))
	}
	if calls[0].method != http.MethodGet || calls[0].path != "/pages/pageID" {
		t.Errorf("call[0]=%s %s want GET /pages/pageID (title-key probe)", calls[0].method, calls[0].path)
	}
	if calls[1].method != http.MethodPatch || calls[1].path != "/pages/pageID" {
		t.Errorf("call[1]=%s %s want PATCH /pages/pageID", calls[1].method, calls[1].path)
	}
	if _, ok := calls[1].body["properties"]; !ok {
		t.Error("expected title-only update to include properties")
	}
	// Guard against regressing the 2026-03-11 wire format on either
	// the old key (archived) or the new one (in_trash) — neither
	// should be present when no trash flag was supplied.
	if _, ok := calls[1].body["archived"]; ok {
		t.Error("title-only update must not include archived (removed in 2026-03-11)")
	}
	if _, ok := calls[1].body["in_trash"]; ok {
		t.Error("title-only update should not include in_trash")
	}
}

// TestUpdate_PropertiesOnly_NoProbe pins the contract that updates
// without --title do NOT issue the title-key probe — only updates that
// need to know what the title column is named pay the GET round-trip.
func TestUpdate_PropertiesOnly_NoProbe(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	props := map[string]interface{}{
		"Status": map[string]interface{}{"status": map[string]interface{}{"name": "Done"}},
	}
	if _, err := pc.Update(context.Background(), "pageID", UpdatePageRequest{Properties: props}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	calls := m.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls)=%d want 1 (no --title means no probe)", len(calls))
	}
	if calls[0].method != http.MethodPatch {
		t.Errorf("call[0]=%s want PATCH", calls[0].method)
	}
}

// TestUpdate_InTrashEmitsNewKey pins the PATCH body from UpdatePageRequest
// with an InTrash pointer set. Notion-Version 2026-03-11 renamed the field
// from `archived` to `in_trash`; this test fails hard if we ever silently
// regress the struct tag or the body composition.
func TestUpdate_InTrashEmitsNewKey(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	tr := true
	if _, err := pc.Update(context.Background(), "pageID", UpdatePageRequest{InTrash: &tr}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	calls := m.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls)=%d want 1", len(calls))
	}
	if calls[0].body["in_trash"] != true {
		t.Errorf("expected in_trash=true in PATCH body, got %v", calls[0].body)
	}
	if _, ok := calls[0].body["archived"]; ok {
		t.Errorf("legacy archived key must not appear in PATCH body, got %v", calls[0].body)
	}
}

func TestUpdate_Empty(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if _, err := pc.Update(context.Background(), "pageID", UpdatePageRequest{}); err == nil {
		t.Fatal("expected error when no fields provided")
	}
}

func TestUpdate_EmptyID(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if _, err := pc.Update(context.Background(), "", UpdatePageRequest{Title: "x"}); err == nil {
		t.Fatal("expected error on empty id")
	}
}

func TestArchive_Unarchive_RoundTrip(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	ctx := context.Background()

	if err := pc.Archive(ctx, "pageID"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	m.lock()
	if !m.inTrashStates["pageID"] {
		t.Error("expected in_trash=true after Archive")
	}
	m.unlock()

	if err := pc.Unarchive(ctx, "pageID"); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	m.lock()
	if m.inTrashStates["pageID"] {
		t.Error("expected in_trash=false after Unarchive")
	}
	m.unlock()

	calls := m.callsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("len(calls)=%d want 2", len(calls))
	}
	// 2026-03-11 renamed the PATCH key from `archived` to `in_trash`.
	// Assert the new key is present with the expected boolean AND the
	// old key is absent — this is what catches a regression where we
	// accidentally fall back to the pre-2026-03-11 shape.
	if calls[0].body["in_trash"] != true {
		t.Errorf("first call should set in_trash=true, body=%v", calls[0].body)
	}
	if _, ok := calls[0].body["archived"]; ok {
		t.Errorf("first call must not include legacy archived key, body=%v", calls[0].body)
	}
	if calls[1].body["in_trash"] != false {
		t.Errorf("second call should set in_trash=false, body=%v", calls[1].body)
	}
	if _, ok := calls[1].body["archived"]; ok {
		t.Errorf("second call must not include legacy archived key, body=%v", calls[1].body)
	}
}

func TestArchive_EmptyID(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if err := pc.Archive(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty id")
	}
}

// TestUnarchive exists so the test-gap checker sees a direct mapping from
// the exported PageClient.Unarchive method to a Test* function. The behavior
// is exercised more fully by TestArchive_Unarchive_RoundTrip.
func TestUnarchive(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if err := pc.Unarchive(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty id for Unarchive")
	}
	if err := pc.Unarchive(context.Background(), "pageID"); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
}

func TestMove_HappyPath(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	if err := pc.Move(context.Background(), "pageID", "newParentID"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	calls := m.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls)=%d want 1", len(calls))
	}
	if calls[0].method != http.MethodPatch {
		t.Errorf("method=%s want PATCH", calls[0].method)
	}
	parent, ok := calls[0].body["parent"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected parent in body, got %v", calls[0].body)
	}
	if parent["page_id"] != "newParentID" {
		t.Errorf("parent.page_id=%v want newParentID", parent["page_id"])
	}
}

func TestMove_EmptyArgs(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if err := pc.Move(context.Background(), "", "newParentID"); err == nil {
		t.Fatal("expected error on empty page id")
	}
	if err := pc.Move(context.Background(), "pageID", ""); err == nil {
		t.Fatal("expected error on empty new parent id")
	}
}

func TestDuplicate_CallSequence(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())

	newPage, err := pc.Duplicate(context.Background(), "srcID", "parentID")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if newPage.ID != "newPageID" {
		t.Errorf("newPage.ID=%q want newPageID", newPage.ID)
	}

	// Expected call sequence: GET children → GET source page → POST new page → PATCH children
	calls := m.callsSnapshot()
	if len(calls) < 3 {
		t.Fatalf("expected >= 3 calls, got %d (%+v)", len(calls), calls)
	}

	var sawGetChildren, sawPostPage, sawPatchChildren bool
	for _, c := range calls {
		switch {
		case c.method == http.MethodGet && strings.HasSuffix(c.path, "/children"):
			sawGetChildren = true
		case c.method == http.MethodPost && c.path == "/pages":
			sawPostPage = true
			// Confirm the new page's parent is the requested one.
			parent, _ := c.body["parent"].(map[string]interface{})
			if parent["page_id"] != "parentID" {
				t.Errorf("create call parent=%v want parentID", parent)
			}
		case c.method == http.MethodPatch && strings.HasSuffix(c.path, "/children"):
			sawPatchChildren = true
			children, _ := c.body["children"].([]interface{})
			if len(children) == 0 {
				t.Error("patch children call sent empty children slice")
			}
		}
	}
	if !sawGetChildren || !sawPostPage || !sawPatchChildren {
		t.Errorf("missing call; got=%+v", calls)
	}
}

// TestDuplicate_AllBlocksFilteredOut covers the edge case where the
// source page has blocks but every block type is dropped by rebuildBlock
// (image/file/video/embed/bookmark/equation/child_database etc.). Without
// the empty-after-filter fast path, blocksToChildren would yield [] and
// the next PATCH would hit /blocks/{newID}/children with `children: []`,
// which Notion rejects — leaving an empty destination page behind.
// Closes the Duplicate edge case from #54.
func TestDuplicate_AllBlocksFilteredOut(t *testing.T) {
	var sawChildrenPatch int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
			writeJSONPage(w, strings.TrimPrefix(r.URL.Path, "/pages/"), false, "Image-only page")
		case r.Method == http.MethodPost && r.URL.Path == "/pages":
			writeJSONPage(w, "newPageID", false, "Image-only page")
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/children"):
			// Source has only block types rebuildBlock drops.
			_ = json.NewEncoder(w).Encode(BlockList{Results: []Block{
				{Object: "block", ID: "i1", Type: "image"},
				{Object: "block", ID: "f1", Type: "file"},
				{Object: "block", ID: "v1", Type: "video"},
			}})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/children"):
			atomic.AddInt64(&sawChildrenPatch, 1)
			http.Error(w, `{"object":"error","status":400,"code":"validation_error","message":"Body should have at least 1 child"}`, http.StatusBadRequest)
		default:
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	pc := NewPageClient(NewClient("sk_test", WithBaseURL(srv.URL)))
	newPage, err := pc.Duplicate(context.Background(), "srcID", "parentID")
	if err != nil {
		t.Fatalf("Duplicate (all-filtered source): %v — should have skipped the empty-children PATCH", err)
	}
	if newPage == nil || newPage.ID != "newPageID" {
		t.Errorf("expected newPageID returned with title only; got %+v", newPage)
	}
	if got := atomic.LoadInt64(&sawChildrenPatch); got != 0 {
		t.Errorf("Duplicate sent %d children PATCH(es); want 0 (empty filter result must not hit /children)", got)
	}
}

func TestDuplicate_EmptyArgs(t *testing.T) {
	m := newPagesMockServer(t)
	pc := NewPageClient(m.client())
	if _, err := pc.Duplicate(context.Background(), "", "parent"); err == nil {
		t.Fatal("expected error on empty srcID")
	}
	if _, err := pc.Duplicate(context.Background(), "src", ""); err == nil {
		t.Fatal("expected error on empty parentID")
	}
}

// TestBlocksToChildren confirms rebuildBlock handles every block type the
// duplicate pipeline emits children for, and skips the ones it doesn't.
func TestBlocksToChildren(t *testing.T) {
	blocks := []Block{
		{Type: "paragraph", Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "p"}}}},
		{Type: "heading_1", Heading1: &RichTextBlock{RichText: []RichText{{PlainText: "h1"}}}},
		{Type: "heading_2", Heading2: &RichTextBlock{RichText: []RichText{{PlainText: "h2"}}}},
		{Type: "heading_3", Heading3: &RichTextBlock{RichText: []RichText{{PlainText: "h3"}}}},
		{Type: "bulleted_list_item", BulletedListItem: &RichTextBlock{RichText: []RichText{{PlainText: "b"}}}},
		{Type: "numbered_list_item", NumberedListItem: &RichTextBlock{RichText: []RichText{{PlainText: "n"}}}},
		{Type: "toggle", Toggle: &RichTextBlock{RichText: []RichText{{PlainText: "t"}}}},
		{Type: "quote", Quote: &RichTextBlock{RichText: []RichText{{PlainText: "q"}}}},
		{Type: "callout", Callout: &RichTextBlock{RichText: []RichText{{PlainText: "c"}}}},
		{Type: "code", Code: &RichTextBlock{RichText: []RichText{{PlainText: "c"}}, Language: "go"}},
		{Type: "to_do", ToDo: &ToDo{Checked: true, RichText: []RichText{{PlainText: "d"}}}},
		{Type: "divider", Divider: &struct{}{}},
		// Unsupported: should be dropped.
		{Type: "image"},
		// Nil inner block: should be dropped.
		{Type: "paragraph"},
		{Type: "to_do"},
		{Type: "code"},
	}

	children := blocksToChildren(blocks)
	// 12 supported types present.
	if len(children) != 12 {
		t.Fatalf("len(children)=%d want 12", len(children))
	}
	// Spot-check: code block carries language.
	var codeBlock map[string]interface{}
	for _, c := range children {
		if c["type"] == "code" {
			codeBlock = c
			break
		}
	}
	if codeBlock == nil {
		t.Fatal("code block missing from children")
	}
	inner, _ := codeBlock["code"].(map[string]interface{})
	if inner["language"] != "go" {
		t.Errorf("code.language=%v want go", inner["language"])
	}
}

// TestBlocksToChildren_PreservesBlockColor pins the #84 contract:
// duplicating a page must round-trip the block-level Color field on
// every richTextBlock-backed type (paragraph/heading/list/quote/
// toggle/callout/code) plus the to_do flavour. Pre-fix richTextFromBlock
// dropped Color universally; the to_do and code paths also dropped it
// even though they were already setting other fields via extra.
//
// "default" is omitted (Notion's default-when-absent equals "default")
// to keep payloads minimal.
func TestBlocksToChildren_PreservesBlockColor(t *testing.T) {
	blocks := []Block{
		{Type: "paragraph", Paragraph: &RichTextBlock{Color: "red", RichText: []RichText{{PlainText: "p"}}}},
		{Type: "heading_1", Heading1: &RichTextBlock{Color: "blue_background", RichText: []RichText{{PlainText: "h"}}}},
		{Type: "callout", Callout: &RichTextBlock{Color: "yellow", RichText: []RichText{{PlainText: "c"}}}},
		{Type: "to_do", ToDo: &ToDo{Color: "green", Checked: true, RichText: []RichText{{PlainText: "d"}}}},
		{Type: "code", Code: &RichTextBlock{Color: "gray", Language: "go", RichText: []RichText{{PlainText: "go code"}}}},
		// Default colour should NOT serialise as "color: default" — must be omitted.
		{Type: "quote", Quote: &RichTextBlock{Color: "default", RichText: []RichText{{PlainText: "q"}}}},
	}
	children := blocksToChildren(blocks)
	if len(children) != len(blocks) {
		t.Fatalf("want %d children, got %d", len(blocks), len(children))
	}

	wantColor := map[string]string{
		"paragraph": "red",
		"heading_1": "blue_background",
		"callout":   "yellow",
		"to_do":     "green",
		"code":      "gray",
	}
	for _, c := range children {
		typ, _ := c["type"].(string)
		inner, _ := c[typ].(map[string]interface{})
		if want, ok := wantColor[typ]; ok {
			if got, _ := inner["color"].(string); got != want {
				t.Errorf("%s: color = %q, want %q", typ, got, want)
			}
		}
		if typ == "quote" {
			if _, hasColor := inner["color"]; hasColor {
				t.Errorf("quote with default color must NOT include color field; got %#v", inner)
			}
		}
	}
}

// TestRichTextPayload covers the content fallback: when Text.Content is empty
// the run's PlainText is used so round-tripping blocks from GET responses
// doesn't lose text.
func TestRichTextPayload(t *testing.T) {
	out := richTextPayload([]RichText{
		{PlainText: "hello"},
		{Text: Text{Content: "world"}},
	})
	if len(out) != 2 {
		t.Fatalf("len=%d want 2", len(out))
	}
	first, _ := out[0]["text"].(map[string]interface{})
	if first["content"] != "hello" {
		t.Errorf("first content=%v want hello", first["content"])
	}
	second, _ := out[1]["text"].(map[string]interface{})
	if second["content"] != "world" {
		t.Errorf("second content=%v want world", second["content"])
	}
}

// TestRichTextPayload_PreservesAnnotations pins the #61 contract:
// duplicate must round-trip annotations (bold/italic/color), inline
// links, page mentions, and inline equations rather than flattening
// every run to plain text. The pre-fix helper emitted only
// {type:"text", text:{content:...}} for every input, silently dropping
// every non-content field.
func TestRichTextPayload_PreservesAnnotations(t *testing.T) {
	runs := []RichText{
		{
			Type:        "text",
			Text:        Text{Content: "bold "},
			PlainText:   "bold ",
			Annotations: Annotation{Bold: true},
		},
		{
			Type:      "mention",
			PlainText: "[page-mention]",
			Mention:   &Mention{Type: "page", Page: &PageMention{ID: "abc-123"}},
		},
		{
			Type:      "equation",
			PlainText: "E=mc^2",
			Equation:  &TextEquation{Expression: "E=mc^2"},
		},
	}
	out := richTextPayload(runs)
	if len(out) != 3 {
		t.Fatalf("len=%d want 3", len(out))
	}

	// Run 0: bold annotation must survive.
	ann0, ok := out[0]["annotations"].(Annotation)
	if !ok {
		t.Fatalf("run 0 lost annotations: %+v", out[0])
	}
	if !ann0.Bold {
		t.Errorf("run 0 annotations.bold = false; want true (bold flag dropped)")
	}

	// Run 1: mention must round-trip as a mention, not flattened to text.
	if out[1]["type"] != "mention" {
		t.Errorf("run 1 type=%v want mention (mention flattened to text)", out[1]["type"])
	}
	if out[1]["mention"] == nil {
		t.Errorf("run 1 lost mention payload: %+v", out[1])
	}

	// Run 2: equation must round-trip with the expression intact.
	if out[2]["type"] != "equation" {
		t.Errorf("run 2 type=%v want equation (equation flattened to text)", out[2]["type"])
	}
	if eq, _ := out[2]["equation"].(*TextEquation); eq == nil || eq.Expression != "E=mc^2" {
		t.Errorf("run 2 equation lost: %+v", out[2])
	}
}

// TestPageClient_MissingAPIKey asserts that every HTTP-calling method on
// PageClient refuses to issue a request when the underlying Client has an
// empty API key, and returns ErrMissingAPIKey (wrapped).
func TestPageClient_MissingAPIKey(t *testing.T) {
	m := newPagesMockServer(t)
	// Construct a Client with an empty apiKey but pointed at the mock so
	// we can also assert no HTTP call escapes the guard.
	c := NewClient("", WithBaseURL(m.srv.URL))
	pc := NewPageClient(c)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "Get", call: func() error { _, err := pc.Get(ctx, "id"); return err }},
		{name: "Create", call: func() error {
			_, err := pc.Create(ctx, CreatePageRequest{Parent: PageParent{PageID: "p"}})
			return err
		}},
		{name: "Update", call: func() error {
			_, err := pc.Update(ctx, "id", UpdatePageRequest{Title: "t"})
			return err
		}},
		{name: "Archive", call: func() error { return pc.Archive(ctx, "id") }},
		{name: "Unarchive", call: func() error { return pc.Unarchive(ctx, "id") }},
		{name: "Move", call: func() error { return pc.Move(ctx, "id", "newParent") }},
		{name: "Duplicate", call: func() error { _, err := pc.Duplicate(ctx, "src", "parent"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrMissingAPIKey) {
				t.Errorf("expected errors.Is ErrMissingAPIKey, got %v", err)
			}
		})
	}
	// No HTTP traffic should have reached the mock.
	if calls := m.callsSnapshot(); len(calls) != 0 {
		t.Errorf("expected zero HTTP calls, got %d: %+v", len(calls), calls)
	}
}

// TestDuplicate_SourceNotFound asserts that when the source page 404s, the
// Duplicate call aborts BEFORE creating a destination page. The returned
// error must wrap the underlying 404 so callers can classify it.
func TestDuplicate_SourceNotFound(t *testing.T) {
	m := newPagesMockServer(t)
	m.notFound = true
	pc := NewPageClient(m.client())

	newPage, err := pc.Duplicate(context.Background(), "missingSrc", "parentID")
	if err == nil {
		t.Fatal("expected error when source page 404s, got nil")
	}
	if newPage != nil {
		t.Errorf("expected nil page on error, got %+v", newPage)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention 404, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "fetch source") {
		t.Errorf("expected error to mention fetch source, got %q", err.Error())
	}

	// Critical: no POST /pages (no destination created) and no PATCH
	// /blocks/*/children (no empty page orphaned).
	for _, c := range m.callsSnapshot() {
		if c.method == http.MethodPost && c.path == "/pages" {
			t.Errorf("Duplicate must not POST /pages when source 404s (calls=%+v)", m.calls)
		}
		if c.method == http.MethodPatch && strings.HasSuffix(c.path, "/children") {
			t.Errorf("Duplicate must not PATCH children when source 404s (calls=%+v)", m.calls)
		}
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name string
		page *Page
		want string
	}{
		{name: "nil page", page: nil, want: ""},
		{name: "no props", page: &Page{}, want: ""},
		{
			name: "with title",
			page: &Page{Properties: map[string]interface{}{
				"Name": map[string]interface{}{
					"title": []interface{}{
						map[string]interface{}{"plain_text": "Hello"},
					},
				},
			}},
			want: "Hello",
		},
		{
			name: "unrelated property",
			page: &Page{Properties: map[string]interface{}{
				"Tags": map[string]interface{}{"multi_select": []interface{}{}},
			}},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTitle(tt.page); got != tt.want {
				t.Errorf("extractTitle=%q want %q", got, tt.want)
			}
		})
	}
}
