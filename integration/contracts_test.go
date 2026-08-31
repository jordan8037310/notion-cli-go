// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// THE VALIDATED DATA SET
//
// Every claim below was observed against a live workspace at Notion-Version
// 2026-03-11 on 2026-08-30, not read from documentation. That distinction
// matters: the create-database reference page still carries pre-2025-09-03
// prose that contradicts its own OpenAPI schema, and following the prose is
// what shipped a schema-dropping bug.
//
// TO UPDATE FOR A NEW API VERSION
//   1. Bump APIVersion in harness.go.
//   2. Run `make integration-test`.
//   3. Each failure names the endpoint whose shape moved and prints the body
//      we actually received. Fix the client, then update the contract here.
// The point is that a version bump produces a list of moved shapes instead
// of a production incident.
//
// The ANTI-CONTRACTS are the important half. They pin behaviour that is
// wrong-but-silent: Notion answers 200 and ignores the obsolete key. A mock
// cannot discover these, and every one of them shipped as a green test.
// ---------------------------------------------------------------------------

// TestContract_DatabaseIsAContainer pins the 2025-09-03 split: a database
// holds data sources and has no schema of its own.
//
// Consequences if this drifts: `databases get` returns an empty schema
// (#94), and the renamed-title-column probe in `pages create` silently
// stops working (#105).
func TestContract_DatabaseIsAContainer(t *testing.T) {
	h := New(t)
	dbID, dsID := h.newDatabase(t, map[string]interface{}{
		"Name": map[string]interface{}{"title": map[string]interface{}{}},
	})

	status, db := h.API(http.MethodGet, "/databases/"+dbID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /databases/{id}: HTTP %d: %v", status, db)
	}
	if _, hasProps := db["properties"]; hasProps {
		t.Errorf("CONTRACT MOVED: GET /databases/{id} now returns a `properties` key.\n"+
			"The container used to carry no schema. Re-check DatabaseClient.Get and the\n"+
			"pages-create title probe (#105) — they assume the schema lives elsewhere.\nGot: %v", db)
	}
	sources, ok := db["data_sources"].([]interface{})
	if !ok || len(sources) == 0 {
		t.Fatalf("CONTRACT MOVED: GET /databases/{id} no longer returns a non-empty `data_sources` array.\n"+
			"That array is how a data source id is discovered (`databases data-sources`).\nGot: %v", db)
	}

	status, ds := h.API(http.MethodGet, "/data_sources/"+dsID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /data_sources/{id}: HTTP %d: %v", status, ds)
	}
	if _, hasProps := ds["properties"]; !hasProps {
		t.Errorf("CONTRACT MOVED: the data source no longer carries `properties`.\nGot: %v", ds)
	}
	if obj, _ := ds["object"].(string); obj != "data_source" {
		t.Errorf("data source object = %q, want data_source", obj)
	}
}

// TestContract_CreateSchemaNestsUnderInitialDataSource is the contract, and
// its anti-contract, for POST /v1/databases.
//
// The anti-contract is the load-bearing half: a top-level `properties` key
// returns HTTP 200 and is SILENTLY IGNORED. No error, no warning — the CLI
// printed a green success while every column the user defined vanished.
// That is why this needs a live test and cannot be mocked.
func TestContract_CreateSchemaNestsUnderInitialDataSource(t *testing.T) {
	h := New(t)
	schema := map[string]interface{}{
		"Name":     map[string]interface{}{"title": map[string]interface{}{}},
		"Priority": map[string]interface{}{"select": map[string]interface{}{}},
		"Notes":    map[string]interface{}{"rich_text": map[string]interface{}{}},
	}

	t.Run("anti-contract: top-level properties is accepted and dropped", func(t *testing.T) {
		status, body := h.API(http.MethodPost, "/databases", map[string]interface{}{
			"parent":     map[string]interface{}{"type": "page_id", "page_id": h.PageID},
			"title":      titleRT("contract top-level"),
			"properties": schema,
		})
		if status != http.StatusOK {
			t.Skipf("top-level properties now returns HTTP %d — Notion may have started rejecting it, "+
				"which would be an improvement; nothing to guard here anymore. Body: %v", status, body)
		}
		dbID, _ := body["id"].(string)
		h.Defer(func() { h.Archive("/databases/", dbID) })

		got := h.schemaOf(t, firstDataSource(t, body))
		if len(got) != 1 {
			t.Errorf("ANTI-CONTRACT MOVED: top-level `properties` now yields %d columns (%v), not the\n"+
				"single default title column. If Notion started honouring it, the nesting in\n"+
				"DatabaseClient.Create is no longer required — but verify before simplifying.", len(got), got)
		}
	})

	t.Run("contract: initial_data_source.properties is applied", func(t *testing.T) {
		status, body := h.API(http.MethodPost, "/databases", map[string]interface{}{
			"parent":              map[string]interface{}{"type": "page_id", "page_id": h.PageID},
			"title":               titleRT("contract nested"),
			"initial_data_source": map[string]interface{}{"properties": schema},
		})
		if status != http.StatusOK {
			t.Fatalf("CONTRACT MOVED: initial_data_source.properties rejected with HTTP %d: %v", status, body)
		}
		dbID, _ := body["id"].(string)
		h.Defer(func() { h.Archive("/databases/", dbID) })

		got := h.schemaOf(t, firstDataSource(t, body))
		for _, want := range []string{"Name", "Priority", "Notes"} {
			if _, ok := got[want]; !ok {
				t.Errorf("CONTRACT MOVED: column %q did not land via initial_data_source.properties; got %v", want, got)
			}
		}
	})
}

// TestContract_SchemaWritesGoToTheDataSource covers PATCH, including its own
// silent anti-contract: the database endpoint accepts `properties` with a
// 200 and ignores it.
func TestContract_SchemaWritesGoToTheDataSource(t *testing.T) {
	h := New(t)
	dbID, dsID := h.newDatabase(t, map[string]interface{}{
		"Name": map[string]interface{}{"title": map[string]interface{}{}},
	})

	t.Run("anti-contract: PATCH /databases ignores properties", func(t *testing.T) {
		status, body := h.API(http.MethodPatch, "/databases/"+dbID, map[string]interface{}{
			"properties": map[string]interface{}{"ShouldNotAppear": map[string]interface{}{"checkbox": map[string]interface{}{}}},
		})
		if status != http.StatusOK {
			t.Skipf("PATCH /databases with properties now returns HTTP %d — it may have started "+
				"rejecting the key, which would be an improvement. Body: %v", status, body)
		}
		if _, leaked := h.schemaOf(t, dsID)["ShouldNotAppear"]; leaked {
			t.Error("ANTI-CONTRACT MOVED: PATCH /v1/databases/{id} now APPLIES `properties`.\n" +
				"Update's routing could be simplified — but confirm this is intended and not a\n" +
				"transient before removing the data-source hop.")
		}
	})

	t.Run("contract: PATCH /data_sources applies properties", func(t *testing.T) {
		status, body := h.API(http.MethodPatch, "/data_sources/"+dsID, map[string]interface{}{
			"properties": map[string]interface{}{"Added": map[string]interface{}{"checkbox": map[string]interface{}{}}},
		})
		if status != http.StatusOK {
			t.Fatalf("CONTRACT MOVED: PATCH /data_sources/{id} rejected properties: HTTP %d: %v", status, body)
		}
		if _, ok := h.schemaOf(t, dsID)["Added"]; !ok {
			t.Error("CONTRACT MOVED: PATCH /data_sources/{id} no longer applies `properties`")
		}
	})

	// This is what lets DatabaseClient.Update stay atomic. If it ever stops
	// being true, Update must go back to two calls and re-acquire the
	// half-applied-write problem the PR #111 review found.
	t.Run("contract: data source PATCH accepts title alongside properties", func(t *testing.T) {
		status, body := h.API(http.MethodPatch, "/data_sources/"+dsID, map[string]interface{}{
			"title":      titleRT("renamed atomically"),
			"properties": map[string]interface{}{"Together": map[string]interface{}{"checkbox": map[string]interface{}{}}},
		})
		if status != http.StatusOK {
			t.Fatalf("CONTRACT MOVED: data source PATCH rejected title+properties together: HTTP %d: %v\n"+
				"DatabaseClient.Update relies on this to write both in ONE request.", status, body)
		}
		if _, ok := h.schemaOf(t, dsID)["Together"]; !ok {
			t.Error("CONTRACT MOVED: combined title+properties PATCH did not apply the schema")
		}
		if plain(body["title"]) != "renamed atomically" {
			t.Errorf("CONTRACT MOVED: combined PATCH did not apply the title; got %q", plain(body["title"]))
		}
	})
}

// TestContract_IdNamespacesAreDisjoint pins the fact that makes
// resolveDataSourceID necessary. If these ever became interchangeable the
// resolution hop would be dead weight — but until then, addressing the wrong
// surface fails, which is why Update must never split one id across two.
func TestContract_IdNamespacesAreDisjoint(t *testing.T) {
	h := New(t)
	dbID, dsID := h.newDatabase(t, map[string]interface{}{
		"Name": map[string]interface{}{"title": map[string]interface{}{}},
	})

	if status, body := h.API(http.MethodGet, "/databases/"+dsID, nil); status == http.StatusOK {
		t.Errorf("CONTRACT MOVED: a data source id now resolves at /v1/databases/{id} (HTTP 200).\n"+
			"Get's probe/fallback and Update's resolution both assume these namespaces are disjoint.\nGot: %v", body)
	}
	if status, body := h.API(http.MethodGet, "/data_sources/"+dbID, nil); status == http.StatusOK {
		t.Errorf("CONTRACT MOVED: a database id now resolves at /v1/data_sources/{id} (HTTP 200).\nGot: %v", body)
	}
}

// TestContract_ErrorsCarryCodeAndRequestID pins the error envelope the CLI
// should be surfacing. Notion support asks for request_id first, and issue
// #101 tracks the client flattening all of this into a prose string.
func TestContract_ErrorsCarryCodeAndRequestID(t *testing.T) {
	h := New(t)
	status, body := h.API(http.MethodGet, "/databases/00000000-0000-0000-0000-000000000000", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent id, got HTTP %d: %v", status, body)
	}
	for _, key := range []string{"object", "status", "code", "message", "request_id"} {
		if _, ok := body[key]; !ok {
			t.Errorf("CONTRACT MOVED: error envelope no longer carries %q; got keys %v", key, keysOf(body))
		}
	}
	if code, _ := body["code"].(string); code != "object_not_found" {
		t.Errorf("404 code = %q, want object_not_found", code)
	}
}

// TestContract_BlockChildrenAppendCapIs100 pins the limit behind issue #97.
// pages duplicate appends an unbounded array; past this cap the append fails
// AFTER the destination page exists, orphaning it.
func TestContract_BlockChildrenAppendCapIs100(t *testing.T) {
	h := New(t)
	children := make([]map[string]interface{}, 0, 101)
	for i := 0; i < 101; i++ {
		children = append(children, map[string]interface{}{
			"object": "block", "type": "paragraph",
			"paragraph": map[string]interface{}{"rich_text": titleRT("x")},
		})
	}
	status, body := h.API(http.MethodPatch, "/blocks/"+h.PageID+"/children",
		map[string]interface{}{"children": children})
	if status == http.StatusOK {
		t.Errorf("CONTRACT MOVED: appending 101 children succeeded. The 100-item cap that makes\n" +
			"chunking necessary (#97) may have been lifted — verify before removing the chunking.")
		return
	}
	if code, _ := body["code"].(string); code != "validation_error" {
		t.Logf("101-child append rejected with code %q (expected validation_error): %v", code, body)
	}
}

// --- helpers ---------------------------------------------------------------

// newDatabase provisions a database under the run page using the shape the
// contracts prove correct, and returns (databaseID, dataSourceID).
func (h *Harness) newDatabase(t *testing.T, schema map[string]interface{}) (string, string) {
	t.Helper()
	status, body := h.API(http.MethodPost, "/databases", map[string]interface{}{
		"parent":              map[string]interface{}{"type": "page_id", "page_id": h.PageID},
		"title":               titleRT("contract fixture"),
		"initial_data_source": map[string]interface{}{"properties": schema},
	})
	if status != http.StatusOK {
		t.Fatalf("provision fixture database: HTTP %d: %v", status, body)
	}
	dbID, _ := body["id"].(string)
	h.Defer(func() { h.Archive("/databases/", dbID) })
	return dbID, firstDataSource(t, body)
}

func (h *Harness) schemaOf(t *testing.T, dsID string) map[string]interface{} {
	t.Helper()
	status, body := h.API(http.MethodGet, "/data_sources/"+dsID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /data_sources/%s: HTTP %d: %v", dsID, status, body)
	}
	props, _ := body["properties"].(map[string]interface{})
	return props
}

func firstDataSource(t *testing.T, db map[string]interface{}) string {
	t.Helper()
	sources, ok := db["data_sources"].([]interface{})
	if !ok || len(sources) == 0 {
		t.Fatalf("database response carries no data_sources: %v", db)
	}
	first, _ := sources[0].(map[string]interface{})
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatalf("first data source has no id: %v", sources[0])
	}
	return id
}

func titleRT(s string) []map[string]interface{} {
	return []map[string]interface{}{
		{"type": "text", "text": map[string]interface{}{"content": s}},
	}
}

func plain(v interface{}) string {
	runs, ok := v.([]interface{})
	if !ok {
		return ""
	}
	out := ""
	for _, r := range runs {
		m, _ := r.(map[string]interface{})
		s, _ := m["plain_text"].(string)
		out += s
	}
	return out
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
