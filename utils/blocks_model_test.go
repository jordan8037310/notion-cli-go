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

// TestBlockContent_ChildPageAndDatabase guards half of issue #106. Subpages
// and inline databases are extremely common on real pages and were entirely
// unmodelled, so `blocks list` rendered them as "? … (empty)" —
// under-reporting content and making the listing untrustworthy as a page
// inventory.
func TestBlockContent_ChildPageAndDatabase(t *testing.T) {
	for _, tt := range []struct {
		name  string
		block Block
		want  string
	}{
		{"child_page", Block{Type: "child_page", ChildPage: &ChildTitleBlock{Title: "Design Notes"}}, "Design Notes"},
		{"child_database", Block{Type: "child_database", ChildDatabase: &ChildTitleBlock{Title: "Q2 Tracker"}}, "Q2 Tracker"},
		{"child_page untitled", Block{Type: "child_page", ChildPage: &ChildTitleBlock{}}, "untitled subpage"},
		{"child_database untitled", Block{Type: "child_database"}, "untitled database"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := GetBlockContent(tt.block)
			if !strings.Contains(got, tt.want) {
				t.Errorf("GetBlockContent = %q, want it to contain %q", got, tt.want)
			}
			if strings.Contains(got, "(empty)") {
				t.Errorf("%s still renders as empty: %q", tt.name, got)
			}
		})
	}
}

// TestMediaBlock_HostedFileVariant guards the other half. The file object
// has three types — "file", "file_upload" and "external" — and the hosted
// one was missing, so any image/file/video uploaded through the Notion UI
// (the common case) rendered as "(empty)".
func TestMediaBlock_HostedFileVariant(t *testing.T) {
	block := Block{Type: "image", Image: &MediaBlock{
		Type: "file",
		File: &HostedFile{URL: "https://prod-files.notion-static.com/x.png", ExpiryTime: "2026-09-01T00:00:00Z"},
	}}
	got := GetBlockContent(block)
	if !strings.Contains(got, "prod-files.notion-static.com") {
		t.Errorf("Notion-hosted image rendered as %q; the `file` variant is unhandled", got)
	}

	// The other two variants must keep working.
	ext := GetBlockContent(Block{Type: "image", Image: &MediaBlock{
		Type: "external", External: &ExternalFile{URL: "https://x/e.png"}}})
	if !strings.Contains(ext, "https://x/e.png") {
		t.Errorf("external variant regressed: %q", ext)
	}
	up := GetBlockContent(Block{Type: "file", File: &MediaBlock{
		Type: "file_upload", FileUpload: &FileUploadRef{ID: "fu-1"}}})
	if !strings.Contains(up, "fu-1") {
		t.Errorf("file_upload variant regressed: %q", up)
	}
}

// TestGetBlocksAndBlockIDPaginate guards issue #108. GetAllBlocks walked the
// full child list while its siblings GetBlocks and GetBlockID read only the
// first page, so the same 1-based index resolved against two different lists
// depending on which helper a code path used. Third instance of "one path
// paginates, its sibling does not", after #55 and #57.
func TestGetBlocksAndBlockIDPaginate(t *testing.T) {
	// 150 to-dos across two pages: the index the user sees must address
	// the same block either way.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("start_cursor")
		start, more := 0, true
		if cursor == "p2" {
			start, more = 100, false
		}
		count := 100
		if !more {
			count = 50
		}
		results := make([]map[string]interface{}, 0, count)
		for i := start; i < start+count; i++ {
			results = append(results, map[string]interface{}{
				"object": "block", "id": fmt.Sprintf("blk%d", i), "type": "to_do",
				"to_do": map[string]interface{}{"checked": false, "rich_text": []map[string]interface{}{
					{"type": "text", "plain_text": fmt.Sprintf("task %d", i)}}},
			})
		}
		next := ""
		if more {
			next = "p2"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list", "results": results, "has_more": more, "next_cursor": next,
		})
	}))
	defer srv.Close()

	bc := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))

	blocks, err := bc.GetBlocks(context.Background(), "pageID")
	if err != nil {
		t.Fatalf("GetBlocks: %v", err)
	}
	if len(blocks) != 150 {
		t.Errorf("GetBlocks returned %d of 150 to-dos — it stopped at the first page", len(blocks))
	}

	// Index 120 exists only on the second page.
	id, err := bc.GetBlockID(context.Background(), "pageID", 120)
	if err != nil {
		t.Fatalf("GetBlockID(120): %v — the index cannot reach past the first page", err)
	}
	if id != "blk119" {
		t.Errorf("GetBlockID(120) = %q, want blk119", id)
	}

	// And the two helpers must agree with GetAllBlocks.
	all, err := bc.GetAllBlocks(context.Background(), "pageID", "")
	if err != nil {
		t.Fatalf("GetAllBlocks: %v", err)
	}
	if all[119].ID != id {
		t.Errorf("GetBlockID and GetAllBlocks disagree at index 120: %q vs %q", id, all[119].ID)
	}
}
