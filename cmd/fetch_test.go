// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"notioncli/utils"
)

// fetchID is a canonical 32-hex Notion id used across the fetch tests.
// Its dashed form is what the cmd dispatch path actually sends to the
// mock server because ParseNotionID normalises every input.
const (
	fetchHexID    = "abc123def4567890abc123def4567890"
	fetchDashedID = "abc123de-f456-7890-abc1-23def4567890"
)

// fetchMock is an httptest server that lets each test configure how the
// /pages and /databases endpoints respond. The default is 404 on both so
// individual tests opt into success by setting the corresponding handler
// flag.
type fetchMock struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    map[string]int
	pageOK   bool
	dbOK     bool
	pageErr  int // optional non-404 error code on /pages
	dbErr    int // optional non-404 error code on /databases
}

func newFetchMock(t *testing.T) *fetchMock {
	t.Helper()
	m := &fetchMock{calls: map[string]int{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.calls[r.Method+" "+r.URL.Path]++
		pageOK, dbOK := m.pageOK, m.dbOK
		pageErr, dbErr := m.pageErr, m.dbErr
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/pages/"):
			id := strings.TrimPrefix(r.URL.Path, "/pages/")
			if pageErr != 0 {
				http.Error(w, `{"object":"error","code":"server_error"}`, pageErr)
				return
			}
			if !pageOK {
				http.Error(w, `{"object":"error","code":"object_not_found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object":           "page",
				"id":               id,
				"created_time":     "2026-04-22T10:00:00.000Z",
				"last_edited_time": "2026-04-22T10:00:00.000Z",
				"url":              "https://notion.so/" + id,
				"parent":           map[string]interface{}{"type": "page_id", "page_id": "p"},
				"properties": map[string]interface{}{
					"Name": map[string]interface{}{
						"title": []interface{}{
							map[string]interface{}{
								"type":       "text",
								"plain_text": "Sample Page",
							},
						},
					},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/databases/"):
			id := strings.TrimPrefix(r.URL.Path, "/databases/")
			if dbErr != 0 {
				http.Error(w, `{"object":"error","code":"server_error"}`, dbErr)
				return
			}
			if !dbOK {
				http.Error(w, `{"object":"error","code":"object_not_found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object":           "database",
				"id":               id,
				"created_time":     "2026-04-22T10:00:00.000Z",
				"last_edited_time": "2026-04-22T10:00:00.000Z",
				"url":              "https://notion.so/" + id,
				"title": []map[string]interface{}{
					{"plain_text": "Sample DB", "text": map[string]interface{}{"content": "Sample DB"}},
				},
				"parent":     map[string]interface{}{"type": "page_id", "page_id": "p"},
				"properties": map[string]interface{}{},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *fetchMock) count(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[key]
}

// withFetchEnv mirrors withCmdEnv but swaps in the fetch-aware mock so
// /pages and /databases respond exactly the way the dispatcher expects.
func withFetchEnv(t *testing.T) *fetchMock {
	t.Helper()
	_ = withCmdEnv(t)
	m := newFetchMock(t)
	prior := utils.GetBaseURL()
	utils.SetBaseURL(m.srv.URL)
	t.Cleanup(func() { utils.SetBaseURL(prior) })
	return m
}

// TestFetch_CmdRegistered confirms the fetch command is wired onto rootCmd.
func TestFetch_CmdRegistered(t *testing.T) {
	cmd := findTopLevelCmd(t, "fetch")
	if cmd.Use == "" {
		t.Error("fetch: Use is empty")
	}
	// Single positional arg.
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("fetch: expected error on zero args")
	}
	if err := cmd.Args(cmd, []string{"id"}); err != nil {
		t.Errorf("fetch: expected no error on one arg, got %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("fetch: expected error on two args")
	}
}

// TestFetch_PersistentFlagsAvailable confirms --json and --resolve-mentions
// are reachable from fetch via the persistent flags on rootCmd. Fetch
// itself adds no flags but must honour these.
func TestFetch_PersistentFlagsAvailable(t *testing.T) {
	cmd := findTopLevelCmd(t, "fetch")
	if cmd.InheritedFlags().Lookup("json") == nil {
		t.Error("fetch: persistent --json flag not visible")
	}
	if cmd.InheritedFlags().Lookup("resolve-mentions") == nil {
		t.Error("fetch: persistent --resolve-mentions flag not visible")
	}
}

// TestFetch_PageResolves runs the happy page path: /pages/{id} returns 200
// and the dispatcher prints the page summary without ever falling through
// to /databases.
func TestFetch_PageResolves(t *testing.T) {
	m := withFetchEnv(t)
	m.pageOK = true
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"fetch", fetchHexID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := m.count("GET /pages/" + fetchDashedID); got != 1 {
		t.Errorf("fetch did not GET /pages/%s exactly once (count=%d, calls=%v)", fetchDashedID, got, m.calls)
	}
	if got := m.count("GET /databases/" + fetchDashedID); got != 0 {
		t.Errorf("fetch should not have probed /databases when page hits (count=%d)", got)
	}
	if !strings.Contains(out.String(), "page") || !strings.Contains(out.String(), fetchDashedID) {
		t.Errorf("fetch human output missing page/id markers: %q", out.String())
	}
	if !strings.Contains(out.String(), "blocks list") {
		t.Errorf("fetch human output missing follow-up hint: %q", out.String())
	}
}

// TestFetch_DatabaseResolvesAfterPage404 confirms the dispatcher falls
// through to /databases when /pages returns 404, and emits the database
// follow-up hint.
func TestFetch_DatabaseResolvesAfterPage404(t *testing.T) {
	m := withFetchEnv(t)
	m.dbOK = true
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"fetch", fetchHexID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := m.count("GET /pages/" + fetchDashedID); got != 1 {
		t.Errorf("fetch should have probed /pages first (count=%d)", got)
	}
	if got := m.count("GET /databases/" + fetchDashedID); got != 1 {
		t.Errorf("fetch did not fall through to /databases (count=%d, calls=%v)", got, m.calls)
	}
	if !strings.Contains(out.String(), "database") || !strings.Contains(out.String(), "databases query") {
		t.Errorf("fetch human output missing database hint: %q", out.String())
	}
}

// TestFetch_AllNotFound confirms a clear "no resource found" error when
// both probes return 404.
func TestFetch_AllNotFound(t *testing.T) {
	_ = withFetchEnv(t)
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs([]string{"fetch", fetchHexID})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when both /pages and /databases 404, got nil")
	}
	if !strings.Contains(err.Error(), "no page or database") {
		t.Errorf("expected 'no page or database' error, got %v", err)
	}
}

// TestFetch_BadInput confirms ParseNotionID errors propagate cleanly
// without issuing any HTTP probes.
func TestFetch_BadInput(t *testing.T) {
	m := withFetchEnv(t)
	resetRootCmdArgs()

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs([]string{"fetch", "not-a-real-id"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error on malformed input, got nil")
	}
	if got := m.count("GET /pages/"); got != 0 {
		t.Errorf("expected no /pages probes on malformed input, got %d", got)
	}
}

// TestFetch_JSONOutput confirms --json emits the raw page object and
// suppresses the human follow-up hint.
func TestFetch_JSONOutput(t *testing.T) {
	m := withFetchEnv(t)
	m.pageOK = true
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"fetch", "--json", fetchHexID})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if got["object"] != "page" {
		t.Errorf("expected object=page, got %v", got["object"])
	}
	if strings.Contains(out.String(), "hint:") {
		t.Errorf("--json mode should not emit human hint line: %q", out.String())
	}
}

// TestFetch_PageProbeNon404Surfaces confirms a non-404 page error short-
// circuits the dispatcher (no fall-through, error returned to caller).
func TestFetch_PageProbeNon404Surfaces(t *testing.T) {
	m := withFetchEnv(t)
	m.pageErr = http.StatusInternalServerError
	resetRootCmdArgs()

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs([]string{"fetch", fetchHexID})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error on 500 from /pages, got nil")
	}
	if got := m.count("GET /databases/" + fetchDashedID); got != 0 {
		t.Errorf("non-404 page error should not fall through to /databases (count=%d)", got)
	}
}

// TestFetch_IsNotFound exercises the helper directly so the wrap-aware
// branch is covered. The dispatcher relies on the wrapped form because
// every Get layers its own fmt.Errorf around the underlying decode error.
func TestFetch_IsNotFound(t *testing.T) {
	if isNotFound(nil) {
		t.Error("isNotFound(nil) must be false")
	}
	plain := fmt.Errorf("unexpected status 404: object_not_found")
	if !isNotFound(plain) {
		t.Error("isNotFound: plain 404 error not detected")
	}
	wrapped := fmt.Errorf("get page: %w", plain)
	if !isNotFound(wrapped) {
		t.Error("isNotFound: wrapped 404 error not detected")
	}
	other := fmt.Errorf("unexpected status 500: server_error")
	if isNotFound(other) {
		t.Error("isNotFound: 500 must not be classified as 404")
	}
}

// TestFetch_PagePlainTitle covers the title extractor branches.
func TestFetch_PagePlainTitle(t *testing.T) {
	if got := pagePlainTitle(nil); got != "" {
		t.Errorf("pagePlainTitle(nil) = %q, want empty", got)
	}
	page := &utils.Page{Properties: map[string]interface{}{
		"Name": map[string]interface{}{
			"title": []interface{}{
				map[string]interface{}{"plain_text": "Hello"},
			},
		},
	}}
	if got := pagePlainTitle(page); got != "Hello" {
		t.Errorf("pagePlainTitle = %q, want Hello", got)
	}
	bare := &utils.Page{Properties: map[string]interface{}{}}
	if got := pagePlainTitle(bare); got != "" {
		t.Errorf("pagePlainTitle(empty) = %q, want empty", got)
	}
}

// TestFetch_DatabasePlainTitle covers the database title joiner.
func TestFetch_DatabasePlainTitle(t *testing.T) {
	if got := databasePlainTitle(nil); got != "" {
		t.Errorf("databasePlainTitle(nil) = %q, want empty", got)
	}
	db := &utils.Database{Title: []utils.RichText{
		{PlainText: "Foo "},
		{PlainText: "Bar"},
	}}
	if got := databasePlainTitle(db); got != "Foo Bar" {
		t.Errorf("databasePlainTitle = %q, want 'Foo Bar'", got)
	}
}
