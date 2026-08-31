// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// dupServer models the parts of Notion that Duplicate touches, including
// the documented 100-item cap on children — which the live API enforces.
type dupServer struct {
	mu           sync.Mutex
	appendSizes  []int
	failOnAppend int // 1-based; 0 disables
	srv          *httptest.Server
}

func newDupServer(t *testing.T, sourceBlocks int) *dupServer {
	t.Helper()
	d := &dupServer{failOnAppend: 0}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
			writeJSON(w, map[string]interface{}{
				"object": "page", "id": strings.TrimPrefix(r.URL.Path, "/pages/"),
				"properties": map[string]interface{}{
					"title": map[string]interface{}{"type": "title", "title": []map[string]interface{}{
						{"type": "text", "plain_text": "Src", "text": map[string]interface{}{"content": "Src"}}}},
				},
			})

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/children"):
			results := make([]map[string]interface{}, 0, sourceBlocks)
			for i := 0; i < sourceBlocks; i++ {
				results = append(results, map[string]interface{}{
					"object": "block", "id": fmt.Sprintf("b%d", i), "type": "paragraph",
					"paragraph": map[string]interface{}{"rich_text": []map[string]interface{}{
						{"type": "text", "plain_text": "x", "text": map[string]interface{}{"content": "x"}}}},
				})
			}
			writeJSON(w, map[string]interface{}{"object": "list", "results": results, "has_more": false})

		case r.Method == http.MethodPost && r.URL.Path == "/pages":
			writeJSON(w, map[string]interface{}{"object": "page", "id": "newPageID"})

		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/children"):
			var body struct {
				Children []json.RawMessage `json:"children"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)

			d.mu.Lock()
			d.appendSizes = append(d.appendSizes, len(body.Children))
			n := len(d.appendSizes)
			fail := d.failOnAppend
			d.mu.Unlock()

			// The live API rejects more than 100 children per request.
			if len(body.Children) > maxBlockChildrenPerRequest {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"object":"error","status":400,"code":"validation_error",
					"message":"body.children.length should be ≤ 100"}`))
				return
			}
			if fail != 0 && n == fail {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"object":"error","status":400,"code":"validation_error","message":"boom"}`))
				return
			}
			writeJSON(w, map[string]interface{}{"object": "list", "results": []interface{}{}})

		default:
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *dupServer) sizes() []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int{}, d.appendSizes...)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// TestDuplicate_ChunksAtTheDocumentedCap guards issue #97. Duplicate sent
// every block in ONE PATCH; past the documented 100-item cap the append
// failed AFTER the destination page had been created, leaving the user an
// empty orphan — and another on every retry.
func TestDuplicate_ChunksAtTheDocumentedCap(t *testing.T) {
	d := newDupServer(t, 250)
	pc := NewPageClient(NewClient("k", WithBaseURL(d.srv.URL)))

	page, err := pc.Duplicate(context.Background(), "srcID", "parentID")
	if err != nil {
		t.Fatalf("Duplicate of a 250-block page failed: %v", err)
	}
	if page.ID != "newPageID" {
		t.Errorf("page.ID = %q", page.ID)
	}

	got := d.sizes()
	want := []int{100, 100, 50}
	if len(got) != len(want) {
		t.Fatalf("append batch sizes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("append batch sizes = %v, want %v", got, want)
		}
	}
}

// TestDuplicate_UnderTheCapStillSendsOneRequest keeps the common case cheap.
func TestDuplicate_UnderTheCapStillSendsOneRequest(t *testing.T) {
	d := newDupServer(t, 12)
	pc := NewPageClient(NewClient("k", WithBaseURL(d.srv.URL)))
	if _, err := pc.Duplicate(context.Background(), "srcID", "parentID"); err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if got := d.sizes(); len(got) != 1 || got[0] != 12 {
		t.Errorf("append batches = %v, want a single batch of 12", got)
	}
}

// TestDuplicate_PartialFailureNamesTheOrphan covers what chunking cannot
// fix. Notion has no transaction across appends, so a later batch failing
// leaves a partially populated page. The error must say so — the difference
// between cleaning up one known page and finding a pile of half-copies.
func TestDuplicate_PartialFailureNamesTheOrphan(t *testing.T) {
	d := newDupServer(t, 250)
	d.failOnAppend = 2
	pc := NewPageClient(NewClient("k", WithBaseURL(d.srv.URL)))

	_, err := pc.Duplicate(context.Background(), "srcID", "parentID")
	if err == nil {
		t.Fatal("expected an error when a mid-run append fails")
	}
	msg := err.Error()
	for _, want := range []string{"100 of 250", "newPageID", "PARTIALLY"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must disclose the partial write; missing %q in: %v", want, err)
		}
	}
}

// TestDuplicate_FirstBatchFailureSaysThePageIsEmpty distinguishes the two
// cleanup situations, since they call for different action.
func TestDuplicate_FirstBatchFailureSaysThePageIsEmpty(t *testing.T) {
	d := newDupServer(t, 150)
	d.failOnAppend = 1
	pc := NewPageClient(NewClient("k", WithBaseURL(d.srv.URL)))

	_, err := pc.Duplicate(context.Background(), "srcID", "parentID")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Errorf("first-batch failure should say the page is empty, got: %v", err)
	}
}
