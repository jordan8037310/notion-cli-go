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

// newViewsMock returns an httptest server that answers the views
// endpoints used by ViewClient. The captured body is returned via the
// bodies slice so tests can assert on the wire shape.
func newViewsMock(t *testing.T, bodies *[]string) *httptest.Server {
	t.Helper()
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture request body for wire-shape assertions.
		body, _ := io.ReadAll(r.Body)
		if bodies != nil {
			*bodies = append(*bodies, string(body))
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/data_sources/db-id/views":
			writeJSON(w, View{
				Object: "view",
				ID:     "view-123",
				Name:   "My View",
				Type:   "table",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/views/view-123":
			writeJSON(w, View{
				Object: "view",
				ID:     "view-123",
				Name:   "Renamed",
				Type:   "table",
			})
		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
}

// newViewClient builds a ViewClient pointed at the given httptest server.
func newViewClient(srv *httptest.Server) *ViewClient {
	return NewViewClient(NewClient("test-key", WithBaseURL(srv.URL)))
}

// TestNewViewClient is a smoke test for the constructor.
func TestNewViewClient(t *testing.T) {
	c := NewClient("k", WithBaseURL("http://example"))
	got := NewViewClient(c)
	if got == nil || got.c != c {
		t.Fatalf("NewViewClient: %+v", got)
	}
}

// TestViews_Create_HappyPath posts a valid create and decodes the response.
func TestViews_Create_HappyPath(t *testing.T) {
	var bodies []string
	srv := newViewsMock(t, &bodies)
	defer srv.Close()

	req := CreateViewRequest{
		DatabaseID: "db-id",
		Name:       "My View",
		Type:       "table",
	}
	got, err := newViewClient(srv).Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got == nil || got.ID != "view-123" {
		t.Errorf("Create: want view-123, got %+v", got)
	}
	if len(bodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(bodies))
	}
	// The wire body must NOT include database_id (that's a path param)
	// but must include name and type.
	if strings.Contains(bodies[0], "database_id") {
		t.Errorf("create body should not include database_id: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], `"name":"My View"`) {
		t.Errorf("create body missing name: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], `"type":"table"`) {
		t.Errorf("create body missing type: %s", bodies[0])
	}
}

// TestViews_Create_WithConfig verifies that Config bytes are forwarded
// verbatim (no re-marshaling / key-reordering).
func TestViews_Create_WithConfig(t *testing.T) {
	var bodies []string
	srv := newViewsMock(t, &bodies)
	defer srv.Close()

	cfg := json.RawMessage(`{"sort":"asc","filter":{"status":"done"}}`)
	req := CreateViewRequest{
		DatabaseID: "db-id",
		Name:       "My View",
		Type:       "table",
		Config:     cfg,
	}
	if _, err := newViewClient(srv).Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(bodies[0], `"sort":"asc"`) {
		t.Errorf("create body missing config bytes: %s", bodies[0])
	}
}

// TestViews_Create_AllValidTypes exercises every type in ValidViewTypes.
func TestViews_Create_AllValidTypes(t *testing.T) {
	for _, vt := range ValidViewTypes {
		t.Run(vt, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(View{Object: "view", ID: "v", Name: "n", Type: vt})
			}))
			defer srv.Close()

			req := CreateViewRequest{DatabaseID: "d", Name: "n", Type: vt}
			if _, err := newViewClient(srv).Create(context.Background(), req); err != nil {
				t.Errorf("Create(%s): %v", vt, err)
			}
		})
	}
}

// TestViews_Create_ValidationErrors asserts bad input returns a precise
// validation error without touching the wire.
func TestViews_Create_ValidationErrors(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))

	tests := []struct {
		name    string
		req     CreateViewRequest
		wantSub string
	}{
		{"missing database_id", CreateViewRequest{Name: "n", Type: "table"}, "database_id"},
		{"missing name", CreateViewRequest{DatabaseID: "d", Type: "table"}, "name"},
		{"missing type", CreateViewRequest{DatabaseID: "d", Name: "n"}, "type is required"},
		{"invalid type", CreateViewRequest{DatabaseID: "d", Name: "n", Type: "bogus"}, "invalid type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.Create(context.Background(), tt.req)
			if err == nil {
				t.Fatalf("Create: want error, got %+v", got)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Create error = %q; want substring %q", err.Error(), tt.wantSub)
			}
			if got != nil {
				t.Errorf("Create: want nil view, got %+v", got)
			}
		})
	}
}

// TestViews_Create_MissingAPIKey asserts ErrMissingAPIKey surfaces after
// validation.
func TestViews_Create_MissingAPIKey(t *testing.T) {
	client := NewViewClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	req := CreateViewRequest{DatabaseID: "db-id", Name: "n", Type: "table"}
	_, err := client.Create(context.Background(), req)
	if err == nil {
		t.Fatal("Create: want error for empty API key")
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Create: want ErrMissingAPIKey, got %v", err)
	}
}

// TestViews_Update_HappyPath patches a view name and decodes the response.
func TestViews_Update_HappyPath(t *testing.T) {
	var bodies []string
	srv := newViewsMock(t, &bodies)
	defer srv.Close()

	got, err := newViewClient(srv).Update(context.Background(), "view-123", UpdateViewRequest{Name: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got == nil || got.Name != "Renamed" {
		t.Errorf("Update: want Renamed, got %+v", got)
	}
	if !strings.Contains(bodies[0], `"name":"Renamed"`) {
		t.Errorf("update body missing name: %s", bodies[0])
	}
}

// TestViews_Update_ConfigOnly verifies a config-only update passes
// validation and is sent with no "name" key.
func TestViews_Update_ConfigOnly(t *testing.T) {
	var bodies []string
	srv := newViewsMock(t, &bodies)
	defer srv.Close()

	req := UpdateViewRequest{Config: json.RawMessage(`{"sort":"asc"}`)}
	if _, err := newViewClient(srv).Update(context.Background(), "view-123", req); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if strings.Contains(bodies[0], `"name"`) {
		t.Errorf("update body should not include name: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], `"sort":"asc"`) {
		t.Errorf("update body missing config bytes: %s", bodies[0])
	}
}

// TestViews_Update_ValidationErrors asserts bad input rejects before
// touching the wire.
func TestViews_Update_ValidationErrors(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))

	tests := []struct {
		name    string
		id      string
		req     UpdateViewRequest
		wantSub string
	}{
		{"missing id", "", UpdateViewRequest{Name: "n"}, "id is required"},
		{"empty request", "view-id", UpdateViewRequest{}, "at least one of name or config is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.Update(context.Background(), tt.id, tt.req)
			if err == nil {
				t.Fatalf("Update: want error, got %+v", got)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Update error = %q; want substring %q", err.Error(), tt.wantSub)
			}
			if got != nil {
				t.Errorf("Update: want nil view, got %+v", got)
			}
		})
	}
}

// TestViews_Update_MissingAPIKey asserts ErrMissingAPIKey surfaces after
// validation.
func TestViews_Update_MissingAPIKey(t *testing.T) {
	client := NewViewClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	_, err := client.Update(context.Background(), "view-id", UpdateViewRequest{Name: "n"})
	if err == nil {
		t.Fatal("Update: want error for empty API key")
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Update: want ErrMissingAPIKey, got %v", err)
	}
}

// TestViews_Create_APIError surfaces a non-2xx response as a wrapped error.
func TestViews_Create_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error","code":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	req := CreateViewRequest{DatabaseID: "d", Name: "n", Type: "table"}
	_, err := newViewClient(srv).Create(context.Background(), req)
	if err == nil {
		t.Fatal("Create: want error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Create error = %q; want to mention 401", err.Error())
	}
}

// TestValidate_CreateViewRequest is a direct exercise of the request's
// Validate method so gap-check sees coverage for the exported method.
func TestValidate_CreateViewRequest(t *testing.T) {
	ok := CreateViewRequest{DatabaseID: "d", Name: "n", Type: "board"}
	if err := ok.Validate(); err != nil {
		t.Errorf("Validate(%+v) = %v, want nil", ok, err)
	}
	bad := CreateViewRequest{DatabaseID: "d", Name: "n", Type: "nope"}
	if err := bad.Validate(); err == nil {
		t.Errorf("Validate(%+v) = nil, want error", bad)
	}
}

// TestValidate_UpdateViewRequest exercises the update validator.
func TestValidate_UpdateViewRequest(t *testing.T) {
	if err := (UpdateViewRequest{Name: "n"}).Validate(); err != nil {
		t.Errorf("Validate name-only: %v", err)
	}
	if err := (UpdateViewRequest{Config: json.RawMessage(`{"k":"v"}`)}).Validate(); err != nil {
		t.Errorf("Validate config-only: %v", err)
	}
	emptyCases := []string{``, `null`, `{}`, `[]`, `  {} `}
	for _, c := range emptyCases {
		got := UpdateViewRequest{Config: json.RawMessage(c)}
		if err := got.Validate(); err == nil {
			t.Errorf("Validate empty-config %q: want error, got nil", c)
		}
	}
	if err := (UpdateViewRequest{}).Validate(); err == nil {
		t.Errorf("Validate empty: want error, got nil")
	}
}
