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

	// queryPages is a per-call script for POST /databases/{id}/query. The
	// server returns queryPages[i] for the i-th query call; when the script
	// is exhausted, the final entry repeats. Each entry's HasMore/NextCursor
	// drives the client's pagination walk.
	queryPages []QueryResponse
	queryIdx   int
	// lastQueryBody is the most recent decoded POST body on /query so tests
	// can assert filter, sorts, start_cursor, page_size were sent correctly.
	lastQueryBody map[string]interface{}
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
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query"):
		d.lock()
		d.lastQueryBody = body
		idx := d.queryIdx
		if idx >= len(d.queryPages) {
			idx = len(d.queryPages) - 1
		}
		page := QueryResponse{Object: "list"}
		if idx >= 0 {
			page = d.queryPages[idx]
		}
		d.queryIdx++
		d.unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)

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
		"in_trash":         false,
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

// samplePage returns a minimal Page for use in query-response fixtures.
func samplePage(id string) Page {
	return Page{
		Object:         "page",
		ID:             id,
		CreatedTime:    "2026-04-22T10:00:00.000Z",
		LastEditedTime: "2026-04-22T10:00:00.000Z",
		URL:            "https://notion.so/" + id,
		Parent:         PageParent{Type: "database_id", DatabaseID: "dbID"},
		Properties:     map[string]interface{}{},
	}
}

// TestQuery_SinglePage exercises a one-page (HasMore=false) query with a
// filter and sort payload. Asserts the filter/sorts are passed through
// untouched and cursor/pageSize are not included when zero-valued.
func TestQuery_SinglePage(t *testing.T) {
	m := newDBMockServer(t)
	m.queryPages = []QueryResponse{{
		Object:  "list",
		Results: []Page{samplePage("p1"), samplePage("p2")},
		HasMore: false,
	}}
	dc := NewDatabaseClient(m.client())

	filter := json.RawMessage(`{"property":"Status","status":{"equals":"Done"}}`)
	sort := json.RawMessage(`[{"property":"Priority","direction":"descending"}]`)

	resp, err := dc.Query(context.Background(), "dbID", filter, sort, "", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Errorf("len(results)=%d want 2", len(resp.Results))
	}

	m.lock()
	body := m.lastQueryBody
	m.unlock()

	// Filter round-trips as an object with property=Status.
	gotFilter, ok := body["filter"].(map[string]interface{})
	if !ok {
		t.Fatalf("filter missing or wrong type: %v", body["filter"])
	}
	if gotFilter["property"] != "Status" {
		t.Errorf("filter.property=%v want Status", gotFilter["property"])
	}
	// Sorts round-trip as an array.
	if _, ok := body["sorts"].([]interface{}); !ok {
		t.Errorf("sorts missing or not an array: %v", body["sorts"])
	}
	// start_cursor/page_size omitted when empty.
	if _, ok := body["start_cursor"]; ok {
		t.Error("start_cursor should be omitted when cursor empty")
	}
	if _, ok := body["page_size"]; ok {
		t.Error("page_size should be omitted when zero")
	}
}

// TestQuery_EmptyID asserts the id guard runs before any HTTP call.
func TestQuery_EmptyID(t *testing.T) {
	m := newDBMockServer(t)
	dc := NewDatabaseClient(m.client())
	if _, err := dc.Query(context.Background(), "", nil, nil, "", 0); err == nil {
		t.Fatal("expected error on empty id")
	}
	if len(m.callsSnapshot()) != 0 {
		t.Error("expected no HTTP calls when id empty")
	}
}

// TestQuery_NotFound covers 404 error surface on the query endpoint.
func TestQuery_NotFound(t *testing.T) {
	m := newDBMockServer(t)
	m.notFound = true
	dc := NewDatabaseClient(m.client())

	_, err := dc.Query(context.Background(), "missing", nil, nil, "", 0)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error=%q should mention 404", err.Error())
	}
}

// TestQuery_CursorAndPageSize asserts non-zero cursor and pageSize survive
// serialization into the POST body.
func TestQuery_CursorAndPageSize(t *testing.T) {
	m := newDBMockServer(t)
	m.queryPages = []QueryResponse{{Object: "list", Results: []Page{samplePage("p1")}}}
	dc := NewDatabaseClient(m.client())

	_, err := dc.Query(context.Background(), "dbID", nil, nil, "cursor-abc", 25)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	m.lock()
	body := m.lastQueryBody
	m.unlock()
	if body["start_cursor"] != "cursor-abc" {
		t.Errorf("start_cursor=%v want cursor-abc", body["start_cursor"])
	}
	// JSON numbers decode as float64.
	if got, _ := body["page_size"].(float64); int(got) != 25 {
		t.Errorf("page_size=%v want 25", body["page_size"])
	}
}

// TestQueryAll_PaginationAggregates confirms QueryAll follows has_more +
// next_cursor until exhaustion and stitches results in order. It also
// verifies start_cursor is forwarded on the second page.
func TestQueryAll_PaginationAggregates(t *testing.T) {
	m := newDBMockServer(t)
	m.queryPages = []QueryResponse{
		{Object: "list", Results: []Page{samplePage("p1"), samplePage("p2")}, HasMore: true, NextCursor: "cur-2"},
		{Object: "list", Results: []Page{samplePage("p3")}, HasMore: false},
	}
	dc := NewDatabaseClient(m.client())

	all, err := dc.QueryAll(context.Background(), "dbID", nil, nil, 0)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all)=%d want 3", len(all))
	}
	if all[0].ID != "p1" || all[1].ID != "p2" || all[2].ID != "p3" {
		t.Errorf("pagination order wrong: %v %v %v", all[0].ID, all[1].ID, all[2].ID)
	}

	// Two POST /databases/dbID/query calls expected.
	var queryCalls int
	for _, c := range m.callsSnapshot() {
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/query") {
			queryCalls++
		}
	}
	if queryCalls != 2 {
		t.Errorf("query call count=%d want 2", queryCalls)
	}
}

// TestQueryAll_LimitTruncates covers the limit branch: QueryAll must stop
// and truncate the result slice as soon as len(all) reaches the supplied
// limit, without making further calls.
func TestQueryAll_LimitTruncates(t *testing.T) {
	m := newDBMockServer(t)
	m.queryPages = []QueryResponse{
		{Object: "list", Results: []Page{samplePage("p1"), samplePage("p2"), samplePage("p3")}, HasMore: true, NextCursor: "cur-2"},
		// This second page SHOULD NOT be fetched because limit=2 is met.
		{Object: "list", Results: []Page{samplePage("p4")}, HasMore: false},
	}
	dc := NewDatabaseClient(m.client())

	all, err := dc.QueryAll(context.Background(), "dbID", nil, nil, 2)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all)=%d want 2 (limit)", len(all))
	}

	var queryCalls int
	for _, c := range m.callsSnapshot() {
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/query") {
			queryCalls++
		}
	}
	if queryCalls != 1 {
		t.Errorf("query call count=%d want 1 (should stop at limit)", queryCalls)
	}
}

// TestQueryAll_ErrorMidPagination asserts QueryAll wraps mid-walk errors
// with the 1-based page number so operators can see how far the walk got
// before failing. Uses a standalone httptest.Server so we can script a
// success→500 sequence keyed on request count, which the shared dbMockServer
// doesn't support out of the box.
func TestQueryAll_ErrorMidPagination(t *testing.T) {
	var pageCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query") {
			pageCount++
			if pageCount == 1 {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(QueryResponse{
					Object:     "list",
					Results:    []Page{samplePage("p1")},
					HasMore:    true,
					NextCursor: "cur-2",
				})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"internal_server_error"}`))
			return
		}
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	dc := NewDatabaseClient(NewClient("sk_test", WithBaseURL(srv.URL)))

	_, err := dc.QueryAll(context.Background(), "dbID", nil, nil, 0)
	if err == nil {
		t.Fatal("expected error when page 2 fails, got nil")
	}
	if !strings.Contains(err.Error(), "page 2") {
		t.Errorf("expected error to mention 'page 2', got %q", err.Error())
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
		{name: "Query", call: func() error { _, err := dc.Query(ctx, "id", nil, nil, "", 0); return err }},
		// QueryAll exercises the same guard via Query; asserting here keeps
		// the public-method matrix explicit.
		{name: "QueryAll", call: func() error { _, err := dc.QueryAll(ctx, "id", nil, nil, 0); return err }},
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
