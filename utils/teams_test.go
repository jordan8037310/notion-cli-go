package utils

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTeamsMock returns an httptest server that answers GET /v1/teams.
// Pagination is exercised via the start_cursor query parameter: the
// first call (cursor="") returns one page with HasMore=true; the
// follow-up call (cursor="cursor-1") returns the final page.
func newTeamsMock(t *testing.T) *httptest.Server {
	t.Helper()
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/teams" {
			// Assert Notion-Version header is propagated so the
			// real endpoint is being addressed.
			if r.Header.Get("Notion-Version") == "" {
				http.Error(w, `{"object":"error","code":"missing_version"}`, http.StatusBadRequest)
				return
			}
			cursor := r.URL.Query().Get("start_cursor")
			if cursor == "cursor-1" {
				writeJSON(w, TeamList{
					Object:  "list",
					Results: []Team{{Object: "team", ID: "team-3", Name: "Platform"}},
				})
				return
			}
			writeJSON(w, TeamList{
				Object: "list",
				Results: []Team{
					{Object: "team", ID: "team-1", Name: "Marketing"},
					{Object: "team", ID: "team-2", Name: "Sales"},
				},
				HasMore:    true,
				NextCursor: "cursor-1",
			})
			return
		}
		http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
	}))
}

// newTeamClient builds a TeamClient pointed at the given httptest server.
func newTeamClient(srv *httptest.Server) *TeamClient {
	return NewTeamClient(NewClient("test-key", WithBaseURL(srv.URL)))
}

// TestNewTeamClient is a smoke test for the constructor; it exists so the
// gap-check script sees a matching Test function for the exported
// NewTeamClient symbol.
func TestNewTeamClient(t *testing.T) {
	c := NewClient("k", WithBaseURL("http://example"))
	got := NewTeamClient(c)
	if got == nil || got.c != c {
		t.Fatalf("NewTeamClient: %+v", got)
	}
}

// TestTeams_ListPage_SinglePage covers the happy-path single-page
// response (HasMore=false, cursor empty).
func TestTeams_ListPage_SinglePage(t *testing.T) {
	srv := newTeamsMock(t)
	defer srv.Close()

	got, err := newTeamClient(srv).ListPage(context.Background(), "cursor-1")
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].ID != "team-3" {
		t.Errorf("ListPage: unexpected results %+v", got.Results)
	}
	if got.HasMore {
		t.Errorf("ListPage: HasMore = true, want false on final page")
	}
}

// TestTeams_ListPage_FirstPage verifies an empty cursor returns the
// first page and signals HasMore=true with a follow-up cursor.
func TestTeams_ListPage_FirstPage(t *testing.T) {
	srv := newTeamsMock(t)
	defer srv.Close()

	got, err := newTeamClient(srv).ListPage(context.Background(), "")
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if !got.HasMore || got.NextCursor != "cursor-1" {
		t.Errorf("ListPage: want HasMore=true NextCursor=cursor-1, got %+v", got)
	}
	if len(got.Results) != 2 {
		t.Errorf("ListPage: want 2 results, got %+v", got.Results)
	}
}

// TestTeams_List_FollowsPagination walks pagination end-to-end and
// verifies every result is collected in order.
func TestTeams_List_FollowsPagination(t *testing.T) {
	srv := newTeamsMock(t)
	defer srv.Close()

	got, err := newTeamClient(srv).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List: want 3 teams across pages, got %d: %+v", len(got), got)
	}
	want := []string{"team-1", "team-2", "team-3"}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("List[%d].ID = %q, want %q", i, got[i].ID, id)
		}
	}
}

// TestTeams_List_MissingAPIKey asserts an unconfigured Client is caught
// before any HTTP call and surfaces ErrMissingAPIKey.
func TestTeams_List_MissingAPIKey(t *testing.T) {
	// Base URL can be anything — the auth check should short-circuit.
	client := NewTeamClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	got, err := client.List(context.Background())
	if err == nil {
		t.Fatalf("List: want error, got %+v", got)
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("List: want errors.Is ErrMissingAPIKey, got %v", err)
	}
	if got != nil {
		t.Errorf("List: want nil teams, got %+v", got)
	}
}

// TestTeams_ListPage_MissingAPIKey covers the per-page accessor for parity.
func TestTeams_ListPage_MissingAPIKey(t *testing.T) {
	client := NewTeamClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	got, err := client.ListPage(context.Background(), "")
	if err == nil {
		t.Fatalf("ListPage: want error, got %+v", got)
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("ListPage: want errors.Is ErrMissingAPIKey, got %v", err)
	}
	if got != nil {
		t.Errorf("ListPage: want nil page, got %+v", got)
	}
}

// TestTeams_ListPage_APIError verifies a non-2xx response is surfaced as
// a wrapped error (no panic, no nil deref).
func TestTeams_ListPage_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error","code":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := newTeamClient(srv).ListPage(context.Background(), "")
	if err == nil {
		t.Fatal("ListPage: want error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("ListPage error = %q; want to mention 401", err.Error())
	}
}

// TestTeams_ListPage_EscapesCursor asserts cursor values with special
// characters are URL-escaped so the wire path stays well-formed.
func TestTeams_ListPage_EscapesCursor(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TeamList{Object: "list"})
	}))
	defer srv.Close()

	if _, err := newTeamClient(srv).ListPage(context.Background(), "abc def+gh"); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if !strings.Contains(gotRaw, "start_cursor=abc+def%2Bgh") && !strings.Contains(gotRaw, "start_cursor=abc%20def%2Bgh") {
		t.Errorf("ListPage: raw query = %q; want escaped cursor", gotRaw)
	}
}
