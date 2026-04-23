package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newUsersMock returns an httptest server that answers the /v1/users
// endpoints the UserClient hits. Behavior is keyed off of the request
// path and, for the list endpoint, the start_cursor query parameter so
// pagination can be exercised deterministically.
func newUsersMock(t *testing.T) *httptest.Server {
	t.Helper()

	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// /v1/users list endpoint
		case r.Method == http.MethodGet && r.URL.Path == "/users":
			cursor := r.URL.Query().Get("start_cursor")
			page := r.URL.Query().Get("page")
			// The "page" query param lets a test opt into pagination
			// without having to re-encode fixtures for every case.
			switch page {
			case "empty":
				writeJSON(w, UserList{Object: "list", Results: []User{}})
			case "paginated":
				if cursor == "cursor-1" {
					writeJSON(w, UserList{
						Object:  "list",
						Results: []User{{Object: "user", ID: "u3", Type: "bot", Name: "Integration", Bot: &UserBot{WorkspaceName: "Acme"}}},
					})
					return
				}
				writeJSON(w, UserList{
					Object: "list",
					Results: []User{
						{Object: "user", ID: "u1", Type: "person", Name: "Ada", Person: &UserPerson{Email: "ada@example.com"}},
						{Object: "user", ID: "u2", Type: "person", Name: "Grace", Person: &UserPerson{Email: "grace@example.com"}},
					},
					HasMore:    true,
					NextCursor: "cursor-1",
				})
			default:
				// Default: single-page listing with one user.
				writeJSON(w, UserList{
					Object:  "list",
					Results: []User{{Object: "user", ID: "u1", Type: "person", Name: "Ada", Person: &UserPerson{Email: "ada@example.com"}}},
				})
			}

		// /v1/users/me
		case r.Method == http.MethodGet && r.URL.Path == "/users/me":
			writeJSON(w, User{Object: "user", ID: "bot-1", Type: "bot", Name: "CLI bot", Bot: &UserBot{WorkspaceName: "Acme"}})

		// /v1/users/{id} — match by exact suffix rather than prefix so the
		// 404 path is easy to exercise.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/users/"):
			id := strings.TrimPrefix(r.URL.Path, "/users/")
			if id == "u1" {
				writeJSON(w, User{Object: "user", ID: "u1", Type: "person", Name: "Ada", Person: &UserPerson{Email: "ada@example.com"}})
				return
			}
			http.Error(w, `{"object":"error","code":"object_not_found"}`, http.StatusNotFound)

		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
}

// newUserClient builds a UserClient pointed at the given httptest server.
func newUserClient(srv *httptest.Server) *UserClient {
	return NewUserClient(NewClient("test-key", WithBaseURL(srv.URL)))
}

// TestList_Empty exercises the pagination-walk on an empty
// workspace. The returned slice must be non-nil so downstream JSON
// callers emit [] rather than null.
func TestList_Empty(t *testing.T) {
	srv := newUsersMock(t)
	defer srv.Close()

	// Wire up a *Client whose base URL appends ?page=empty to every
	// request path. We do this by constructing a dedicated mock for
	// this test with the empty branch pinned; reusing the existing
	// mock via query would require rewriting paths, which is brittle.
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UserList{Object: "list", Results: []User{}})
	}))
	defer emptySrv.Close()

	client := NewUserClient(NewClient("test-key", WithBaseURL(emptySrv.URL)))
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List returned nil slice; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len=%d want=0", len(got))
	}
}

// TestList_SinglePage covers the non-paginated happy path.
func TestList_SinglePage(t *testing.T) {
	srv := newUsersMock(t)
	defer srv.Close()

	client := newUserClient(srv)
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 user, got %d", len(got))
	}
	if got[0].ID != "u1" || got[0].Type != "person" || got[0].Person == nil || got[0].Person.Email != "ada@example.com" {
		t.Errorf("unexpected user: %+v", got[0])
	}
}

// TestList_Paginated asserts the client walks multiple pages
// and concatenates the results in order.
func TestList_Paginated(t *testing.T) {
	// Mock that dispatches on start_cursor explicitly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		cursor := r.URL.Query().Get("start_cursor")
		if cursor == "cursor-1" {
			_ = enc.Encode(UserList{
				Object:  "list",
				Results: []User{{Object: "user", ID: "u3", Type: "bot", Name: "Integration", Bot: &UserBot{WorkspaceName: "Acme"}}},
			})
			return
		}
		_ = enc.Encode(UserList{
			Object: "list",
			Results: []User{
				{Object: "user", ID: "u1", Type: "person", Name: "Ada"},
				{Object: "user", ID: "u2", Type: "person", Name: "Grace"},
			},
			HasMore:    true,
			NextCursor: "cursor-1",
		})
	}))
	defer srv.Close()

	client := NewUserClient(NewClient("test-key", WithBaseURL(srv.URL)))
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 users (2 + 1), got %d", len(got))
	}
	wantIDs := []string{"u1", "u2", "u3"}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("got[%d].ID=%q want=%q", i, got[i].ID, id)
		}
	}
}

// TestListPage_CursorEncoded asserts ListPage forwards the
// cursor verbatim on the wire (URL-encoded).
func TestListPage_CursorEncoded(t *testing.T) {
	var gotCursor string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCursor = r.URL.Query().Get("start_cursor")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UserList{Object: "list", Results: []User{}})
	}))
	defer srv.Close()

	client := NewUserClient(NewClient("test-key", WithBaseURL(srv.URL)))
	if _, err := client.ListPage(context.Background(), "abc xyz"); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if gotCursor != "abc xyz" {
		t.Errorf("gotCursor=%q want=%q", gotCursor, "abc xyz")
	}
}

// TestGet_Happy covers the /v1/users/{id} happy path.
func TestGet_Happy(t *testing.T) {
	srv := newUsersMock(t)
	defer srv.Close()

	client := newUserClient(srv)
	got, err := client.Get(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "u1" {
		t.Errorf("ID=%q want=u1", got.ID)
	}
	if got.Person == nil || got.Person.Email != "ada@example.com" {
		t.Errorf("unexpected Person: %+v", got.Person)
	}
}

// TestGet_NotFound verifies a 404 produces an error rather
// than a panic or nil user.
func TestGet_NotFound(t *testing.T) {
	srv := newUsersMock(t)
	defer srv.Close()

	client := newUserClient(srv)
	got, err := client.Get(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatalf("Get: want error, got user %+v", got)
	}
	if got != nil {
		t.Errorf("Get: want nil user on error, got %+v", got)
	}
}

// TestGet_RequiresID rejects an empty id before hitting the
// network.
func TestGet_RequiresID(t *testing.T) {
	client := NewUserClient(NewClient("test-key", WithBaseURL("http://127.0.0.1:0")))
	if _, err := client.Get(context.Background(), ""); err == nil {
		t.Fatal("Get(\"\"): want error, got nil")
	}
}

// TestMe_Happy covers /v1/users/me and asserts the bot variant is
// decoded into the Bot pointer.
func TestMe_Happy(t *testing.T) {
	srv := newUsersMock(t)
	defer srv.Close()

	client := newUserClient(srv)
	got, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got.Type != "bot" {
		t.Errorf("Type=%q want=bot", got.Type)
	}
	if got.Bot == nil || got.Bot.WorkspaceName != "Acme" {
		t.Errorf("unexpected Bot: %+v", got.Bot)
	}
}

// TestMe_HTTPError asserts a non-2xx response from /users/me
// is surfaced as an error.
func TestMe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewUserClient(NewClient("test-key", WithBaseURL(srv.URL)))
	if _, err := client.Me(context.Background()); err == nil {
		t.Fatal("Me: want error on 401, got nil")
	}
}
