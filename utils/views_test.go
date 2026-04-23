package utils

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestNewViewClient is a smoke test for the constructor; it exists so
// the gap-check script sees a matching Test function for the exported
// NewViewClient symbol.
func TestNewViewClient(t *testing.T) {
	c := NewClient("k", WithBaseURL("http://example"))
	got := NewViewClient(c)
	if got == nil || got.c != c {
		t.Fatalf("NewViewClient: %+v", got)
	}
}

// TestViews_Create_NotSupported asserts that a fully-valid create
// request surfaces ErrViewsNotSupported (the stub path) via errors.Is
// so callers can branch on it without string-matching.
func TestViews_Create_NotSupported(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	req := CreateViewRequest{
		DatabaseID: "db-id",
		Name:       "My View",
		Type:       "table",
	}
	got, err := client.Create(context.Background(), req)
	if err == nil {
		t.Fatalf("Create: want error, got view %+v", got)
	}
	if !errors.Is(err, ErrViewsNotSupported) {
		t.Errorf("Create: want errors.Is ErrViewsNotSupported, got %v", err)
	}
	if got != nil {
		t.Errorf("Create: want nil view, got %+v", got)
	}
}

// TestViews_Create_AllValidTypes exercises every type in ValidViewTypes
// and confirms each still dispatches to the stub (ErrViewsNotSupported).
// This locks in the vocabulary so a future refactor can't silently drop
// a supported type.
func TestViews_Create_AllValidTypes(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	for _, vt := range ValidViewTypes {
		t.Run(vt, func(t *testing.T) {
			req := CreateViewRequest{
				DatabaseID: "db-id",
				Name:       "n",
				Type:       vt,
			}
			_, err := client.Create(context.Background(), req)
			if !errors.Is(err, ErrViewsNotSupported) {
				t.Errorf("Create(%s): want ErrViewsNotSupported, got %v", vt, err)
			}
		})
	}
}

// TestViews_Create_ValidationBeforeStub asserts that a missing required
// field produces a precise validation error rather than the stub
// sentinel. Ordering matters: bad input should not be masked by the
// "views not supported" message.
func TestViews_Create_ValidationBeforeStub(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))

	tests := []struct {
		name    string
		req     CreateViewRequest
		wantSub string
	}{
		{
			name:    "missing database_id",
			req:     CreateViewRequest{Name: "n", Type: "table"},
			wantSub: "database_id",
		},
		{
			name:    "missing name",
			req:     CreateViewRequest{DatabaseID: "d", Type: "table"},
			wantSub: "name",
		},
		{
			name:    "missing type",
			req:     CreateViewRequest{DatabaseID: "d", Name: "n"},
			wantSub: "type is required",
		},
		{
			name:    "invalid type",
			req:     CreateViewRequest{DatabaseID: "d", Name: "n", Type: "bogus"},
			wantSub: "invalid type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.Create(context.Background(), tt.req)
			if err == nil {
				t.Fatalf("Create: want error, got %+v", got)
			}
			if errors.Is(err, ErrViewsNotSupported) {
				t.Errorf("Create: validation error swallowed by stub sentinel: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Create error = %q; want substring %q", err.Error(), tt.wantSub)
			}
			if got != nil {
				t.Errorf("Create: want nil view on validation error, got %+v", got)
			}
		})
	}
}

// TestViews_Create_MissingAPIKey asserts that a client built without an
// API key surfaces ErrMissingAPIKey rather than ErrViewsNotSupported.
// Validation of the request itself still runs first.
func TestViews_Create_MissingAPIKey(t *testing.T) {
	client := NewViewClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	req := CreateViewRequest{
		DatabaseID: "db-id",
		Name:       "n",
		Type:       "table",
	}
	_, err := client.Create(context.Background(), req)
	if err == nil {
		t.Fatal("Create: want error for empty API key")
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Create: want ErrMissingAPIKey, got %v", err)
	}
}

// TestViews_Update_NotSupported asserts that a fully-valid update
// request surfaces ErrViewsNotSupported.
func TestViews_Update_NotSupported(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	got, err := client.Update(context.Background(), "view-id", UpdateViewRequest{Name: "renamed"})
	if err == nil {
		t.Fatalf("Update: want error, got %+v", got)
	}
	if !errors.Is(err, ErrViewsNotSupported) {
		t.Errorf("Update: want errors.Is ErrViewsNotSupported, got %v", err)
	}
	if got != nil {
		t.Errorf("Update: want nil view, got %+v", got)
	}
}

// TestViews_Update_ValidationBeforeStub asserts update-time validation
// runs ahead of the stub sentinel: a bad id or empty request should
// produce a precise error, not "views not supported".
func TestViews_Update_ValidationBeforeStub(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))

	tests := []struct {
		name    string
		id      string
		req     UpdateViewRequest
		wantSub string
	}{
		{
			name:    "missing id",
			id:      "",
			req:     UpdateViewRequest{Name: "n"},
			wantSub: "id is required",
		},
		{
			name:    "empty request",
			id:      "view-id",
			req:     UpdateViewRequest{},
			wantSub: "at least one of name or config is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.Update(context.Background(), tt.id, tt.req)
			if err == nil {
				t.Fatalf("Update: want error, got %+v", got)
			}
			if errors.Is(err, ErrViewsNotSupported) {
				t.Errorf("Update: validation error swallowed by stub sentinel: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Update error = %q; want substring %q", err.Error(), tt.wantSub)
			}
			if got != nil {
				t.Errorf("Update: want nil view on validation error, got %+v", got)
			}
		})
	}
}

// TestViews_Update_ConfigOnly verifies that a config-only update (no
// name) passes validation and still dispatches to the stub.
func TestViews_Update_ConfigOnly(t *testing.T) {
	client := NewViewClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	req := UpdateViewRequest{Config: map[string]interface{}{"sort": "asc"}}
	_, err := client.Update(context.Background(), "view-id", req)
	if !errors.Is(err, ErrViewsNotSupported) {
		t.Errorf("Update: want ErrViewsNotSupported for config-only request, got %v", err)
	}
}

// TestViews_Update_MissingAPIKey asserts that a client built without an
// API key surfaces ErrMissingAPIKey (after validation).
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

// TestViews_ErrViewsNotSupported_MessageReferences11 asserts the error
// message mentions the pinned version and issue #11 so users can find
// the tracking issue without reading the code.
func TestViews_ErrViewsNotSupported_MessageReferences11(t *testing.T) {
	msg := ErrViewsNotSupported.Error()
	if !strings.Contains(msg, NotionAPIVersion) {
		t.Errorf("ErrViewsNotSupported message = %q; want it to mention %q", msg, NotionAPIVersion)
	}
	if !strings.Contains(msg, "#11") {
		t.Errorf("ErrViewsNotSupported message = %q; want it to mention %q", msg, "#11")
	}
}

// TestValidate_CreateViewRequest is a direct exercise of the request's
// Validate method so gap-check sees coverage for the exported method
// independently of ViewClient.Create. Named TestValidate_* (vs the
// TestViews_* prefix used elsewhere in this file) so the gap-check
// script's regex matches the bare Validate function name — no other
// Validate methods exist in this module so there is no collision risk.
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
// Named TestValidate_* for the same gap-check reason documented on
// TestValidate_CreateViewRequest above.
func TestValidate_UpdateViewRequest(t *testing.T) {
	if err := (UpdateViewRequest{Name: "n"}).Validate(); err != nil {
		t.Errorf("Validate name-only: %v", err)
	}
	if err := (UpdateViewRequest{Config: map[string]interface{}{"k": "v"}}).Validate(); err != nil {
		t.Errorf("Validate config-only: %v", err)
	}
	if err := (UpdateViewRequest{}).Validate(); err == nil {
		t.Errorf("Validate empty: want error, got nil")
	}
}
