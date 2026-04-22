package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newEnhancedMock returns an httptest server that understands every Notion
// endpoint the utils package hits today. Unlike the minimal mock in
// block_test.go, this one differentiates behavior by (method, path, query)
// and covers pagination, mixed block types, and error flows.
func newEnhancedMock(t *testing.T) *httptest.Server {
	t.Helper()

	// A handful of canned Block fixtures.
	todo := func(id, text string, checked bool) Block {
		return Block{
			Object:         "block",
			ID:             id,
			Type:           "to_do",
			LastEditedTime: "2026-04-22T10:00:00.000Z",
			ToDo:           &ToDo{Checked: checked, RichText: []RichText{{PlainText: text}}},
		}
	}
	paragraph := func(id, text string) Block {
		return Block{
			Object:         "block",
			ID:             id,
			Type:           "paragraph",
			LastEditedTime: "2026-04-22T11:00:00.000Z",
			Paragraph:      &RichTextBlock{RichText: []RichText{{PlainText: text}}},
		}
	}
	heading1 := func(id, text string) Block {
		return Block{
			Object:         "block",
			ID:             id,
			Type:           "heading_1",
			LastEditedTime: "2026-04-22T12:00:00.000Z",
			Heading1:       &RichTextBlock{RichText: []RichText{{PlainText: text}}},
		}
	}
	divider := func(id string) Block {
		return Block{
			Object:         "block",
			ID:             id,
			Type:           "divider",
			LastEditedTime: "2026-04-22T13:00:00.000Z",
			Divider:        &struct{}{},
		}
	}

	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Pagination fixture: first page has_more=true + cursor, second page is final.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/paginatedPage/children":
			if r.URL.Query().Get("start_cursor") == "cursor-1" {
				writeJSON(w, BlockList{
					Results: []Block{paragraph("p3", "page-2 para")},
					HasMore: false,
				})
				return
			}
			writeJSON(w, BlockList{
				Results:    []Block{todo("p1", "first", false), paragraph("p2", "middle")},
				HasMore:    true,
				NextCursor: "cursor-1",
			})

		// Mixed block types for AddBlock/GetAllBlocks/FormatAllBlocks coverage.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/mixedPage/children":
			writeJSON(w, BlockList{
				Results: []Block{
					heading1("m1", "Title"),
					paragraph("m2", "Body text"),
					todo("m3", "A task", false),
					divider("m4"),
				},
			})

		// Empty page.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/emptyPage/children":
			writeJSON(w, BlockList{Results: []Block{}})

		// To-do listing with varied last-edited times.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/todosPage/children":
			writeJSON(w, BlockList{Results: []Block{
				todo("t1", "first", false),
				todo("t2", "second", true),
			}})

		// Used by GetBlockID tests: 3 blocks.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/threeBlocksPage/children":
			writeJSON(w, BlockList{Results: []Block{
				{Object: "block", ID: "b-1", Type: "to_do"},
				{Object: "block", ID: "b-2", Type: "to_do"},
				{Object: "block", ID: "b-3", Type: "to_do"},
			}})

		// PATCH to a block id (any) → 200 (MarkToDoBlockChecked/Unchecked)
		case r.Method == http.MethodPatch && r.URL.Path == "/blocks/b-2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))

		// PATCH /blocks/{pageID}/children → 200 (AddBlock happy path)
		case r.Method == http.MethodPatch && r.URL.Path == "/blocks/mixedPage/children":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))

		// DELETE any block → 200 (shared with existing mock pattern)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))

		// Default: 404 with a Notion-ish error shape so callers fail clearly.
		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
}

// withMock wires the enhanced mock, restores baseURL on teardown.
func withMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newEnhancedMock(t)
	prev := baseURL
	SetBaseURL(srv.URL)
	t.Cleanup(func() {
		SetBaseURL(prev)
		srv.Close()
	})
	return srv
}

// ---------- Block listing + pagination ----------

func TestGetAllBlocks(t *testing.T) {
	withMock(t)
	tests := []struct {
		name       string
		pageID     string
		filterType string
		wantCount  int
	}{
		{"no filter returns all mixed types", "mixedPage", "", 4},
		{"filter paragraph", "mixedPage", "paragraph", 1},
		{"filter heading_1", "mixedPage", "heading_1", 1},
		{"filter to_do", "mixedPage", "to_do", 1},
		{"filter divider", "mixedPage", "divider", 1},
		{"unmatched filter returns empty", "mixedPage", "code", 0},
		{"pagination walks all pages", "paginatedPage", "", 3},
		{"empty page returns empty", "emptyPage", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetAllBlocks("fakeKey", tt.pageID, tt.filterType)
			if err != nil {
				t.Fatalf("GetAllBlocks returned error: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Errorf("len=%d want=%d", len(got), tt.wantCount)
			}
		})
	}
}

func TestFormatAllBlocks(t *testing.T) {
	withMock(t)
	tz := time.UTC
	formatted, counts, err := FormatAllBlocks("fakeKey", "mixedPage", tz, "")
	if err != nil {
		t.Fatalf("FormatAllBlocks returned error: %v", err)
	}
	if len(formatted) != 4 {
		t.Fatalf("want 4 formatted lines, got %d", len(formatted))
	}
	// Type counts: one each of heading_1, paragraph, to_do, divider.
	if counts["heading_1"] != 1 || counts["paragraph"] != 1 || counts["to_do"] != 1 || counts["divider"] != 1 {
		t.Errorf("unexpected type counts: %#v", counts)
	}

	// Filter path.
	filtered, counts, err := FormatAllBlocks("fakeKey", "mixedPage", tz, "paragraph")
	if err != nil {
		t.Fatalf("FormatAllBlocks (filter) error: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("filter=paragraph: want 1, got %d", len(filtered))
	}
	if counts["paragraph"] != 1 {
		t.Errorf("filter=paragraph: type counts wrong: %#v", counts)
	}
}

func TestFormatAllBlocksBadTimestamp(t *testing.T) {
	// Install a mock that returns a malformed LastEditedTime to exercise the
	// time.Parse error branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(BlockList{Results: []Block{
			{Object: "block", ID: "x1", Type: "paragraph", LastEditedTime: "not-a-timestamp",
				Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "x"}}}},
		}})
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	_, _, err := FormatAllBlocks("fakeKey", "anyPage", time.UTC, "")
	if err == nil {
		t.Fatal("expected error on bad timestamp, got nil")
	}
}

// ---------- To-do listing ----------

func TestGetToDoBlocks(t *testing.T) {
	withMock(t)
	got, err := GetToDoBlocks("fakeKey", "todosPage", time.UTC)
	if err != nil {
		t.Fatalf("GetToDoBlocks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 todo lines, got %d: %v", len(got), got)
	}
	// First line starts with "1 [ ]" (unchecked), second with "2 [X]" (checked).
	if got[0][0:5] != "1 [ ]" {
		t.Errorf("line 1 not unchecked: %q", got[0])
	}
	if got[1][0:5] != "2 [X]" {
		t.Errorf("line 2 not checked: %q", got[1])
	}
}

func TestGetToDoBlocksBadTimestamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(BlockList{Results: []Block{
			{
				Object: "block", ID: "t1", Type: "to_do", LastEditedTime: "not-a-timestamp",
				ToDo: &ToDo{RichText: []RichText{{PlainText: "x"}}},
			},
		}})
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)
	if _, err := GetToDoBlocks("fakeKey", "anyPage", time.UTC); err == nil {
		t.Fatal("expected error on bad timestamp, got nil")
	}
}

// ---------- Block id resolution ----------

func TestGetBlockID(t *testing.T) {
	withMock(t)
	tests := []struct {
		name    string
		pageID  string
		order   int
		want    string
		wantErr bool
	}{
		{"first block", "threeBlocksPage", 1, "b-1", false},
		{"middle block", "threeBlocksPage", 2, "b-2", false},
		{"last block", "threeBlocksPage", 3, "b-3", false},
		{"order=0 rejected", "threeBlocksPage", 0, "", true},
		{"order<0 rejected", "threeBlocksPage", -1, "", true},
		{"order exceeds count", "threeBlocksPage", 99, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetBlockID("fakeKey", tt.pageID, tt.order)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}

// ---------- To-do toggles ----------

func TestMarkToDoBlockChecked(t *testing.T) {
	withMock(t)
	if err := MarkToDoBlockChecked("fakeKey", "threeBlocksPage", 2); err != nil {
		t.Fatalf("MarkToDoBlockChecked: %v", err)
	}
}

func TestMarkToDoBlockCheckedBadOrder(t *testing.T) {
	withMock(t)
	if err := MarkToDoBlockChecked("fakeKey", "threeBlocksPage", 99); err == nil {
		t.Fatal("expected error on out-of-range order, got nil")
	}
}

func TestMarkToDoBlockUnChecked(t *testing.T) {
	withMock(t)
	if err := MarkToDoBlockUnChecked("fakeKey", "threeBlocksPage", 2); err != nil {
		t.Fatalf("MarkToDoBlockUnChecked: %v", err)
	}
}

func TestMarkToDoBlockUnCheckedBadOrder(t *testing.T) {
	withMock(t)
	if err := MarkToDoBlockUnChecked("fakeKey", "threeBlocksPage", 0); err == nil {
		t.Fatal("expected error on invalid order, got nil")
	}
}

// ---------- AddBlock across every supported type ----------

func TestAddBlock_AllTypes(t *testing.T) {
	withMock(t)
	for _, typ := range GetSupportedBlockTypeNames() {
		t.Run(typ, func(t *testing.T) {
			text := "hello"
			if typ == "divider" {
				text = ""
			}
			if err := AddBlock("fakeKey", "mixedPage", typ, text); err != nil {
				t.Fatalf("AddBlock(%s): %v", typ, err)
			}
		})
	}
}

func TestAddBlock_RejectsUnknownType(t *testing.T) {
	withMock(t)
	err := AddBlock("fakeKey", "mixedPage", "image", "nope")
	if err == nil {
		t.Fatal("expected error for unsupported block type, got nil")
	}
}

func TestAddBlock_HTTPError(t *testing.T) {
	// Mock that replies 400 to surface the unexpected-status-code branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error","code":"validation_error"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)
	if err := AddBlock("fakeKey", "somePage", "paragraph", "x"); err == nil {
		t.Fatal("expected error on 400 response, got nil")
	}
}

// ---------- DeleteBlock ----------

func TestDeleteBlock(t *testing.T) {
	withMock(t)
	if err := DeleteBlock("fakeKey", "mixedPage", 1); err != nil {
		t.Fatalf("DeleteBlock: %v", err)
	}
}

func TestDeleteBlockOutOfRange(t *testing.T) {
	withMock(t)
	tests := []struct {
		name  string
		order int
	}{
		{"zero rejected", 0},
		{"negative rejected", -1},
		{"too high rejected", 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := DeleteBlock("fakeKey", "mixedPage", tt.order); err == nil {
				t.Fatalf("expected error for order=%d, got nil", tt.order)
			}
		})
	}
}
