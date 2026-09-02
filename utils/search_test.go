package utils

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSearchClient_MissingAPIKey verifies Search/SearchAll surface
// ErrMissingAPIKey instead of panicking on a nil client or falling
// through to an opaque 401 from Notion. Mirrors the contract on
// PageClient/UserClient/TeamClient. Regression guard for #54.
func TestSearchClient_MissingAPIKey(t *testing.T) {
	client := NewSearchClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	if _, err := client.Search(context.Background(), SearchRequest{Query: "x"}); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Search: err = %v, want errors.Is ErrMissingAPIKey", err)
	}
	if _, err := client.SearchAll(context.Background(), SearchRequest{Query: "x"}, 10); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("SearchAll: err = %v, want errors.Is ErrMissingAPIKey", err)
	}
}

// TestSearchClient_NilClientNoPanic confirms NewSearchClient(nil) does
// not panic on first call — library callers wiring through a test seam
// get a typed error.
func TestSearchClient_NilClientNoPanic(t *testing.T) {
	client := NewSearchClient(nil)
	if _, err := client.Search(context.Background(), SearchRequest{}); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Search(nil client): err = %v, want errors.Is ErrMissingAPIKey", err)
	}
}

// newSearchMock returns an httptest server that handles POST /search, with
// behavior driven by the query field of the request body. This mirrors the
// block_extra_test.go pattern of switching on (method, path, payload) rather
// than spinning up a fresh server per test case.
func newSearchMock(t *testing.T) *httptest.Server {
	t.Helper()

	result := func(id, object, title string) SearchResult {
		raw := `{"object":"` + object + `","id":"` + id + `","url":"https://notion.so/` + id + `","last_edited_time":"2026-04-22T10:00:00.000Z","icon":{"type":"emoji","emoji":"🗒️"},"properties":{"title":{"title":[{"plain_text":"` + title + `"}]}}}`
		var r SearchResult
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatalf("seed result: %v", err)
		}
		return r
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req SearchRequest
		_ = json.Unmarshal(body, &req)

		writeJSON := func(v interface{}) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}

		// Pagination fixture: "paginated" returns two pages.
		if req.Query == "paginated" {
			if req.StartCursor == "cursor-1" {
				writeJSON(SearchResponse{
					Object:  "list",
					Results: []SearchResult{result("pg-3", "page", "Third")},
					HasMore: false,
				})
				return
			}
			writeJSON(SearchResponse{
				Object:     "list",
				Results:    []SearchResult{result("pg-1", "page", "First"), result("pg-2", "page", "Second")},
				HasMore:    true,
				NextCursor: "cursor-1",
			})
			return
		}

		// Empty fixture.
		if req.Query == "missing" {
			writeJSON(SearchResponse{Object: "list", Results: []SearchResult{}})
			return
		}

		// Type-filtered fixtures.
		if req.Filter != nil && req.Filter.Property == "object" {
			switch req.Filter.Value {
			case "page":
				writeJSON(SearchResponse{Object: "list", Results: []SearchResult{
					result("pg-only", "page", "Only Page"),
				}})
				return
			case "database":
				writeJSON(SearchResponse{Object: "list", Results: []SearchResult{
					result("db-only", "database", "Only DB"),
				}})
				return
			}
		}

		// Default: one page + one database.
		writeJSON(SearchResponse{Object: "list", Results: []SearchResult{
			result("pg-1", "page", "Roadmap"),
			result("db-1", "database", "Tracker"),
		}})
	}))
}

// withSearchMock wires the search mock and restores baseURL on teardown.
func withSearchMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newSearchMock(t)
	prev := baseURL
	SetBaseURL(srv.URL)
	t.Cleanup(func() {
		SetBaseURL(prev)
		srv.Close()
	})
	return srv
}

// newClientForSearch returns a SearchClient bound to the current baseURL so
// tests exercise the same wiring the cmd layer uses.
func newClientForSearch() *SearchClient {
	// Retries off: these tests assert that an error SURFACES, not that it
	// is retried, and the default policy would make each of them pay four
	// real backoffs.
	return NewSearchClient(NewClient("fakeKey", WithBaseURL(baseURL), WithMaxRetries(0)))
}

func TestSearch(t *testing.T) {
	withSearchMock(t)
	sc := newClientForSearch()

	tests := []struct {
		name    string
		req     SearchRequest
		wantLen int
		wantIDs []string
	}{
		{
			name:    "basic query returns page + database",
			req:     SearchRequest{Query: "roadmap"},
			wantLen: 2,
			wantIDs: []string{"pg-1", "db-1"},
		},
		{
			name:    "filter pages only",
			req:     SearchRequest{Query: "x", Filter: &SearchFilter{Property: "object", Value: "page"}},
			wantLen: 1,
			wantIDs: []string{"pg-only"},
		},
		{
			name:    "filter databases only",
			req:     SearchRequest{Query: "x", Filter: &SearchFilter{Property: "object", Value: "database"}},
			wantLen: 1,
			wantIDs: []string{"db-only"},
		},
		{
			name:    "empty results",
			req:     SearchRequest{Query: "missing"},
			wantLen: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := sc.Search(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(resp.Results) != tt.wantLen {
				t.Fatalf("len=%d want=%d", len(resp.Results), tt.wantLen)
			}
			for i, id := range tt.wantIDs {
				if resp.Results[i].ID != id {
					t.Errorf("result[%d].ID=%q want=%q", i, resp.Results[i].ID, id)
				}
			}
		})
	}
}

func TestSearchAll(t *testing.T) {
	withSearchMock(t)
	sc := newClientForSearch()

	tests := []struct {
		name    string
		req     SearchRequest
		limit   int
		wantLen int
		wantIDs []string
	}{
		{
			name:    "walks pagination across two pages",
			req:     SearchRequest{Query: "paginated"},
			limit:   0,
			wantLen: 3,
			wantIDs: []string{"pg-1", "pg-2", "pg-3"},
		},
		{
			name:    "limit truncates mid-page",
			req:     SearchRequest{Query: "paginated"},
			limit:   2,
			wantLen: 2,
			wantIDs: []string{"pg-1", "pg-2"},
		},
		{
			name:    "limit exceeds total returns all",
			req:     SearchRequest{Query: "paginated"},
			limit:   999,
			wantLen: 3,
			wantIDs: []string{"pg-1", "pg-2", "pg-3"},
		},
		{
			name:    "single page returns early",
			req:     SearchRequest{Query: "roadmap"},
			limit:   0,
			wantLen: 2,
			wantIDs: []string{"pg-1", "db-1"},
		},
		{
			name:    "empty result set",
			req:     SearchRequest{Query: "missing"},
			limit:   0,
			wantLen: 0,
		},
		{
			name:    "negative limit treated as unlimited",
			req:     SearchRequest{Query: "paginated"},
			limit:   -5,
			wantLen: 3,
			wantIDs: []string{"pg-1", "pg-2", "pg-3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sc.SearchAll(context.Background(), tt.req, tt.limit)
			if err != nil {
				t.Fatalf("SearchAll: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len=%d want=%d", len(got), tt.wantLen)
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("result[%d].ID=%q want=%q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestSearchAll_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error","code":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	sc := newClientForSearch()
	_, err := sc.SearchAll(context.Background(), SearchRequest{Query: "anything"}, 0)
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	// Page 1 failures should be wrapped with "search page 1:".
	if !strings.Contains(err.Error(), "search page 1:") {
		t.Errorf("error missing page-number wrap: %v", err)
	}
}

// TestSearchAll_MidPaginationError verifies that a failure on page 2 of a
// walk is wrapped with the correct page number so callers can see how far
// the walk got before failing.
func TestSearchAll_MidPaginationError(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(SearchResponse{
				Object:     "list",
				Results:    []SearchResult{},
				HasMore:    true,
				NextCursor: "cursor-2",
			})
			return
		}
		http.Error(w, `{"object":"error","code":"rate_limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	sc := newClientForSearch()
	_, err := sc.SearchAll(context.Background(), SearchRequest{Query: "sweep"}, 0)
	if err == nil {
		t.Fatal("expected mid-pagination error, got nil")
	}
	if !strings.Contains(err.Error(), "search page 2:") {
		t.Errorf("error missing page-2 wrap: %v", err)
	}
}

func TestUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantID   string
		wantType string
		wantRaw  string
	}{
		{
			name:     "page payload populates typed + raw",
			input:    `{"object":"page","id":"pg-1","url":"https://notion.so/pg-1","last_edited_time":"2026-04-22T10:00:00.000Z","icon":{"type":"emoji","emoji":"📄"}}`,
			wantID:   "pg-1",
			wantType: "page",
			wantRaw:  `"id":"pg-1"`,
		},
		{
			name:     "database payload",
			input:    `{"object":"database","id":"db-9","url":"https://notion.so/db-9"}`,
			wantID:   "db-9",
			wantType: "database",
			wantRaw:  `"id":"db-9"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r SearchResult
			if err := json.Unmarshal([]byte(tt.input), &r); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if r.ID != tt.wantID {
				t.Errorf("ID=%q want=%q", r.ID, tt.wantID)
			}
			if r.Object != tt.wantType {
				t.Errorf("Object=%q want=%q", r.Object, tt.wantType)
			}
			if !strings.Contains(string(r.Raw), tt.wantRaw) {
				t.Errorf("Raw missing %q: %s", tt.wantRaw, string(r.Raw))
			}
		})
	}

	t.Run("malformed json returns error", func(t *testing.T) {
		var r SearchResult
		if err := json.Unmarshal([]byte(`{"id":`), &r); err == nil {
			t.Fatal("expected error on malformed json, got nil")
		}
	})
}

func TestSearchResultRawPreservedThroughHTTP(t *testing.T) {
	withSearchMock(t)
	sc := newClientForSearch()
	resp, err := sc.Search(context.Background(), SearchRequest{Query: "roadmap"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("no results")
	}
	if len(resp.Results[0].Raw) == 0 {
		t.Fatal("Raw was not populated")
	}
	if !strings.Contains(string(resp.Results[0].Raw), `"id":"pg-1"`) {
		t.Errorf("Raw did not contain expected id: %s", string(resp.Results[0].Raw))
	}
}

func TestDisplay(t *testing.T) {
	tests := []struct {
		name string
		icon *Icon
		want string
	}{
		{"nil icon", nil, ""},
		{"emoji", &Icon{Type: "emoji", Emoji: "📄"}, "📄"},
		{"external url", &Icon{Type: "external", External: &IconExternal{URL: "https://ex/x.png"}}, "https://ex/x.png"},
		{"external nil inner", &Icon{Type: "external"}, ""},
		{"file url", &Icon{Type: "file", File: &IconFile{URL: "https://files/y.png"}}, "https://files/y.png"},
		{"file nil inner", &Icon{Type: "file"}, ""},
		{"unknown type", &Icon{Type: "custom"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.icon.Display(); got != tt.want {
				t.Errorf("Display()=%q want=%q", got, tt.want)
			}
		})
	}
}
