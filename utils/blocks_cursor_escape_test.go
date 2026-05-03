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
	"testing"
)

// TestGetAllBlocks_EscapesPaginationCursor pins #57: Notion cursors are
// opaque tokens that commonly contain reserved URL characters (`+`, `/`,
// `=`), so the paginator must run them through url.QueryEscape before
// concatenating into the query string. Without this, the second request
// goes out with a different effective start_cursor than Notion issued
// and pagination silently breaks on long pages.
//
// The fixture issues a first-page cursor that exercises every reserved
// character, then asserts the second request URL contains the
// percent-escaped form on the wire.
func TestGetAllBlocks_EscapesPaginationCursor(t *testing.T) {
	const trickyCursor = "abc+def/ghi==" // contains +, /, =

	var (
		page1Hits int
		page2Raw  string
		page2Hits int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/blocks/cursorPage/children" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// RawQuery preserves the on-the-wire encoding, which is what
		// we actually need to assert. r.URL.Query() would already
		// decode percent-escapes back to literal +///= — masking the
		// bug entirely.
		raw := r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")

		switch {
		case raw == "":
			page1Hits++
			_ = json.NewEncoder(w).Encode(BlockList{
				Results:    []Block{{Object: "block", ID: "p1", Type: "paragraph", Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "first"}}}}},
				HasMore:    true,
				NextCursor: trickyCursor,
			})
		default:
			page2Hits++
			page2Raw = raw
			// Echo back a final page regardless of cursor shape so
			// the loop terminates; the test cares about the URL, not
			// the body.
			_ = json.NewEncoder(w).Encode(BlockList{
				Results: []Block{{Object: "block", ID: "p2", Type: "paragraph", Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "second"}}}}},
				HasMore: false,
			})
		}
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	c := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))
	got, err := c.GetAllBlocks(context.Background(), "cursorPage", "")
	if err != nil {
		t.Fatalf("GetAllBlocks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 blocks across pages, got %d", len(got))
	}
	if page1Hits != 1 || page2Hits != 1 {
		t.Fatalf("want one hit per page, got page1=%d page2=%d", page1Hits, page2Hits)
	}

	// Wire-shape assertion. With escaping: `start_cursor=abc%2Bdef%2Fghi%3D%3D`.
	// Without escaping (the bug): `start_cursor=abc+def/ghi==` — the `+`
	// would also decode to space client-side. Either deviation must fail
	// this assertion.
	wantQuery := "start_cursor=abc%2Bdef%2Fghi%3D%3D"
	if page2Raw != wantQuery {
		t.Errorf("page-2 query string = %q, want %q (cursor must be url.QueryEscape'd)", page2Raw, wantQuery)
	}
	// Belt-and-braces: the raw form must NOT contain the unescaped
	// reserved characters. If a future regression switches to a
	// different escape that still produces a working URL we'd want to
	// catch it.
	for _, bad := range []string{"+", "/", "=="} {
		if strings.Contains(page2Raw, bad) {
			t.Errorf("page-2 query %q contains unescaped %q", page2Raw, bad)
		}
	}
}
