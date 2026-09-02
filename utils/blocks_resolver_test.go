// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pageMention returns a to_do block whose rich_text array is a single
// page-mention run referencing pageID. Used to seed the mock block list
// with repeated mentions of the same page id so we can assert caching.
// pageMentionBlock builds a to_do carrying one page mention.
//
// plainText is what Notion puts in the run's plain_text. The real API sends
// the mentioned page's TITLE there — verified live, and it holds even for a
// page that has since been trashed. This fixture used to hardcode
// "[page:<id>]", i.e. the marker the CLI itself produced, which is not a
// shape Notion ever sends; that fiction is why the resolver was built to
// re-fetch a title the response already contained (issue #41).
//
// Pass "" to model the degraded case where plain_text is absent, which is
// the only situation the resolver is still needed for.
func pageMentionBlock(id, pageID, plainText string) Block {
	return Block{
		Object:         "block",
		ID:             id,
		Type:           "to_do",
		LastEditedTime: "2026-04-22T10:00:00.000Z",
		ToDo: &ToDo{
			Checked: false,
			RichText: []RichText{
				{
					Type:      "mention",
					PlainText: plainText,
					Mention:   &Mention{Type: "page", Page: &PageMention{ID: pageID}},
				},
			},
		},
	}
}

// resolverFormatMockServer answers both GET /blocks/{id}/children (for
// FormatAllBlocksWithResolver) and GET /pages/{id} (for the resolver's
// PageClient.Get). Counts page GETs so tests can assert the cache
// collapses repeated mentions of the same page into a single API call.
type resolverFormatMockServer struct {
	srv          *httptest.Server
	pageGetCalls int64
	pageTitles   map[string]string // page id → title; missing = 404
	blocks       []Block
}

func newResolverFormatMockServer(t *testing.T, blocks []Block, titles map[string]string) *resolverFormatMockServer {
	t.Helper()
	m := &resolverFormatMockServer{
		pageTitles: titles,
		blocks:     blocks,
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *resolverFormatMockServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/blocks/pageID/children":
		_ = json.NewEncoder(w).Encode(BlockList{Results: m.blocks})

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
		atomic.AddInt64(&m.pageGetCalls, 1)
		id := strings.TrimPrefix(r.URL.Path, "/pages/")
		title, ok := m.pageTitles[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "page",
			"id":     id,
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"id":   "title",
					"type": "title",
					"title": []interface{}{
						map[string]interface{}{
							"type":       "text",
							"plain_text": title,
							"text":       map[string]interface{}{"content": title},
						},
					},
				},
			},
		})

	default:
		http.Error(w, `{"code":"unexpected"}`, http.StatusBadRequest)
	}
}

// TestFormatAllBlocksWithResolver_ExpandsTitle locks the happy path:
// --resolve-mentions on, the resolver returns a non-empty title, and
// the formatted snippet contains "[<title>]" instead of "[page:<id>]".
func TestFormatAllBlocksWithResolver_ExpandsTitle(t *testing.T) {
	withNoColor(t)

	blocks := []Block{pageMentionBlock("b-1", "p-42", "")}
	m := newResolverFormatMockServer(t, blocks, map[string]string{"p-42": "Project Plan"})

	bc := NewBlockClient(m.client())
	pc := NewPageClient(m.client())
	resolver := NewCachingPageResolver(pc)

	loc, _ := time.LoadLocation("UTC")
	formatted, _, err := bc.FormatAllBlocksWithResolver(context.Background(), "pageID", loc, "", resolver)
	if err != nil {
		t.Fatalf("FormatAllBlocksWithResolver: %v", err)
	}
	if len(formatted) != 1 {
		t.Fatalf("formatted len=%d, want 1", len(formatted))
	}
	if !strings.Contains(formatted[0], "[Project Plan]") {
		t.Errorf("expected expanded title in snippet, got %q", formatted[0])
	}
	if strings.Contains(formatted[0], "[page:p-42]") {
		t.Errorf("expected expanded title to replace legacy marker, got %q", formatted[0])
	}
}

// TestFormatAllBlocksWithResolver_NoResolverPreservesMarker asserts that
// a NoPageResolver round-trip leaves the legacy "[page:<id>]" marker
// byte-for-byte unchanged. Guards against any future resolver-aware
// refactor that accidentally reaches for the network when the caller
// opted out.
func TestFormatAllBlocksWithResolver_NoResolverPreservesMarker(t *testing.T) {
	withNoColor(t)

	blocks := []Block{pageMentionBlock("b-1", "p-1", "")}
	m := newResolverFormatMockServer(t, blocks, map[string]string{"p-1": "Should Not Be Used"})

	bc := NewBlockClient(m.client())

	loc, _ := time.LoadLocation("UTC")
	formatted, _, err := bc.FormatAllBlocksWithResolver(context.Background(), "pageID", loc, "", NoPageResolver{})
	if err != nil {
		t.Fatalf("FormatAllBlocksWithResolver: %v", err)
	}
	if !strings.Contains(formatted[0], "[page:p-1]") {
		t.Errorf("no-resolver path should keep legacy marker; got %q", formatted[0])
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 0 {
		t.Errorf("page GET calls=%d, want 0 (no resolver)", got)
	}
}

// TestFormatAllBlocksWithResolver_CachingHitCount is the cache-behavior
// assertion the issue calls out: three mentions of the same page must
// trigger exactly one PageClient.Get call in total. Without the cache
// this is the N+1 problem.
func TestFormatAllBlocksWithResolver_CachingHitCount(t *testing.T) {
	withNoColor(t)

	blocks := []Block{
		pageMentionBlock("b-1", "p-repeat", ""),
		pageMentionBlock("b-2", "p-repeat", ""),
		pageMentionBlock("b-3", "p-repeat", ""),
	}
	m := newResolverFormatMockServer(t, blocks, map[string]string{"p-repeat": "Runbook"})

	bc := NewBlockClient(m.client())
	pc := NewPageClient(m.client())
	resolver := NewCachingPageResolver(pc)

	loc, _ := time.LoadLocation("UTC")
	formatted, _, err := bc.FormatAllBlocksWithResolver(context.Background(), "pageID", loc, "", resolver)
	if err != nil {
		t.Fatalf("FormatAllBlocksWithResolver: %v", err)
	}
	if len(formatted) != 3 {
		t.Fatalf("formatted len=%d, want 3", len(formatted))
	}
	for i, line := range formatted {
		if !strings.Contains(line, "[Runbook]") {
			t.Errorf("snippet %d = %q, missing expanded title", i, line)
		}
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 1 {
		t.Errorf("PageClient.Get calls=%d, want exactly 1 (cache)", got)
	}
}

// TestFormatAllBlocksWithResolver_NotFoundFallsBack covers the error
// path: the resolver hits 404 for the referenced page, and the snippet
// falls back to the legacy "[page:<id>]" marker without panicking. The
// negative cache ensures repeated mentions don't hammer the API — we
// assert pageGetCalls == 1 even with three mentions.
func TestFormatAllBlocksWithResolver_NotFoundFallsBack(t *testing.T) {
	withNoColor(t)

	blocks := []Block{
		pageMentionBlock("b-1", "p-missing", ""),
		pageMentionBlock("b-2", "p-missing", ""),
		pageMentionBlock("b-3", "p-missing", ""),
	}
	// No entry for p-missing → 404 on every GET.
	m := newResolverFormatMockServer(t, blocks, map[string]string{})

	bc := NewBlockClient(m.client())
	pc := NewPageClient(m.client())
	resolver := NewCachingPageResolver(pc)

	loc, _ := time.LoadLocation("UTC")
	formatted, _, err := bc.FormatAllBlocksWithResolver(context.Background(), "pageID", loc, "", resolver)
	if err != nil {
		t.Fatalf("FormatAllBlocksWithResolver: %v", err)
	}
	for i, line := range formatted {
		if !strings.Contains(line, "[page:p-missing]") {
			t.Errorf("snippet %d = %q, should fall back to legacy marker on 404", i, line)
		}
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 1 {
		t.Errorf("PageClient.Get calls=%d, want exactly 1 (negative cache)", got)
	}
}

// TestFormatAllBlocks_LegacyDelegatesToResolverAware locks the refactor:
// FormatAllBlocks is now a thin wrapper over FormatAllBlocksWithResolver
// with NoPageResolver{}. The legacy function's output must match the
// no-resolver path byte-for-byte so existing callers and their snapshot
// tests keep passing.
func TestFormatAllBlocks_LegacyDelegatesToResolverAware(t *testing.T) {
	withNoColor(t)

	blocks := []Block{pageMentionBlock("b-1", "p-1", "")}
	m := newResolverFormatMockServer(t, blocks, map[string]string{"p-1": "Unused"})

	bc := NewBlockClient(m.client())

	loc, _ := time.LoadLocation("UTC")
	legacy, _, err := bc.FormatAllBlocks(context.Background(), "pageID", loc, "")
	if err != nil {
		t.Fatalf("FormatAllBlocks: %v", err)
	}
	noresolver, _, err := bc.FormatAllBlocksWithResolver(context.Background(), "pageID", loc, "", NoPageResolver{})
	if err != nil {
		t.Fatalf("FormatAllBlocksWithResolver: %v", err)
	}
	if len(legacy) != len(noresolver) {
		t.Fatalf("len mismatch: legacy=%d, noresolver=%d", len(legacy), len(noresolver))
	}
	for i := range legacy {
		if legacy[i] != noresolver[i] {
			t.Errorf("line %d mismatch:\n legacy=%q\n nores =%q", i, legacy[i], noresolver[i])
		}
	}
}

func (m *resolverFormatMockServer) client() *Client {
	return NewClient("sk_test", WithBaseURL(m.srv.URL))
}

// TestGetBlockContentPlainWithResolver covers the per-block plain
// renderer: resolver hit yields "[<title>]", resolver miss preserves
// "[page:<id>]".
func TestGetBlockContentPlainWithResolver(t *testing.T) {
	withNoColor(t)

	block := pageMentionBlock("b-1", "p-x", "")

	// Stub resolver via the in-memory titles path.
	res := &stubTitleResolver{titles: map[string]string{"p-x": "Specs"}}
	got := GetBlockContentPlainWithResolver(context.Background(), block, res)
	if got != "[Specs]" {
		t.Errorf("with-resolver = %q, want [Specs]", got)
	}

	if legacy := GetBlockContentPlain(block); legacy != "[page:p-x]" {
		t.Errorf("legacy plain = %q, want [page:p-x]", legacy)
	}
}

// TestGetBlockContentWithResolver covers the ANSI-capable rendering
// path with a resolver hit.
func TestGetBlockContentWithResolver(t *testing.T) {
	withNoColor(t)

	block := pageMentionBlock("b-1", "p-y", "")
	res := &stubTitleResolver{titles: map[string]string{"p-y": "Docs"}}
	got := GetBlockContentWithResolver(context.Background(), block, res)
	if got != "[Docs]" {
		t.Errorf("with-resolver = %q, want [Docs]", got)
	}

	// Non-rich-text block types are unaffected by the resolver.
	divider := Block{Type: "divider", Divider: &struct{}{}}
	if s := GetBlockContentWithResolver(context.Background(), divider, res); s != "───────────" {
		t.Errorf("divider with resolver = %q, want the divider glyph", s)
	}
}

// TestPlainRichTextWithResolver locks the PlainRichText resolver-aware
// variant in isolation. Empty input must still return "", resolver hit
// must expand to [<title>], no resolver → legacy marker.
func TestPlainRichTextWithResolver(t *testing.T) {
	withNoColor(t)

	if got := PlainRichTextWithResolver(context.Background(), nil, NoPageResolver{}); got != "" {
		t.Errorf("empty = %q, want empty string", got)
	}

	in := []RichText{{Type: "mention", Mention: &Mention{Type: "page", Page: &PageMention{ID: "p-z"}}}}
	// No resolver → legacy marker.
	if got := PlainRichTextWithResolver(context.Background(), in, NoPageResolver{}); got != "[page:p-z]" {
		t.Errorf("noresolver = %q, want [page:p-z]", got)
	}
	// Resolver hit → expanded.
	res := &stubTitleResolver{titles: map[string]string{"p-z": "Roadmap"}}
	if got := PlainRichTextWithResolver(context.Background(), in, res); got != "[Roadmap]" {
		t.Errorf("resolved = %q, want [Roadmap]", got)
	}
}

// TestFormatAllBlocksWithResolverDelegate exercises the package-level
// FormatAllBlocksWithResolver entry point so it does not silently rot.
func TestFormatAllBlocksWithResolverDelegate(t *testing.T) {
	withNoColor(t)

	blocks := []Block{pageMentionBlock("b-1", "p-d", "")}
	m := newResolverFormatMockServer(t, blocks, map[string]string{"p-d": "Delegated"})

	// Redirect the legacy package-level baseURL so the default client
	// used by the delegate lands on our mock server.
	prior := GetBaseURL()
	SetBaseURL(m.srv.URL)
	t.Cleanup(func() { SetBaseURL(prior) })

	pc := NewPageClient(m.client())
	resolver := NewCachingPageResolver(pc)

	loc, _ := time.LoadLocation("UTC")
	formatted, _, err := FormatAllBlocksWithResolver("sk_test", "pageID", loc, "", resolver)
	if err != nil {
		t.Fatalf("FormatAllBlocksWithResolver: %v", err)
	}
	if len(formatted) != 1 {
		t.Fatalf("len=%d, want 1", len(formatted))
	}
	if !strings.Contains(formatted[0], "[Delegated]") {
		t.Errorf("snippet=%q, missing expanded title", formatted[0])
	}
}
