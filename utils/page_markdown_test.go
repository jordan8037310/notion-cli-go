// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetMarkdown_DecodesTheRealFieldNames guards the trap in issue #109.
//
// The proposal assumed `page_markdown` and `unknown_blocks`. The API
// actually returns `markdown` and `unknown_block_ids` — verified against a
// live page. Building on the assumed names would decode to an empty string
// on every call and look exactly like an empty page, with no error.
//
// The fixture below is the real response shape.
func TestGetMarkdown_DecodesTheRealFieldNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/markdown") {
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"page_markdown",
			"id":"p1",
			"markdown":"# Title\n\nbody text",
			"truncated":false,
			"unknown_block_ids":[],
			"request_id":"rq-1"
		}`))
	}))
	defer srv.Close()

	md, err := NewPageClient(NewClient("k", WithBaseURL(srv.URL))).
		GetMarkdown(context.Background(), "p1")
	if err != nil {
		t.Fatalf("GetMarkdown: %v", err)
	}
	if !strings.Contains(md.Markdown, "# Title") {
		t.Errorf("markdown did not decode — the field is `markdown`, not `page_markdown`. Got %q", md.Markdown)
	}
	if md.Object != "page_markdown" || md.RequestID != "rq-1" {
		t.Errorf("envelope fields lost: %+v", md)
	}
}

// TestGetMarkdown_SurfacesIncompleteness. Notion reports when it truncated
// the render or could not handle particular blocks. Dropping either would
// hand the user a silently partial document — the failure shape behind #88
// and #104.
func TestGetMarkdown_SurfacesIncompleteness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"p1","markdown":"partial",
			"truncated":true,"unknown_block_ids":["b1","b2"]}`))
	}))
	defer srv.Close()

	md, err := NewPageClient(NewClient("k", WithBaseURL(srv.URL))).
		GetMarkdown(context.Background(), "p1")
	if err != nil {
		t.Fatalf("GetMarkdown: %v", err)
	}
	if !md.Truncated {
		t.Error("truncated flag was dropped; the caller cannot tell the page is incomplete")
	}
	if len(md.UnknownBlockIDs) != 2 {
		t.Errorf("unknown_block_ids = %v, want both ids", md.UnknownBlockIDs)
	}
}

func TestGetMarkdown_RequiresID(t *testing.T) {
	if _, err := NewPageClient(NewClient("k")).GetMarkdown(context.Background(), ""); err == nil {
		t.Error("empty id: want error")
	}
}

// TestGetBlockTree_DescendsIntoNestedBlocks guards the other half of #109.
// `blocks list` returns TOP-LEVEL blocks only and never follows
// HasChildren, so toggles, columns and tables hide everything beneath them
// — and nothing in the output says so.
func TestGetBlockTree_DescendsIntoNestedBlocks(t *testing.T) {
	// page → [para, toggle → [para, toggle → [para]]]
	tree := map[string][]map[string]interface{}{
		"page": {
			blk("p1", "paragraph", false),
			blk("t1", "toggle", true),
		},
		"t1": {
			blk("p2", "paragraph", false),
			blk("t2", "toggle", true),
		},
		"t2": {blk("p3", "paragraph", false)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/blocks/"), "/children")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list", "has_more": false, "results": tree[id],
		})
	}))
	defer srv.Close()

	bc := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))
	nodes, err := bc.GetBlockTree(context.Background(), "page", 0)
	if err != nil {
		t.Fatalf("GetBlockTree: %v", err)
	}
	if len(nodes) != 5 {
		t.Fatalf("walked %d blocks, want 5 — the walk did not descend", len(nodes))
	}
	wantDepth := []int{0, 0, 1, 1, 2}
	for i, n := range nodes {
		if n.Depth != wantDepth[i] {
			t.Errorf("node %d (%s) depth = %d, want %d", i, n.ID, n.Depth, wantDepth[i])
		}
	}
	if nodes[4].ID != "p3" {
		t.Errorf("deepest node = %s, want p3 two levels down", nodes[4].ID)
	}
}

// TestGetBlockTree_MaxDepthBounds the walk. Each level is another paginated
// request, so an unbounded descent on a deeply nested page is expensive.
func TestGetBlockTree_MaxDepthBounds(t *testing.T) {
	tree := map[string][]map[string]interface{}{
		"page": {blk("t1", "toggle", true)},
		"t1":   {blk("t2", "toggle", true)},
		"t2":   {blk("p1", "paragraph", false)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/blocks/"), "/children")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list", "has_more": false, "results": tree[id],
		})
	}))
	defer srv.Close()

	bc := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))
	for _, tt := range []struct{ depth, want int }{
		{1, 1}, // top level only
		{2, 2}, // top level plus one
		{0, 3}, // unlimited
	} {
		nodes, err := bc.GetBlockTree(context.Background(), "page", tt.depth)
		if err != nil {
			t.Fatalf("GetBlockTree(maxDepth=%d): %v", tt.depth, err)
		}
		if len(nodes) != tt.want {
			t.Errorf("maxDepth=%d returned %d blocks, want %d", tt.depth, len(nodes), tt.want)
		}
	}
}

// TestGetBlockTree_DoesNotDescendIntoChildPages. A child_page is a page in
// its own right, not nested content — descending would pull an unrelated
// document's blocks into this page's listing.
func TestGetBlockTree_DoesNotDescendIntoChildPages(t *testing.T) {
	var fetched []string
	tree := map[string][]map[string]interface{}{
		"page": {blk("cp1", "child_page", true), blk("cd1", "child_database", true)},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/blocks/"), "/children")
		fetched = append(fetched, id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list", "has_more": false, "results": tree[id],
		})
	}))
	defer srv.Close()

	nodes, err := NewBlockClient(NewClient("k", WithBaseURL(srv.URL))).
		GetBlockTree(context.Background(), "page", 0)
	if err != nil {
		t.Fatalf("GetBlockTree: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("got %d nodes, want the 2 children without descending", len(nodes))
	}
	for _, id := range fetched {
		if id == "cp1" || id == "cd1" {
			t.Errorf("descended into %s — a subpage's blocks belong to that page, not this one", id)
		}
	}
}

func blk(id, typ string, hasChildren bool) map[string]interface{} {
	return map[string]interface{}{
		"object": "block", "id": id, "type": typ, "has_children": hasChildren,
		typ: map[string]interface{}{"rich_text": []map[string]interface{}{
			{"type": "text", "plain_text": fmt.Sprintf("%s content", id)}}},
	}
}
