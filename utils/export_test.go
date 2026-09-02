// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// exportMock serves a small page hierarchy:
//
//	root ── alpha ── deep
//	    └── beta
//
// Pages listed in forbidden 403 on GET, so partial-export behaviour can be
// driven without inventing a transport failure.
type exportMock struct {
	srv       *httptest.Server
	forbidden map[string]bool
	// childrenOf maps a page id to the child_page ids it reports.
	childrenOf map[string][]string
	// cycleTo, when set for a page, makes it report that page id as its
	// own child — a shape Notion should never produce, used to prove the
	// walker cannot be made to loop.
	cycleTo map[string]string

	mu    sync.Mutex
	calls []string
}

func newExportMock(t *testing.T) *exportMock {
	t.Helper()
	m := &exportMock{
		forbidden: map[string]bool{},
		childrenOf: map[string][]string{
			"root":  {"alpha", "beta"},
			"alpha": {"deep"},
		},
		cycleTo: map[string]string{},
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *exportMock) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, r.Method+" "+r.URL.Path)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch {
	case strings.HasSuffix(r.URL.Path, "/markdown"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/pages/"), "/markdown")
		_, _ = fmt.Fprintf(w, `{"object":"page_markdown","id":%q,"markdown":"# %s body\n","truncated":false,"unknown_block_ids":[]}`, id, id)

	case strings.HasSuffix(r.URL.Path, "/children"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/blocks/"), "/children")
		var parts []string
		if target, ok := m.cycleTo[id]; ok {
			parts = append(parts, fmt.Sprintf(
				`{"object":"block","id":%q,"type":"child_page","has_children":true,"child_page":{"title":%q}}`, target, target))
		}
		// One ordinary block so the page is never empty, proving the
		// walker filters on type rather than on position.
		parts = append(parts, `{"object":"block","id":"para","type":"paragraph","has_children":false,"paragraph":{"rich_text":[]}}`)
		for _, child := range m.childrenOf[id] {
			parts = append(parts, fmt.Sprintf(
				`{"object":"block","id":%q,"type":"child_page","has_children":true,"child_page":{"title":%q}}`, child, child))
		}
		_, _ = fmt.Fprintf(w, `{"object":"list","results":[%s],"has_more":false,"next_cursor":null}`, strings.Join(parts, ","))

	case strings.HasPrefix(r.URL.Path, "/pages/"):
		id := strings.TrimPrefix(r.URL.Path, "/pages/")
		if m.forbidden[id] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"object":"error","status":403,"code":"restricted_resource","message":"no access"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"object":"page","id":%q,"properties":{"title":{"id":"title","type":"title","title":[{"type":"text","plain_text":%q,"text":{"content":%q}}]}}}`, id, "Page "+id, "Page "+id)

	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"object":"error","status":404,"code":"object_not_found","message":"nope"}`)
	}
}

func (m *exportMock) client() *PageClient {
	return NewPageClient(NewClient("sk_test", WithBaseURL(m.srv.URL), WithMaxRetries(0)))
}

func (m *exportMock) countCalls(substr string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

// titles flattens a tree to "title(depth)" strings in walk order.
func titles(root *PageNode) []string {
	var out []string
	root.Walk(func(n *PageNode) {
		out = append(out, fmt.Sprintf("%s(%d)", n.ID, n.Depth))
	})
	return out
}

// TestExportTree_UnlimitedDepth walks the whole hierarchy.
func TestExportTree_UnlimitedDepth(t *testing.T) {
	m := newExportMock(t)
	root, err := m.client().ExportTree(context.Background(), "root", ExportOptions{MaxDepth: -1})
	if err != nil {
		t.Fatalf("ExportTree: %v", err)
	}
	got := strings.Join(titles(root), " ")
	want := "root(0) alpha(1) deep(2) beta(1)"
	if got != want {
		t.Errorf("tree = %q, want %q", got, want)
	}
	if root.Count() != 4 {
		t.Errorf("Count = %d, want 4", root.Count())
	}
	if root.Title != "Page root" {
		t.Errorf("Title = %q, want the page's title property", root.Title)
	}
}

// TestExportTree_DepthLimits pins the boundary the issue specifies: depth
// 0 is the page alone, 1 adds its immediate children.
func TestExportTree_DepthLimits(t *testing.T) {
	for _, tc := range []struct {
		depth int
		want  string
	}{
		{depth: 0, want: "root(0)"},
		{depth: 1, want: "root(0) alpha(1) beta(1)"},
		{depth: 2, want: "root(0) alpha(1) deep(2) beta(1)"},
	} {
		t.Run(fmt.Sprintf("depth-%d", tc.depth), func(t *testing.T) {
			m := newExportMock(t)
			root, err := m.client().ExportTree(context.Background(), "root", ExportOptions{MaxDepth: tc.depth})
			if err != nil {
				t.Fatalf("ExportTree: %v", err)
			}
			if got := strings.Join(titles(root), " "); got != tc.want {
				t.Errorf("tree = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExportTree_CycleIsNotFollowed proves the visited set works. Notion's
// hierarchy is a tree so this shape should be impossible — but a backup
// command must not be loopable by unexpected server data.
func TestExportTree_CycleIsNotFollowed(t *testing.T) {
	m := newExportMock(t)
	m.childrenOf = map[string][]string{"root": {"alpha"}, "alpha": {}}
	m.cycleTo["alpha"] = "root" // alpha claims root as its own child

	done := make(chan *PageNode, 1)
	go func() {
		root, err := m.client().ExportTree(context.Background(), "root", ExportOptions{MaxDepth: -1})
		if err != nil {
			t.Errorf("ExportTree: %v", err)
		}
		done <- root
	}()
	root := <-done

	if root.Count() != 3 {
		t.Fatalf("Count = %d, want 3 (root, alpha, and the refused revisit)", root.Count())
	}
	var revisit *PageNode
	root.Walk(func(n *PageNode) {
		if n.Depth == 2 {
			revisit = n
		}
	})
	if revisit == nil || !strings.Contains(revisit.Err, "cycle") {
		t.Errorf("revisited node = %+v, want it recorded as a cycle rather than followed", revisit)
	}
}

// TestExportTree_ForbiddenChildDoesNotAbort is the whole reason errors are
// per-node. A workspace contains pages the integration cannot read, and an
// export that dies on the first one cannot take a backup at all.
func TestExportTree_ForbiddenChildDoesNotAbort(t *testing.T) {
	m := newExportMock(t)
	m.forbidden["alpha"] = true

	root, err := m.client().ExportTree(context.Background(), "root", ExportOptions{MaxDepth: -1})
	if err != nil {
		t.Fatalf("ExportTree aborted on an unreadable child: %v", err)
	}
	failed := root.Errors()
	if len(failed) != 1 || failed[0].ID != "alpha" {
		t.Fatalf("Errors() = %+v, want exactly the forbidden page", failed)
	}
	if !strings.Contains(failed[0].Err, "restricted_resource") {
		t.Errorf("error = %q, want Notion's own code surfaced", failed[0].Err)
	}
	// beta is a sibling and must still be exported.
	var sawBeta bool
	root.Walk(func(n *PageNode) {
		if n.ID == "beta" && n.Page != nil {
			sawBeta = true
		}
	})
	if !sawBeta {
		t.Error("beta was not exported; one unreadable sibling must not take the others with it")
	}
	// alpha's own subtree is unreachable, so deep is not attempted.
	if m.countCalls("/pages/deep") != 0 {
		t.Error("descended into the subtree of a page that could not be read")
	}
}

// TestExportTree_RootFailureIsFatal is the one case that must NOT be
// tolerated: the caller named that page specifically, so an empty tree is
// not a useful answer.
func TestExportTree_RootFailureIsFatal(t *testing.T) {
	m := newExportMock(t)
	m.forbidden["root"] = true
	if _, err := m.client().ExportTree(context.Background(), "root", ExportOptions{MaxDepth: -1}); err == nil {
		t.Fatal("ExportTree succeeded with an unreadable root")
	}
}

// TestExportTree_MarkdownComesFromNotion checks the render is fetched per
// page rather than assembled locally, and that blocks are not fetched into
// the node when only markdown was asked for.
func TestExportTree_MarkdownComesFromNotion(t *testing.T) {
	m := newExportMock(t)
	root, err := m.client().ExportTree(context.Background(), "root",
		ExportOptions{MaxDepth: 1, WithMarkdown: true})
	if err != nil {
		t.Fatalf("ExportTree: %v", err)
	}
	if root.Markdown != "# root body\n" {
		t.Errorf("Markdown = %q, want Notion's rendered page", root.Markdown)
	}
	if len(root.Blocks) != 0 {
		t.Errorf("Blocks = %v, want none when only markdown was requested", root.Blocks)
	}
	if got := m.countCalls("/markdown"); got != 3 {
		t.Errorf("markdown renders = %d, want one per exported page (3)", got)
	}
}

// TestExportTree_BlocksAreFetchedOnlyWhenAsked guards the other direction,
// and documents that a block listing happens regardless — it is the only
// way to discover sub-pages.
func TestExportTree_BlocksAreFetchedOnlyWhenAsked(t *testing.T) {
	m := newExportMock(t)
	root, err := m.client().ExportTree(context.Background(), "root",
		ExportOptions{MaxDepth: 0, WithBlocks: true})
	if err != nil {
		t.Fatalf("ExportTree: %v", err)
	}
	if len(root.Blocks) == 0 {
		t.Error("Blocks is empty despite WithBlocks")
	}
	if root.Markdown != "" {
		t.Errorf("Markdown = %q, want none when it was not requested", root.Markdown)
	}
	if got := m.countCalls("/markdown"); got != 0 {
		t.Errorf("made %d markdown renders for a blocks-only export", got)
	}
}

// TestExportTree_RequiresAnID keeps a missing argument from becoming a GET
// against /pages/.
func TestExportTree_RequiresAnID(t *testing.T) {
	m := newExportMock(t)
	if _, err := m.client().ExportTree(context.Background(), "", ExportOptions{}); err == nil {
		t.Error("ExportTree accepted an empty id")
	}
	if len(m.calls) != 0 {
		t.Errorf("made %v requests for an empty id", m.calls)
	}
}

// TestExportTree_StopsOnCanceledContext keeps an interrupted export from
// crawling the rest of a workspace.
func TestExportTree_StopsOnCanceledContext(t *testing.T) {
	m := newExportMock(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.client().ExportTree(ctx, "root", ExportOptions{MaxDepth: -1}); err == nil {
		t.Fatal("ExportTree succeeded on a canceled context")
	}
	if m.countCalls("/pages/alpha") != 0 {
		t.Error("kept walking after cancellation")
	}
}
