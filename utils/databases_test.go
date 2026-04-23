// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

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

// dbMockServer is a stateful httptest server covering every /databases path
// the DatabaseClient touches. Per-test behavior is tuned by swapping fields
// on the returned handle rather than juggling multiple servers. Mirrors the
// pagesMockServer pattern in pages_test.go.
type dbMockServer struct {
	srv      *httptest.Server
	calls    []dbRecordedCall
	mu       chan struct{} // simple mutex via buffered chan
	notFound bool
}

type dbRecordedCall struct {
	method string
	path   string
	body   map[string]interface{}
}

func newDBMockServer(t *testing.T) *dbMockServer {
	t.Helper()
	d := &dbMockServer{mu: make(chan struct{}, 1)}
	d.mu <- struct{}{}
	d.srv = httptest.NewServer(http.HandlerFunc(d.handle))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *dbMockServer) lock()   { <-d.mu }
func (d *dbMockServer) unlock() { d.mu <- struct{}{} }

func (d *dbMockServer) record(method, path string, body map[string]interface{}) {
	d.lock()
	defer d.unlock()
	d.calls = append(d.calls, dbRecordedCall{method: method, path: path, body: body})
}

func (d *dbMockServer) callsSnapshot() []dbRecordedCall {
	d.lock()
	defer d.unlock()
	out := make([]dbRecordedCall, len(d.calls))
	copy(out, d.calls)
	return out
}

func (d *dbMockServer) client() *Client {
	return NewClient("sk_test", WithBaseURL(d.srv.URL))
}

func (d *dbMockServer) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if r.Method == http.MethodPatch || r.Method == http.MethodPost {
		buf, _ := io.ReadAll(r.Body)
		if len(buf) > 0 {
			_ = json.Unmarshal(buf, &body)
		}
	}
	d.record(r.Method, r.URL.Path, body)

	if d.notFound {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found","message":"Could not find database"}`))
		return
	}

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/"):
		id := strings.TrimPrefix(r.URL.Path, "/databases/")
		writeJSONDatabase(w, id, "Source database")

	case r.Method == http.MethodPost && r.URL.Path == "/databases":
		writeJSONDatabase(w, "newDBID", "Created")

	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/databases/"):
		id := strings.TrimPrefix(r.URL.Path, "/databases/")
		writeJSONDatabase(w, id, "Updated")

	default:
		http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
	}
}

func writeJSONDatabase(w http.ResponseWriter, id, title string) {
	payload := map[string]interface{}{
		"object":           "database",
		"id":               id,
		"created_time":     "2026-04-22T10:00:00.000Z",
		"last_edited_time": "2026-04-22T10:00:00.000Z",
		"archived":         false,
		"url":              "https://notion.so/" + id,
		"title": []map[string]interface{}{
			{
				"type":       "text",
				"plain_text": title,
				"text":       map[string]interface{}{"content": title},
			},
		},
		"parent": map[string]interface{}{"type": "page_id", "page_id": "parentPageID"},
		"properties": map[string]interface{}{
			"Name": map[string]interface{}{"id": "title", "name": "Name", "type": "title"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// -------- Tests --------

func TestDatabases_Get_HappyPath(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())

	db, err := dc.Get(context.Background(), "dbID")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if db.ID != "dbID" {
		t.Errorf("db.ID=%q want dbID", db.ID)
	}
	if db.URL == "" {
		t.Error("expected non-empty URL on returned database")
	}
}

func TestDatabases_Get_EmptyID(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())
	if _, err := dc.Get(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty id, got nil")
	}
}

func TestDatabases_Get_NotFound(t *testing.T) {
	m := newDBMockServer(t)
	m.notFound = true
	dc := NewDatabaseClient(m.client())

	_, err := dc.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected 404 error, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error=%q should mention 404", err.Error())
	}
}

func TestDatabases_Create_Minimal(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())

	props := map[string]DatabaseProperty{
		"Name":   {"title": map[string]interface{}{}},
		"Status": {"select": map[string]interface{}{}},
	}
	db, err := dc.Create(context.Background(), CreateDatabaseRequest{
		Parent:     PageParent{PageID: "parentPageID"},
		Title:      "My DB",
		Properties: props,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if db.ID != "newDBID" {
		t.Errorf("db.ID=%q want newDBID", db.ID)
	}

	calls := m.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls)=%d want 1", len(calls))
	}
	if calls[0].method != http.MethodPost || calls[0].path != "/databases" {
		t.Errorf("call=%s %s want POST /databases", calls[0].method, calls[0].path)
	}
	// Title must serialize to a rich-text array.
	title, ok := calls[0].body["title"].([]interface{})
	if !ok || len(title) == 0 {
		t.Fatalf("expected title rich-text array, got %v", calls[0].body["title"])
	}
	// Properties must pass through.
	gotProps, ok := calls[0].body["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %v", calls[0].body["properties"])
	}
	if _, ok := gotProps["Name"]; !ok {
		t.Error("expected Name property in request body")
	}
	// Parent must carry page_id.
	parent, ok := calls[0].body["parent"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected parent map, got %v", calls[0].body["parent"])
	}
	if parent["page_id"] != "parentPageID" {
		t.Errorf("parent.page_id=%v want parentPageID", parent["page_id"])
	}
}

func TestDatabases_Create_NoParent(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())
	_, err := dc.Create(context.Background(), CreateDatabaseRequest{Title: "x"})
	if err == nil {
		t.Fatal("expected error when parent missing")
	}
	if len(m.callsSnapshot()) != 0 {
		t.Error("expected no HTTP calls when parent missing")
	}
}

func TestDatabases_Update_TitleOnly(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())

	db, err := dc.Update(context.Background(), "dbID", UpdateDatabaseRequest{Title: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if db.ID != "dbID" {
		t.Errorf("db.ID=%q want dbID", db.ID)
	}

	calls := m.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls)=%d want 1", len(calls))
	}
	if calls[0].method != http.MethodPatch || calls[0].path != "/databases/dbID" {
		t.Errorf("call=%s %s want PATCH /databases/dbID", calls[0].method, calls[0].path)
	}
	if _, ok := calls[0].body["title"]; !ok {
		t.Error("title-only update should include title in body")
	}
	if _, ok := calls[0].body["properties"]; ok {
		t.Error("title-only update should not include properties")
	}
}

func TestDatabases_Update_PropertiesOnly(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())

	_, err := dc.Update(context.Background(), "dbID", UpdateDatabaseRequest{
		Properties: map[string]DatabaseProperty{
			"Priority": {"select": map[string]interface{}{}},
		},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	calls := m.callsSnapshot()
	if _, ok := calls[0].body["title"]; ok {
		t.Error("properties-only update should not include title")
	}
	if _, ok := calls[0].body["properties"]; !ok {
		t.Error("expected properties in body")
	}
}

func TestDatabases_Update_Empty(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())
	if _, err := dc.Update(context.Background(), "dbID", UpdateDatabaseRequest{}); err == nil {
		t.Fatal("expected error when no fields provided")
	}
	if len(m.callsSnapshot()) != 0 {
		t.Error("expected no HTTP calls when update body would be empty")
	}
}

func TestDatabases_Update_EmptyID(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())
	if _, err := dc.Update(context.Background(), "", UpdateDatabaseRequest{Title: "x"}); err == nil {
		t.Fatal("expected error on empty id")
	}
}

// TestDatabases_MissingAPIKey asserts that every HTTP-calling method on
// DatabaseClient refuses to issue a request when the underlying Client has
// an empty API key, and returns ErrMissingAPIKey (wrapped). Mirrors the
// PageClient missing-key test so new callers inherit the same guarantee.
func TestDatabases_MissingAPIKey(t *testing.T) {
	m := newDBMockServer(t)
	c := NewClient("", WithBaseURL(m.srv.URL))
	dc := NewDatabaseClient(c)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "Get", call: func() error { _, err := dc.Get(ctx, "id"); return err }},
		{name: "Create", call: func() error {
			_, err := dc.Create(ctx, CreateDatabaseRequest{Parent: PageParent{PageID: "p"}})
			return err
		}},
		{name: "Update", call: func() error {
			_, err := dc.Update(ctx, "id", UpdateDatabaseRequest{Title: "t"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrMissingAPIKey) {
				t.Errorf("expected errors.Is ErrMissingAPIKey, got %v", err)
			}
		})
	}
	if calls := m.callsSnapshot(); len(calls) != 0 {
		t.Errorf("expected zero HTTP calls, got %d: %+v", len(calls), calls)
	}
}
