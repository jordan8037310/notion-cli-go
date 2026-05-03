// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDatabaseClient_QueryProbesDataSourceFirst pins the contract for
// #48: Query() must POST /v1/data_sources/{id}/query first, since on
// Notion-Version 2026-03-11 search results are data_sources (not
// databases) and queries against the legacy /databases/{id}/query
// endpoint return invalid_request_url for those ids.
//
// The fixture server 200s on /data_sources/.../query and would 500 on
// /databases/.../query — if Query ever flips the order, the test
// surfaces the regression as a 500 rather than the unrelated 404
// fallback signal.
func TestDatabaseClient_QueryProbesDataSourceFirst(t *testing.T) {
	var dataSourceHits, databaseHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/data_sources/") && strings.HasSuffix(r.URL.Path, "/query"):
			atomic.AddInt64(&dataSourceHits, 1)
			_ = json.NewEncoder(w).Encode(QueryResponse{
				Object:  "list",
				Results: []Page{{Object: "page", ID: "row-1"}},
			})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/databases/") && strings.HasSuffix(r.URL.Path, "/query"):
			atomic.AddInt64(&databaseHits, 1)
			http.Error(w, "should not be reached on the happy path", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	d := NewDatabaseClient(NewClient("k", WithBaseURL(srv.URL)))
	resp, err := d.Query(context.Background(), "ds-id", nil, nil, "", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp == nil || len(resp.Results) != 1 || resp.Results[0].ID != "row-1" {
		t.Errorf("Query result mismatch: %+v", resp)
	}
	if got := atomic.LoadInt64(&dataSourceHits); got != 1 {
		t.Errorf("/data_sources/.../query hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&databaseHits); got != 0 {
		t.Errorf("/databases/.../query hits = %d, want 0 (must not fall back when data_sources succeeded)", got)
	}
}

// TestDatabaseClient_QueryFallsBackToDatabasesOn404 covers the legacy
// path: when the id is a real database (not a data_source), the
// /data_sources/.../query probe returns 404 and Query must fall through
// to /databases/.../query rather than surfacing the 404 to the caller.
func TestDatabaseClient_QueryFallsBackToDatabasesOn404(t *testing.T) {
	var dataSourceHits, databaseHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/data_sources/"):
			atomic.AddInt64(&dataSourceHits, 1)
			http.Error(w, `{"object":"error","status":404,"code":"object_not_found"}`, http.StatusNotFound)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/databases/") && strings.HasSuffix(r.URL.Path, "/query"):
			atomic.AddInt64(&databaseHits, 1)
			_ = json.NewEncoder(w).Encode(QueryResponse{
				Object:  "list",
				Results: []Page{{Object: "page", ID: "legacy-row"}},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	d := NewDatabaseClient(NewClient("k", WithBaseURL(srv.URL)))
	resp, err := d.Query(context.Background(), "legacy-db-id", nil, nil, "", 0)
	if err != nil {
		t.Fatalf("Query (fallback path): %v", err)
	}
	if resp == nil || len(resp.Results) != 1 || resp.Results[0].ID != "legacy-row" {
		t.Errorf("fallback Query returned %+v, want one row with id legacy-row", resp)
	}
	if got := atomic.LoadInt64(&dataSourceHits); got != 1 {
		t.Errorf("data_sources probe hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&databaseHits); got != 1 {
		t.Errorf("databases fallback hits = %d, want 1", got)
	}
}

// TestDatabaseClient_QueryDoesNotFallBackOnNon404 confirms that genuine
// errors (auth, 5xx) surface immediately from the data_sources probe
// instead of being masked by the fallback chain. A 500 on
// /data_sources must NOT trigger a /databases retry — that would hide
// real problems and double the load on outages.
func TestDatabaseClient_QueryDoesNotFallBackOnNon404(t *testing.T) {
	var dataSourceHits, databaseHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/data_sources/"):
			atomic.AddInt64(&dataSourceHits, 1)
			http.Error(w, `{"object":"error","status":500,"code":"server_error"}`, http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/databases/"):
			atomic.AddInt64(&databaseHits, 1)
			http.Error(w, "should not be reached", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	d := NewDatabaseClient(NewClient("k", WithBaseURL(srv.URL)))
	if _, err := d.Query(context.Background(), "id", nil, nil, "", 0); err == nil {
		t.Fatal("expected 500 to surface, got nil")
	}
	if got := atomic.LoadInt64(&dataSourceHits); got != 1 {
		t.Errorf("data_sources hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&databaseHits); got != 0 {
		t.Errorf("databases hits = %d, want 0 (5xx must surface, not fall back)", got)
	}
}

// TestDatabaseClient_GetProbesDatabaseFirst pins the symmetric contract
// for Get(): try /databases/{id} first (legacy), fall back to
// /data_sources/{id} on 404. The order is reversed from Query's because
// the database object is the wrapper — when both endpoints work for an
// id, /databases/{id} is the more informative response.
func TestDatabaseClient_GetProbesDatabaseFirst(t *testing.T) {
	var databaseHits, dataSourceHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/"):
			atomic.AddInt64(&databaseHits, 1)
			id := strings.TrimPrefix(r.URL.Path, "/databases/")
			_ = json.NewEncoder(w).Encode(Database{Object: "database", ID: id})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/data_sources/"):
			atomic.AddInt64(&dataSourceHits, 1)
			http.Error(w, "should not be reached on legacy DB", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	d := NewDatabaseClient(NewClient("k", WithBaseURL(srv.URL)))
	db, err := d.Get(context.Background(), "db-id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if db == nil || db.ID != "db-id" {
		t.Errorf("got %+v, want id=db-id", db)
	}
	if got := atomic.LoadInt64(&databaseHits); got != 1 {
		t.Errorf("/databases hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&dataSourceHits); got != 0 {
		t.Errorf("/data_sources hits = %d, want 0 (no fallback when /databases succeeded)", got)
	}
}

// TestDatabaseClient_GetFallsBackToDataSourceOn404 covers the modern
// case: the id is a 2026-03-11 data_source returned by search, so
// /databases/{id} 404s and Get falls through.
func TestDatabaseClient_GetFallsBackToDataSourceOn404(t *testing.T) {
	var databaseHits, dataSourceHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/"):
			atomic.AddInt64(&databaseHits, 1)
			http.Error(w, `{"object":"error","status":404,"code":"object_not_found"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/data_sources/"):
			atomic.AddInt64(&dataSourceHits, 1)
			id := strings.TrimPrefix(r.URL.Path, "/data_sources/")
			_ = json.NewEncoder(w).Encode(Database{Object: "data_source", ID: id})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	d := NewDatabaseClient(NewClient("k", WithBaseURL(srv.URL)))
	db, err := d.Get(context.Background(), "ds-id")
	if err != nil {
		t.Fatalf("Get (fallback path): %v", err)
	}
	if db == nil || db.Object != "data_source" {
		t.Errorf("got %+v, want object=data_source", db)
	}
	if got := atomic.LoadInt64(&databaseHits); got != 1 || atomic.LoadInt64(&dataSourceHits) != 1 {
		t.Errorf("hits = (db=%d, ds=%d), want (1, 1)", databaseHits, dataSourceHits)
	}
}
