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
	"sync"
	"testing"
)

// TestToDoIndex_MatchesListNumbering pins the central contract for #55:
// MarkToDoBlockChecked / MarkToDoBlockUnChecked / DeleteToDoBlock all
// number to-do blocks the way `notioncli list` numbers them — the 1-based
// ordinal into the to-do-only subset, NOT the absolute block index.
//
// Fixture: a mixed-block page where to-dos are at absolute positions
// 2, 4, 6 and to-do ordinals 1, 2, 3. Asking for to-do #2 must hit the
// block at absolute position 4 ("middle todo"), not absolute position 2
// ("heading"). The pre-fix code would have hit the heading and either
// silently no-op'd or 400'd because heading blocks have no .to_do field.
func TestToDoIndex_MatchesListNumbering(t *testing.T) {
	cases := []struct {
		name string
		// op is the operation under test; it returns an error so the
		// table-driven runner can fail with the per-op message.
		op func(ctx context.Context, c *BlockClient, pageID string, order int) error
		// wantMethod is the HTTP method the op should issue against the
		// resolved block id. PATCH for check/uncheck, DELETE for delete.
		wantMethod string
	}{
		{
			name: "MarkToDoBlockChecked",
			op: func(ctx context.Context, c *BlockClient, p string, n int) error {
				return c.MarkToDoBlockChecked(ctx, p, n)
			},
			wantMethod: http.MethodPatch,
		},
		{
			name: "MarkToDoBlockUnChecked",
			op: func(ctx context.Context, c *BlockClient, p string, n int) error {
				return c.MarkToDoBlockUnChecked(ctx, p, n)
			},
			wantMethod: http.MethodPatch,
		},
		{
			name:       "DeleteToDoBlock",
			op:         func(ctx context.Context, c *BlockClient, p string, n int) error { return c.DeleteToDoBlock(ctx, p, n) },
			wantMethod: http.MethodDelete,
		},
	}

	// Each ordinal maps to the to-do block id we expect the op to hit.
	// Absolute layout: [paragraph, to_do(td1), heading, to_do(td2),
	// divider, to_do(td3)]. So ordinal 1 → td1, 2 → td2, 3 → td3.
	wantByOrdinal := map[int]string{1: "td1", 2: "td2", 3: "td3"}

	for _, tc := range cases {
		for ordinal, wantID := range wantByOrdinal {
			t.Run(tc.name+"/ordinal_"+itoa(ordinal), func(t *testing.T) {
				srv, captured := newMixedTodoServer(t)
				defer srv.Close()
				prev := baseURL
				SetBaseURL(srv.URL)
				defer SetBaseURL(prev)

				c := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))
				if err := tc.op(context.Background(), c, "mixedTodoPage", ordinal); err != nil {
					t.Fatalf("%s(ordinal=%d): %v", tc.name, ordinal, err)
				}
				gotMethod, gotID := captured()
				if gotMethod != tc.wantMethod {
					t.Errorf("HTTP method = %q, want %q", gotMethod, tc.wantMethod)
				}
				if gotID != wantID {
					t.Errorf("hit block id %q for ordinal %d, want %q (ordinal must index to-dos only, not absolute blocks)", gotID, ordinal, wantID)
				}
			})
		}
	}
}

// TestToDoIndex_ZeroToDosErrors asserts that calling check/uncheck/delete
// on a page with no to-do blocks surfaces a clear error before any
// mutation, instead of silently no-op'ing or 400-ing on a non-to_do
// block.
func TestToDoIndex_ZeroToDosErrors(t *testing.T) {
	srv, _ := newMixedTodoServer(t)
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	c := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))

	for _, op := range []struct {
		name string
		fn   func() error
	}{
		{"check", func() error { return c.MarkToDoBlockChecked(context.Background(), "noTodosPage", 1) }},
		{"uncheck", func() error { return c.MarkToDoBlockUnChecked(context.Background(), "noTodosPage", 1) }},
		{"delete", func() error { return c.DeleteToDoBlock(context.Background(), "noTodosPage", 1) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			if err == nil {
				t.Fatal("expected error on zero-todo page, got nil")
			}
			if !strings.Contains(err.Error(), "no to-do") {
				t.Errorf("err = %v, want substring 'no to-do'", err)
			}
		})
	}
}

// TestToDoIndex_SkipsEmptyToDos pins the post-PR-#75 contract: empty
// to-do blocks (no rich_text) are hidden from the human `list` command,
// so check/uncheck/delete must skip them too. Otherwise ordinal N from
// `list` resolves to a different block in the resolver, which is the
// same data-loss class as the original #55 index drift.
//
// Fixture: [todo(""), todo("real-1"), todo(""), todo("real-2")]. The
// human list shows the two real to-dos as ordinals 1 and 2; the
// resolver must agree.
func TestToDoIndex_SkipsEmptyToDos(t *testing.T) {
	srv, captured := newEmptyTodosMixedServer(t)
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	c := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))

	if err := c.MarkToDoBlockChecked(context.Background(), "emptyMixedPage", 1); err != nil {
		t.Fatalf("MarkToDoBlockChecked(ordinal=1): %v", err)
	}
	if _, gotID := captured(); gotID != "real-1" {
		t.Errorf("ordinal 1 hit %q, want \"real-1\" (the empty to-do at absolute position 1 must be skipped)", gotID)
	}

	if err := c.MarkToDoBlockChecked(context.Background(), "emptyMixedPage", 2); err != nil {
		t.Fatalf("MarkToDoBlockChecked(ordinal=2): %v", err)
	}
	if _, gotID := captured(); gotID != "real-2" {
		t.Errorf("ordinal 2 hit %q, want \"real-2\" (skipping empty at absolute position 3)", gotID)
	}

	// Out of range: only 2 visible to-dos, not 4.
	err := c.MarkToDoBlockChecked(context.Background(), "emptyMixedPage", 3)
	if err == nil {
		t.Fatal("ordinal 3 should be out of range (page has 2 visible to-dos), got nil")
	}
	if !strings.Contains(err.Error(), "2 to-do block") {
		t.Errorf("err = %v; want substring '2 to-do block' (the visible count)", err)
	}
}

// newEmptyTodosMixedServer returns a fixture with empty and non-empty
// to-do blocks interleaved. Used by TestToDoIndex_SkipsEmptyToDos.
func newEmptyTodosMixedServer(t *testing.T) (*httptest.Server, func() (string, string)) {
	t.Helper()

	var mu sync.Mutex
	var lastMethod, lastBlockID string

	blocks := []Block{
		{Object: "block", ID: "blank-1", Type: "to_do", ToDo: &ToDo{Checked: false}}, // empty rich_text
		{Object: "block", ID: "real-1", Type: "to_do", ToDo: &ToDo{Checked: false, RichText: []RichText{{PlainText: "first task"}}}},
		{Object: "block", ID: "blank-2", Type: "to_do", ToDo: &ToDo{Checked: false}}, // empty rich_text
		{Object: "block", ID: "real-2", Type: "to_do", ToDo: &ToDo{Checked: false, RichText: []RichText{{PlainText: "second task"}}}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/emptyMixedPage/children":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BlockList{Results: blocks})
		case strings.HasPrefix(r.URL.Path, "/blocks/") && (r.Method == http.MethodPatch || r.Method == http.MethodDelete):
			id := strings.TrimPrefix(r.URL.Path, "/blocks/")
			mu.Lock()
			lastMethod = r.Method
			lastBlockID = id
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return lastMethod, lastBlockID
	}
}

// TestToDoIndex_OutOfRangeNamesToDoCount asserts the error message when
// the requested ordinal exceeds the to-do count cites the to-do count,
// not the total-block count, so users have a useful number to act on.
func TestToDoIndex_OutOfRangeNamesToDoCount(t *testing.T) {
	srv, _ := newMixedTodoServer(t)
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	c := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))
	err := c.MarkToDoBlockChecked(context.Background(), "mixedTodoPage", 99)
	if err == nil {
		t.Fatal("expected error on out-of-range ordinal, got nil")
	}
	// Page has 3 to-dos (and 6 total blocks). Error must cite 3, not 6.
	if !strings.Contains(err.Error(), "3 to-do block") {
		t.Errorf("err = %v, want substring '3 to-do block'", err)
	}
}

// newMixedTodoServer wires an httptest server with two fixture pages:
//
//   - mixedTodoPage: 6 blocks alternating paragraph/heading/divider with
//     to-dos at absolute positions 2, 4, 6 (to-do ordinals 1, 2, 3).
//   - noTodosPage:   3 non-to-do blocks (paragraph, heading, divider).
//
// Returns a thunk that captures the (method, blockID) of the last write
// against /blocks/{id} so tests can assert which block was actually hit.
func newMixedTodoServer(t *testing.T) (*httptest.Server, func() (string, string)) {
	t.Helper()

	var mu sync.Mutex
	var lastMethod, lastBlockID string

	mixed := []Block{
		{Object: "block", ID: "p1", Type: "paragraph", Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "intro"}}}},
		{Object: "block", ID: "td1", Type: "to_do", ToDo: &ToDo{Checked: false, RichText: []RichText{{PlainText: "first"}}}},
		{Object: "block", ID: "h1", Type: "heading_1", Heading1: &RichTextBlock{RichText: []RichText{{PlainText: "Section"}}}},
		{Object: "block", ID: "td2", Type: "to_do", ToDo: &ToDo{Checked: false, RichText: []RichText{{PlainText: "middle"}}}},
		{Object: "block", ID: "d1", Type: "divider", Divider: &struct{}{}},
		{Object: "block", ID: "td3", Type: "to_do", ToDo: &ToDo{Checked: true, RichText: []RichText{{PlainText: "last"}}}},
	}
	noTodos := []Block{
		{Object: "block", ID: "p1", Type: "paragraph", Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "x"}}}},
		{Object: "block", ID: "h1", Type: "heading_1", Heading1: &RichTextBlock{RichText: []RichText{{PlainText: "y"}}}},
		{Object: "block", ID: "d1", Type: "divider", Divider: &struct{}{}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/mixedTodoPage/children":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BlockList{Results: mixed})
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/noTodosPage/children":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BlockList{Results: noTodos})
		case strings.HasPrefix(r.URL.Path, "/blocks/") && (r.Method == http.MethodPatch || r.Method == http.MethodDelete):
			id := strings.TrimPrefix(r.URL.Path, "/blocks/")
			mu.Lock()
			lastMethod = r.Method
			lastBlockID = id
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return lastMethod, lastBlockID
	}
}

// itoa is a tiny dependency-free int→string helper so subtest names don't
// pull in strconv just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
