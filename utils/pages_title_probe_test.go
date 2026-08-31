// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreate_TitleKeyProbeFollowsTheDataSource guards issue #105 — and the
// reason it went unnoticed for so long.
//
// pages create probes the parent database to learn its title-property key,
// so a renamed title column ("Name", "Project", "Client Name") gets the
// value. That is the #60 fix. But since 2025-09-03 GET /v1/databases/{id}
// returns a CONTAINER carrying no `properties` at all — confirmed live. The
// probe therefore read an empty schema, matched nothing, and fell back to
// the literal key "title" on every real database parent.
//
// #60's tests kept passing because their mock returned a pre-upgrade object
// with `properties` inline: a fixture more permissive than the live API,
// asserting a response Notion no longer sends. This mock models the real
// split, so it fails against the unfixed probe.
func TestCreate_TitleKeyProbeFollowsTheDataSource(t *testing.T) {
	var createBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// The container: data_sources, and NO properties. A data source id
		// presented here 404s — the namespaces are disjoint, confirmed live.
		case r.Method == http.MethodGet && r.URL.Path == "/databases/dbID":
			writeJSON(w, map[string]interface{}{
				"object": "database", "id": "dbID",
				"title":        []map[string]interface{}{{"type": "text", "plain_text": "Tracker"}},
				"data_sources": []map[string]string{{"id": "dsID", "name": "Tracker"}},
			})

		// The data source: where the schema actually lives. Its title
		// column is renamed, which is the whole point of #60.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/data_sources/"):
			writeJSON(w, map[string]interface{}{
				"object": "data_source", "id": "dsID",
				"parent": map[string]interface{}{"type": "database_id", "database_id": "dbID"},
				"properties": map[string]interface{}{
					"Client Name": map[string]interface{}{"type": "title", "title": map[string]interface{}{}},
					"Status":      map[string]interface{}{"type": "status", "status": map[string]interface{}{}},
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/pages":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &createBody)
			writeJSON(w, map[string]interface{}{"object": "page", "id": "newPage"})

		default:
			http.Error(w, `{"object":"error","status":404,"code":"object_not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	pc := NewPageClient(NewClient("k", WithBaseURL(srv.URL)))
	if _, err := pc.Create(context.Background(), CreatePageRequest{
		Parent: PageParent{DatabaseID: "dbID"},
		Title:  "Acme Corp",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	props, ok := createBody["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("create body has no properties: %v", createBody)
	}
	if _, wrong := props["title"]; wrong {
		t.Error("title was written to the literal key \"title\": the probe never reached the data source, " +
			"so the #60 renamed-title-column fix is dead again")
	}
	if _, right := props["Client Name"]; !right {
		t.Errorf("title should have been written to the renamed column %q; got keys %v",
			"Client Name", keysOfAny(props))
	}
}

// TestCreate_TitleKeyProbeDoesNotGuessOnMultiSource confirms the probe
// declines rather than picking a source. Two data sources give no basis for
// choosing, and writing the title to the wrong column is worse than falling
// back to the documented default.
func TestCreate_TitleKeyProbeDoesNotGuessOnMultiSource(t *testing.T) {
	var createBody map[string]interface{}
	dsHits := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/"):
			writeJSON(w, map[string]interface{}{
				"object": "database", "id": "dbID",
				"data_sources": []map[string]string{{"id": "dsA", "name": "A"}, {"id": "dsB", "name": "B"}},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/data_sources/"):
			dsHits++
			writeJSON(w, map[string]interface{}{"object": "data_source", "id": "dsA"})
		case r.Method == http.MethodPost && r.URL.Path == "/pages":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &createBody)
			writeJSON(w, map[string]interface{}{"object": "page", "id": "newPage"})
		default:
			http.Error(w, `{"code":"object_not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	pc := NewPageClient(NewClient("k", WithBaseURL(srv.URL)))
	if _, err := pc.Create(context.Background(), CreatePageRequest{
		Parent: PageParent{DatabaseID: "dbID"},
		Title:  "Acme Corp",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if dsHits != 0 {
		t.Errorf("probe followed a data source on a multi-source container (%d hits); "+
			"with two sources there is no basis for picking one", dsHits)
	}
	props, _ := createBody["properties"].(map[string]interface{})
	if _, ok := props["title"]; !ok {
		t.Errorf("ambiguous container should fall back to the literal \"title\" key; got %v", keysOfAny(props))
	}
}

func keysOfAny(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
