package utils

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
			// Pin Notion-Version to the package constant so any future
			// drift between the client and this test fails loudly here
			// instead of silently slipping through CI.
			if got := r.Header.Get("Notion-Version"); got != NotionAPIVersion {
				http.Error(w, `{"object":"error","code":"wrong_version"}`, http.StatusBadRequest)
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

// TestTeams_ListPage_StubReturnsErrTeamsNotSupported pins the post-#37
// contract: every network-issuing TeamClient method short-circuits with
// ErrTeamsNotSupported instead of letting the live API 400 with
// invalid_request_url. The mock server stays in place so a future
// restoration (when Notion re-exposes /v1/teams or its successor) only
// needs to flip the return statement back to the network path; the
// pagination/cursor scaffolding here is the documentation for that
// future state.
func TestTeams_ListPage_StubReturnsErrTeamsNotSupported(t *testing.T) {
	srv := newTeamsMock(t)
	defer srv.Close()

	got, err := newTeamClient(srv).ListPage(context.Background(), "")
	if err == nil {
		t.Fatalf("ListPage: expected ErrTeamsNotSupported, got %+v", got)
	}
	if !errors.Is(err, ErrTeamsNotSupported) {
		t.Errorf("ListPage: err = %v; want errors.Is ErrTeamsNotSupported", err)
	}
	if got != nil {
		t.Errorf("ListPage: want nil page on stub, got %+v", got)
	}
}

// TestTeams_List_StubReturnsErrTeamsNotSupported is the same contract
// applied to the paginated List method.
func TestTeams_List_StubReturnsErrTeamsNotSupported(t *testing.T) {
	srv := newTeamsMock(t)
	defer srv.Close()

	got, err := newTeamClient(srv).List(context.Background())
	if err == nil {
		t.Fatalf("List: expected ErrTeamsNotSupported, got %+v", got)
	}
	if !errors.Is(err, ErrTeamsNotSupported) {
		t.Errorf("List: err = %v; want errors.Is ErrTeamsNotSupported", err)
	}
	if got != nil {
		t.Errorf("List: want nil teams on stub, got %+v", got)
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

// TestTeams_ListPage_StubBeforeNetwork confirms the stub short-circuits
// before any HTTP call — the mock server registers zero hits regardless
// of the cursor value. Restoration of the network path (when Notion
// exposes a working endpoint) should flip this assertion: the mock
// should see exactly one hit per ListPage with the cursor URL-escaped.
func TestTeams_ListPage_StubBeforeNetwork(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TeamList{Object: "list"})
	}))
	defer srv.Close()

	_, err := newTeamClient(srv).ListPage(context.Background(), "abc def+gh")
	if !errors.Is(err, ErrTeamsNotSupported) {
		t.Errorf("ListPage: err = %v; want errors.Is ErrTeamsNotSupported", err)
	}
	if hits != 0 {
		t.Errorf("ListPage made %d HTTP call(s); stub must short-circuit before the network", hits)
	}
}
