// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// createManyMock serves the two endpoints CreateMany touches: the database
// probe (GET /databases/{id}) and the create itself (POST /pages).
//
// failTitles names the titles whose POST should 400. Driving the failure
// off the title rather than a call counter keeps the abort/continue tests
// readable — the test says which entries are bad, not which ordinals.
type createManyMock struct {
	srv        *httptest.Server
	failTitles map[string]bool

	mu        sync.Mutex
	dbProbes  int
	postPaths []string
	postBody  []map[string]interface{}
}

func newCreateManyMock(t *testing.T, failTitles ...string) *createManyMock {
	t.Helper()
	m := &createManyMock{failTitles: map[string]bool{}}
	for _, title := range failTitles {
		m.failTitles[title] = true
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *createManyMock) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/"):
		m.mu.Lock()
		m.dbProbes++
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// A 2025-09-03+ container: no inline properties, one data source.
		_, _ = io.WriteString(w, `{"object":"database","id":"db1","data_sources":[{"id":"ds1","name":"Tasks"}]}`)

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/data_sources/"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"data_source","id":"ds1","properties":{"Task name":{"id":"title","type":"title","title":{}}}}`)

	case r.Method == http.MethodPost && r.URL.Path == "/pages":
		var body map[string]interface{}
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		m.mu.Lock()
		m.postPaths = append(m.postPaths, r.URL.Path)
		m.postBody = append(m.postBody, body)
		m.mu.Unlock()

		if m.failTitles[titleOfCreateBody(body)] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"object":"error","status":400,"code":"validation_error","message":"body failed validation"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		m.mu.Lock()
		n := len(m.postBody)
		m.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"object":"page","id":"page-%d"}`, n)

	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"object":"error","status":404,"code":"object_not_found","message":"nope"}`)
	}
}

// titleOfCreateBody digs the plain-text title out of a create body under
// whatever key the title landed on, so the mock does not have to know
// whether the probe renamed it.
func titleOfCreateBody(body map[string]interface{}) string {
	props, _ := body["properties"].(map[string]interface{})
	for _, v := range props {
		prop, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		rich, ok := prop["title"].([]interface{})
		if !ok {
			continue
		}
		for _, r := range rich {
			item, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := item["text"].(map[string]interface{}); ok {
				if c, ok := text["content"].(string); ok {
					return c
				}
			}
		}
	}
	return ""
}

func (m *createManyMock) client() *PageClient {
	return NewPageClient(NewClient("sk_test", WithBaseURL(m.srv.URL), WithMaxRetries(0)))
}

func (m *createManyMock) counts() (probes, posts int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dbProbes, len(m.postBody)
}

func dbSpec(title string) CreatePageRequest {
	return CreatePageRequest{Parent: PageParent{DatabaseID: "db1"}, Title: title}
}

// TestCreateMany_CreatesEachInOrder is the happy path: one POST per spec,
// pages returned in input order, no errors.
func TestCreateMany_CreatesEachInOrder(t *testing.T) {
	m := newCreateManyMock(t)
	created, errs := m.client().CreateMany(context.Background(),
		[]CreatePageRequest{dbSpec("one"), dbSpec("two"), dbSpec("three")}, true, nil)

	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if len(created) != 3 {
		t.Fatalf("created %d pages, want 3", len(created))
	}
	for i, want := range []string{"page-1", "page-2", "page-3"} {
		if created[i].ID != want {
			t.Errorf("created[%d].ID = %q, want %q", i, created[i].ID, want)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, want := range []string{"one", "two", "three"} {
		if got := titleOfCreateBody(m.postBody[i]); got != want {
			t.Errorf("POST %d title = %q, want %q (order not preserved)", i, got, want)
		}
	}
}

// TestCreateMany_ProbesEachDatabaseOnce is the reason CreateMany exists as
// more than a for-loop around Create. Create probes the parent database on
// every call; for an import of N rows under one database that is N-1
// redundant round-trips against an API that rate-limits at a few per
// second. The probe result must be memoised per database id.
func TestCreateMany_ProbesEachDatabaseOnce(t *testing.T) {
	m := newCreateManyMock(t)
	specs := make([]CreatePageRequest, 5)
	for i := range specs {
		specs[i] = dbSpec(fmt.Sprintf("row %d", i))
	}
	created, errs := m.client().CreateMany(context.Background(), specs, true, nil)
	if len(errs) != 0 || len(created) != 5 {
		t.Fatalf("created=%d errs=%v, want 5 and none", len(created), errs)
	}
	probes, posts := m.counts()
	if probes != 1 {
		t.Errorf("database probed %d times for 5 rows under one database, want 1", probes)
	}
	if posts != 5 {
		t.Errorf("posts = %d, want 5", posts)
	}
}

// TestCreateMany_ResolvedTitleKeyReachesEveryRow guards the memoisation
// itself: the cached probe carries the renamed title column ("Task name"),
// so every row — not just the first — must write its title there. Caching
// the parent but losing the title key would put every row's title in a
// column Notion does not have.
func TestCreateMany_ResolvedTitleKeyReachesEveryRow(t *testing.T) {
	m := newCreateManyMock(t)
	_, errs := m.client().CreateMany(context.Background(),
		[]CreatePageRequest{dbSpec("first"), dbSpec("second")}, true, nil)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, body := range m.postBody {
		props, _ := body["properties"].(map[string]interface{})
		if _, ok := props["Task name"]; !ok {
			t.Errorf("POST %d wrote title to %v, want the probed key %q", i, keysOf(props), "Task name")
		}
		parent, _ := body["parent"].(map[string]interface{})
		if parent["database_id"] != "db1" {
			t.Errorf("POST %d parent = %v, want the database parent preserved", i, parent)
		}
	}
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCreateMany_AbortStopsAtFirstFailure asserts the default mode leaves
// the rest of the file untouched — and that the pages already created are
// still returned, since the operator has to know what not to re-run.
func TestCreateMany_AbortStopsAtFirstFailure(t *testing.T) {
	m := newCreateManyMock(t, "bad")
	created, errs := m.client().CreateMany(context.Background(),
		[]CreatePageRequest{dbSpec("ok"), dbSpec("bad"), dbSpec("never")}, true, nil)

	if len(created) != 1 || created[0].ID != "page-1" {
		t.Errorf("created = %v, want just the first page", created)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}
	if _, posts := m.counts(); posts != 2 {
		t.Errorf("posted %d times, want 2 — the third entry should never be attempted", posts)
	}
	if !strings.Contains(errs[0].Error(), "entry 2 (bad)") {
		t.Errorf("error %q does not name the failing entry; an operator cannot find the line to fix", errs[0])
	}
}

// TestCreateMany_ContinueAttemptsEveryEntry is the other half: every entry
// is tried, failures accumulate, and the successes still come back.
func TestCreateMany_ContinueAttemptsEveryEntry(t *testing.T) {
	m := newCreateManyMock(t, "bad1", "bad2")
	created, errs := m.client().CreateMany(context.Background(),
		[]CreatePageRequest{dbSpec("bad1"), dbSpec("good"), dbSpec("bad2")}, false, nil)

	if len(created) != 1 || created[0].ID != "page-2" {
		t.Errorf("created = %v, want only the middle page", created)
	}
	if len(errs) != 2 {
		t.Fatalf("errs = %v, want two", errs)
	}
	if _, posts := m.counts(); posts != 3 {
		t.Errorf("posted %d times, want all 3 attempted under --on-error continue", posts)
	}
	for i, want := range []string{"entry 1 (bad1)", "entry 3 (bad2)"} {
		if !strings.Contains(errs[i].Error(), want) {
			t.Errorf("errs[%d] = %q, want it to name %q", i, errs[i], want)
		}
	}
}

// TestCreateMany_OnEachSeesEveryAttempt covers the streaming callback. A
// large import runs for minutes at Notion's rate limit, so the caller has
// to be able to report progress as it happens rather than at the end.
func TestCreateMany_OnEachSeesEveryAttempt(t *testing.T) {
	m := newCreateManyMock(t, "bad")
	var seen []string
	_, _ = m.client().CreateMany(context.Background(),
		[]CreatePageRequest{dbSpec("good"), dbSpec("bad")}, false,
		func(i int, page *Page, err error) {
			if err != nil {
				seen = append(seen, fmt.Sprintf("%d:err", i))
				return
			}
			seen = append(seen, fmt.Sprintf("%d:%s", i, page.ID))
		})

	want := []string{"0:page-1", "1:err"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("onEach saw %v, want %v", seen, want)
	}
}

// TestCreateMany_EmptyInput must be a no-op, not an error: a filtered
// import that legitimately selects nothing should not fail the run.
func TestCreateMany_EmptyInput(t *testing.T) {
	m := newCreateManyMock(t)
	created, errs := m.client().CreateMany(context.Background(), nil, true, nil)
	if len(created) != 0 || len(errs) != 0 {
		t.Errorf("created=%v errs=%v, want both empty", created, errs)
	}
	if _, posts := m.counts(); posts != 0 {
		t.Errorf("posted %d times for an empty input", posts)
	}
}

// TestCreateMany_RejectsEntryWithoutParent checks the per-entry guard. A
// file can carry a parentless entry even when other entries have one, so
// validation belongs on the entry, not on the batch.
func TestCreateMany_RejectsEntryWithoutParent(t *testing.T) {
	m := newCreateManyMock(t)
	created, errs := m.client().CreateMany(context.Background(),
		[]CreatePageRequest{dbSpec("fine"), {Title: "orphan"}}, false, nil)

	if len(created) != 1 {
		t.Errorf("created = %v, want the valid entry to still land", created)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "entry 2 (orphan)") {
		t.Fatalf("errs = %v, want one naming entry 2", errs)
	}
	if _, posts := m.counts(); posts != 1 {
		t.Errorf("posted %d times; the parentless entry must not be sent", posts)
	}
}

// TestCreateMany_StopsOnCanceledContext keeps an interrupted import from
// running to the end of the file.
func TestCreateMany_StopsOnCanceledContext(t *testing.T) {
	m := newCreateManyMock(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	created, errs := m.client().CreateMany(ctx,
		[]CreatePageRequest{dbSpec("one"), dbSpec("two")}, false, nil)

	if len(created) != 0 {
		t.Errorf("created = %v, want none on a canceled context", created)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want one", errs)
	}
	if _, posts := m.counts(); posts != 0 {
		t.Errorf("posted %d times after cancellation", posts)
	}
}

// TestCreateMany_PassesPropertiesAndChildrenThrough confirms the #40
// passthrough survives the bulk path — the two features are only useful
// together, which is why #39 waited on #40.
func TestCreateMany_PassesPropertiesAndChildrenThrough(t *testing.T) {
	m := newCreateManyMock(t)
	spec := CreatePageRequest{
		Parent:     PageParent{DatabaseID: "db1"},
		Title:      "row",
		Properties: map[string]interface{}{"Stage": map[string]interface{}{"select": map[string]interface{}{"name": "Live"}}},
		Children: []map[string]interface{}{
			{"object": "block", "type": "paragraph"},
		},
	}
	if _, errs := m.client().CreateMany(context.Background(), []CreatePageRequest{spec}, true, nil); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	props, _ := m.postBody[0]["properties"].(map[string]interface{})
	if _, ok := props["Stage"]; !ok {
		t.Errorf("properties = %v, want Stage passed through", keysOf(props))
	}
	if children, _ := m.postBody[0]["children"].([]interface{}); len(children) != 1 {
		t.Errorf("children = %v, want the one block passed through", m.postBody[0]["children"])
	}
}
