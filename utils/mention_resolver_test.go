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
)

// resolverMockServer is a compact httptest harness tailored for the
// CachingPageResolver tests. It counts /pages/:id GETs so tests can
// assert the cache collapses repeated lookups into a single network
// call, and supports per-id pages with multi-segment titles, empty
// title properties, and 404 responses.
type resolverMockServer struct {
	srv      *httptest.Server
	getCalls int64
	// pages maps page id to its Properties payload. A nil entry means
	// "respond 404"; an entry with an empty map means "page exists but
	// has no title".
	pages map[string]map[string]interface{}
}

func newResolverMockServer(t *testing.T) *resolverMockServer {
	t.Helper()
	m := &resolverMockServer{pages: map[string]map[string]interface{}{}}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

// titleProps builds the full Properties map Notion would return for a
// page whose title rich_text array is `segments`. The outer key "title"
// is the property name; the inner "title" key is the array of rich-text
// runs, matching Notion's /v1/pages response shape.
func titleProps(segments ...string) map[string]interface{} {
	runs := make([]interface{}, 0, len(segments))
	for _, s := range segments {
		runs = append(runs, map[string]interface{}{
			"type":       "text",
			"plain_text": s,
			"text":       map[string]interface{}{"content": s},
		})
	}
	return map[string]interface{}{
		"title": map[string]interface{}{
			"id":    "title",
			"type":  "title",
			"title": runs,
		},
	}
}

func (m *resolverMockServer) addPage(id string, props map[string]interface{}) {
	m.pages[id] = props
}

// addMissing marks id as a 404.
func (m *resolverMockServer) addMissing(id string) {
	m.pages[id] = nil
}

func (m *resolverMockServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/pages/") {
		http.Error(w, `{"code":"unexpected"}`, http.StatusBadRequest)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/pages/")
	atomic.AddInt64(&m.getCalls, 1)
	props, found := m.pages[id]
	if !found || props == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found","message":"Could not find page"}`))
		return
	}
	payload := map[string]interface{}{
		"object":           "page",
		"id":               id,
		"created_time":     "2026-04-22T10:00:00.000Z",
		"last_edited_time": "2026-04-22T10:00:00.000Z",
		"in_trash":         false,
		"url":              "https://notion.so/" + id,
		"parent":           map[string]interface{}{"type": "workspace", "workspace": true},
		"properties":       props,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (m *resolverMockServer) client() *Client {
	return NewClient("sk_test", WithBaseURL(m.srv.URL))
}

// -------- NoPageResolver --------

// TestResolvePageTitle_NoResolver locks the default-path contract: a
// NoPageResolver always errors, so RenderRichTextWithResolver must emit
// the legacy "[page:<id>]" marker unchanged. Guards against any future
// refactor that accidentally treats a nil/Noop resolver as permission
// to hit the network or drop the marker.
func TestResolvePageTitle_NoResolver(t *testing.T) {
	withNoColor(t)

	got, err := NoPageResolver{}.ResolvePageTitle(context.Background(), "p-1")
	if err == nil {
		t.Fatal("NoPageResolver.ResolvePageTitle should always error")
	}
	if got != "" {
		t.Errorf("NoPageResolver title=%q, want empty", got)
	}

	rt := []RichText{{Type: "mention", Mention: &Mention{Type: "page", Page: &PageMention{ID: "p-1"}}}}
	out := RenderRichTextWithResolver(context.Background(), rt, NoPageResolver{})
	if out != "[page:p-1]" {
		t.Errorf("legacy fallback = %q, want [page:p-1]", out)
	}
}

// TestResolvePageTitle_NilResolver verifies that passing a nil
// PageTitleResolver into RenderRichTextWithResolver is treated the
// same as NoPageResolver — the mention falls through to "[page:<id>]".
// Important because cmd call sites may pass nil when no resolver is
// wired up.
func TestResolvePageTitle_NilResolver(t *testing.T) {
	withNoColor(t)

	rt := []RichText{{Type: "mention", Mention: &Mention{Type: "page", Page: &PageMention{ID: "p-2"}}}}
	out := RenderRichTextWithResolver(context.Background(), rt, nil)
	if out != "[page:p-2]" {
		t.Errorf("nil resolver path = %q, want [page:p-2]", out)
	}
}

// TestResolvePageTitle_CachingHitTwiceOneAPICall exercises the core
// caching promise of CachingPageResolver: a page mentioned N times in
// a single render pass triggers exactly one PageClient.Get call.
// Without the cache this is the N+1 problem the issue calls out.
func TestResolvePageTitle_CachingHitTwiceOneAPICall(t *testing.T) {
	m := newResolverMockServer(t)
	m.addPage("p-cache", titleProps("Project Plan"))

	pc := NewPageClient(m.client())
	r := NewCachingPageResolver(pc)

	// First lookup — miss, calls PageClient.Get.
	title1, err := r.ResolvePageTitle(context.Background(), "p-cache")
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if title1 != "Project Plan" {
		t.Errorf("title1=%q want Project Plan", title1)
	}

	// Second lookup — hit, must NOT call the API.
	title2, err := r.ResolvePageTitle(context.Background(), "p-cache")
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if title2 != "Project Plan" {
		t.Errorf("title2=%q want Project Plan", title2)
	}

	if got := atomic.LoadInt64(&m.getCalls); got != 1 {
		t.Errorf("PageClient.Get calls=%d, want 1 (cache miss)", got)
	}
}

// TestResolvePageTitle_ErrorCaching covers the negative-cache branch: a
// page that 404s on first lookup must not be re-queried on subsequent
// references. Guards against a single broken mention hammering the API
// when the same page id appears many times in one block.
func TestResolvePageTitle_ErrorCaching(t *testing.T) {
	m := newResolverMockServer(t)
	m.addMissing("p-missing")

	pc := NewPageClient(m.client())
	r := NewCachingPageResolver(pc)

	if _, err := r.ResolvePageTitle(context.Background(), "p-missing"); err == nil {
		t.Fatal("expected error on first 404 lookup")
	}
	if _, err := r.ResolvePageTitle(context.Background(), "p-missing"); err == nil {
		t.Fatal("expected cached error on second lookup")
	}

	if got := atomic.LoadInt64(&m.getCalls); got != 1 {
		t.Errorf("PageClient.Get calls=%d, want 1 (negative cache)", got)
	}
}

// TestResolvePageTitle_EmptyTitle covers the "page exists but has no
// title property" path. extractPageTitle returns "" in that case, and
// ResolvePageTitle surfaces ("", nil) so RenderRichTextWithResolver
// can fall back to "[page:<id>]" rather than emit "[]".
func TestResolvePageTitle_EmptyTitle(t *testing.T) {
	withNoColor(t)

	m := newResolverMockServer(t)
	// Page exists but has NO title property at all.
	m.addPage("p-empty", map[string]interface{}{
		"Status": map[string]interface{}{"id": "status", "type": "status"},
	})

	pc := NewPageClient(m.client())
	r := NewCachingPageResolver(pc)

	title, err := r.ResolvePageTitle(context.Background(), "p-empty")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "" {
		t.Errorf("title=%q want empty string for title-less page", title)
	}

	// Integration: RenderRichTextWithResolver must fall back to the
	// legacy marker when the resolver returns an empty title.
	rt := []RichText{{Type: "mention", Mention: &Mention{Type: "page", Page: &PageMention{ID: "p-empty"}}}}
	out := RenderRichTextWithResolver(context.Background(), rt, r)
	if out != "[page:p-empty]" {
		t.Errorf("render = %q, want [page:p-empty] fallback", out)
	}
}

// TestResolvePageTitle_MultiSegmentTitle locks the title-extraction
// behavior for pages whose title is split across multiple rich-text
// runs — the extractor must concatenate every plain_text segment in
// order rather than returning only the first one.
func TestResolvePageTitle_MultiSegmentTitle(t *testing.T) {
	m := newResolverMockServer(t)
	m.addPage("p-multi", titleProps("Hello ", "World"))

	pc := NewPageClient(m.client())
	r := NewCachingPageResolver(pc)

	title, err := r.ResolvePageTitle(context.Background(), "p-multi")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if title != "Hello World" {
		t.Errorf("title=%q want Hello World (concatenated)", title)
	}
}

// TestResolvePageTitle_EmptyPageID covers a programmer-error input:
// ResolvePageTitle called with an empty id must error before hitting
// the network so the caller's loop doesn't trigger a speculative call
// with a malformed URL.
func TestResolvePageTitle_EmptyPageID(t *testing.T) {
	m := newResolverMockServer(t)
	pc := NewPageClient(m.client())
	r := NewCachingPageResolver(pc)

	if _, err := r.ResolvePageTitle(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty page id")
	}
	if got := atomic.LoadInt64(&m.getCalls); got != 0 {
		t.Errorf("PageClient.Get called %d times on empty id, want 0", got)
	}
}

// TestResolvePageTitle_NilClient covers constructing a resolver without
// a real PageClient (e.g. a test harness or a CLI path that could not
// build one). Every lookup must error so RenderRichTextWithResolver
// falls back to "[page:<id>]" instead of panicking on a nil deref.
func TestResolvePageTitle_NilClient(t *testing.T) {
	r := NewCachingPageResolver(nil)
	if _, err := r.ResolvePageTitle(context.Background(), "p-any"); err == nil {
		t.Fatal("expected error from resolver with nil client")
	}
	// Second call also errors via the negative cache — no panic.
	if _, err := r.ResolvePageTitle(context.Background(), "p-any"); err == nil {
		t.Fatal("expected cached error on second call with nil client")
	}
}

// TestNewCachingPageResolver verifies the constructor wires up non-nil
// maps so the first lookup's cache probe doesn't nil-deref. Kept as a
// dedicated test to satisfy the gap-gate (exported constructor → needs
// a Test<Name>) and to lock the zero-state contract. The deeper
// caching / negative-cache / empty-title paths live in
// TestResolvePageTitle_* above.
func TestNewCachingPageResolver(t *testing.T) {
	r := NewCachingPageResolver(nil)
	if r == nil {
		t.Fatal("NewCachingPageResolver returned nil")
	}
	if r.cache == nil {
		t.Error("cache map is nil; first Set will panic")
	}
	if r.errs == nil {
		t.Error("errs map is nil; first Set will panic")
	}
}
