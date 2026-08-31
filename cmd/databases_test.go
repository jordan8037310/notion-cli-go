// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// findDatabasesSubcommand walks the databases command's children and returns
// the child matching name, or fails the test.
func findDatabasesSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	databasesC := findTopLevelCmd(t, "databases")
	for _, c := range databasesC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("databases subcommand %q not found", name)
	return nil
}

// TestDatabasesCmdRegistered confirms every expected databases subcommand is
// wired onto the root command.
func TestDatabasesCmdRegistered(t *testing.T) {
	databasesC := findTopLevelCmd(t, "databases")
	want := map[string]bool{"get": false, "query": false, "create": false, "update": false}
	for _, c := range databasesC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("databases subcommand %q not registered", name)
		}
	}
}

// TestDatabasesQueryFlags asserts the query subcommand exposes the three
// flags that issue #6 calls out.
func TestDatabasesQueryFlags(t *testing.T) {
	query := findDatabasesSubcommand(t, "query")
	for _, name := range []string{"filter-json", "sort-json", "limit"} {
		if query.Flag(name) == nil {
			t.Errorf("databases query: --%s flag missing", name)
		}
	}
}

// TestDatabasesCreateFlags asserts the create subcommand exposes --parent,
// --title, and --properties-json.
func TestDatabasesCreateFlags(t *testing.T) {
	create := findDatabasesSubcommand(t, "create")
	for _, name := range []string{"parent", "title", "properties-json"} {
		if create.Flag(name) == nil {
			t.Errorf("databases create: --%s flag missing", name)
		}
	}
}

// TestDatabasesUpdateFlags asserts the update subcommand exposes --title and
// --properties-json and requires exactly one positional arg.
func TestDatabasesUpdateFlags(t *testing.T) {
	update := findDatabasesSubcommand(t, "update")
	for _, name := range []string{"title", "properties-json"} {
		if update.Flag(name) == nil {
			t.Errorf("databases update: --%s flag missing", name)
		}
	}
	if err := update.Args(update, []string{}); err == nil {
		t.Error("databases update: expected error on zero args")
	}
	if err := update.Args(update, []string{"id"}); err != nil {
		t.Errorf("databases update: expected no error on one arg, got %v", err)
	}
}

// TestDatabasesArgs confirms each subcommand's positional-arg contract.
func TestDatabasesArgs(t *testing.T) {
	for _, name := range []string{"get", "query", "update"} {
		sub := findDatabasesSubcommand(t, name)
		if err := sub.Args(sub, []string{}); err == nil {
			t.Errorf("databases %s: expected error on zero args", name)
		}
		if err := sub.Args(sub, []string{"id"}); err != nil {
			t.Errorf("databases %s: expected no error on one arg, got %v", name, err)
		}
	}
}

// TestReadJSONFile covers the --filter-json / --sort-json parsing helper.
// Empty path returns (nil, nil); malformed JSON surfaces a clear error; a
// missing path surfaces a read error.
func TestReadJSONFile(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.json")
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(goodPath, []byte(`{"property":"Status"}`), 0o600); err != nil {
		t.Fatalf("write good.json: %v", err)
	}
	if err := os.WriteFile(badPath, []byte(`{not-json`), 0o600); err != nil {
		t.Fatalf("write bad.json: %v", err)
	}

	t.Run("empty path", func(t *testing.T) {
		out, err := readJSONFile("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != nil {
			t.Errorf("expected nil, got %s", string(out))
		}
	})
	t.Run("valid file", func(t *testing.T) {
		out, err := readJSONFile(goodPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(out), "Status") {
			t.Errorf("expected content to round-trip, got %s", string(out))
		}
	})
	t.Run("malformed file", func(t *testing.T) {
		_, err := readJSONFile(badPath)
		if err == nil {
			t.Fatal("expected error on malformed JSON")
		}
		if !strings.Contains(err.Error(), "parse") {
			t.Errorf("error=%q should mention parse", err.Error())
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := readJSONFile(filepath.Join(dir, "nope.json"))
		if err == nil {
			t.Fatal("expected error on missing file")
		}
		if !strings.Contains(err.Error(), "read") {
			t.Errorf("error=%q should mention read", err.Error())
		}
	})
}

// TestReadPropertiesFile covers the --properties-json helper: empty path
// returns nil, well-formed JSON decodes, malformed JSON errors.
func TestReadPropertiesFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "schema.json")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(good, []byte(`{"Name":{"title":{}},"Status":{"select":{}}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(bad, []byte(`[`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := readPropertiesFile(""); err != nil {
		t.Errorf("empty path should return nil,nil; got %v", err)
	}
	props, err := readPropertiesFile(good)
	if err != nil {
		t.Fatalf("good file: %v", err)
	}
	if _, ok := props["Name"]; !ok {
		t.Error("expected Name property in parsed result")
	}
	if _, err := readPropertiesFile(bad); err == nil {
		t.Error("expected error on malformed JSON")
	}
}

// databasesDispatchServer mirrors pagesDispatchServer: a counter-keyed mock
// that answers every /databases path the cmd layer might touch. Per-test
// behavior is adjusted by swapping the optional handler override.
type databasesDispatchServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    map[string]int64
	queryIdx int
}

func newDatabasesDispatchServer(t *testing.T) *databasesDispatchServer {
	t.Helper()
	d := &databasesDispatchServer{calls: map[string]int64{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.calls[r.Method+" "+r.URL.Path]++
		d.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query"):
			writeDispatchQuery(w, d)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/"):
			writeDispatchDatabase(w, strings.TrimPrefix(r.URL.Path, "/databases/"))
		case r.Method == http.MethodPost && r.URL.Path == "/databases":
			writeDispatchDatabase(w, "newDBID")
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/databases/"):
			writeDispatchDatabase(w, strings.TrimPrefix(r.URL.Path, "/databases/"))
		// Schema writes land on the data source (2025-09-03 onward).
		// "dsID" is the only data source this fixture knows; anything
		// else gets the 404 a wrong-namespace id really receives.
		case strings.HasPrefix(r.URL.Path, "/data_sources/"):
			id := strings.TrimPrefix(r.URL.Path, "/data_sources/")
			if id != "dsID" {
				http.Error(w, `{"object":"error","status":404,"code":"object_not_found"}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"data_source","id":"dsID","in_trash":false,
				"title":[{"type":"text","plain_text":"DS","text":{"content":"DS"}}],
				"parent":{"type":"database_id","database_id":"dbID"},
				"properties":{"Name":{"type":"title","title":{}}}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func writeDispatchDatabase(w http.ResponseWriter, id string) {
	payload := map[string]interface{}{
		"object":           "database",
		"id":               id,
		"created_time":     "2026-04-22T10:00:00.000Z",
		"last_edited_time": "2026-04-22T10:00:00.000Z",
		"url":              "https://notion.so/" + id,
		"title":            []map[string]interface{}{{"type": "text", "plain_text": id}},
		"parent":           map[string]interface{}{"type": "page_id", "page_id": "parent"},
		"properties":       map[string]interface{}{},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// writeDispatchQuery scripts a two-page query response so dispatch tests can
// assert pagination is followed and --limit truncation is honored.
func writeDispatchQuery(w http.ResponseWriter, d *databasesDispatchServer) {
	d.mu.Lock()
	idx := d.queryIdx
	d.queryIdx++
	d.mu.Unlock()

	page := utils.Page{Object: "page", ID: "p1", URL: "https://notion.so/p1", Properties: map[string]interface{}{}}
	resp := utils.QueryResponse{Object: "list", Results: []utils.Page{page}}
	if idx == 0 {
		resp.HasMore = true
		resp.NextCursor = "cur-2"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (d *databasesDispatchServer) count(key string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[key]
}

// withDatabasesEnv swaps in the databases dispatch server and sets env the
// way the pages dispatch tests do.
func withDatabasesEnv(t *testing.T) *databasesDispatchServer {
	t.Helper()
	_ = withCmdEnv(t)
	d := newDatabasesDispatchServer(t)
	priorBaseURL := utils.GetBaseURL()
	utils.SetBaseURL(d.srv.URL)
	t.Cleanup(func() { utils.SetBaseURL(priorBaseURL) })
	return d
}

// TestDatabasesGetDispatch runs `databases get <id>` end-to-end.
func TestDatabasesGetDispatch(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"databases", "get", "dbID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("GET /databases/dbID"); got == 0 {
		t.Errorf("databases get did not GET /databases/dbID (calls=%v)", d.calls)
	}
}

// TestDatabasesQueryDispatch exercises pagination: the scripted mock returns
// two pages, so the limit=0 case should POST twice.
func TestDatabasesQueryDispatch(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"databases", "query", "dbID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// PR #48 routes Query through /data_sources/{id}/query first
	// (Notion-Version 2026-03-11 default). The mock responds 200 to
	// either /data_sources or /databases query suffixes, so the count
	// lives on the new primary path.
	if got := d.count("POST /data_sources/dbID/query"); got != 2 {
		t.Errorf("POST query count=%d want 2 (calls=%v)", got, d.calls)
	}
}

// TestDatabasesQueryWithLimit verifies --limit truncates the pagination walk
// so only the first page is fetched when len(page1) >= limit.
func TestDatabasesQueryWithLimit(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"databases", "query", "dbID", "--limit", "1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("POST /data_sources/dbID/query"); got != 1 {
		t.Errorf("POST query count=%d want 1 when --limit 1 (calls=%v)", got, d.calls)
	}
}

// TestDatabasesQueryMalformedFilter is the error-path dispatch test: a
// malformed --filter-json file must surface a non-nil RunE error and prevent
// any HTTP call.
func TestDatabasesQueryMalformedFilter(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{oops`), 0o600); err != nil {
		t.Fatalf("write bad.json: %v", err)
	}

	rootCmd.SetArgs([]string{"databases", "query", "dbID", "--filter-json", bad})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error on malformed --filter-json, got nil")
	}
	// Neither probe path should have been reached when validation
	// fails before the request leaves the client.
	if got := d.count("POST /data_sources/dbID/query") + d.count("POST /databases/dbID/query"); got != 0 {
		t.Errorf("expected no POST when filter malformed, got %d", got)
	}
}

// TestDatabasesCreateDispatch runs `databases create --parent p --title t
// --properties-json file` and asserts the POST fires.
func TestDatabasesCreateDispatch(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schema, []byte(`{"Name":{"title":{}}}`), 0o600); err != nil {
		t.Fatalf("write schema.json: %v", err)
	}

	rootCmd.SetArgs([]string{
		"databases", "create",
		"--parent", "parentPageID",
		"--title", "My DB",
		"--properties-json", schema,
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("POST /databases"); got != 1 {
		t.Errorf("POST /databases count=%d want 1 (calls=%v)", got, d.calls)
	}
}

// TestDatabasesCreateMissingParent confirms --parent is required and that
// the missing-flag case propagates a non-nil error out of RunE.
func TestDatabasesCreateMissingParent(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"databases", "create", "--title", "x"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when --parent missing, got nil")
	}
	if got := d.count("POST /databases"); got != 0 {
		t.Errorf("expected no POST /databases when --parent missing, got %d", got)
	}
}

// TestDatabasesUpdateDispatch runs `databases update id --title t`.
func TestDatabasesUpdateDispatch(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"databases", "update", "dbID", "--title", "Renamed"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("PATCH /databases/dbID"); got != 1 {
		t.Errorf("PATCH /databases/dbID count=%d want 1", got)
	}
}

// TestDatabasesUpdateEmpty confirms update refuses to fire with neither
// --title nor --properties-json set.
func TestDatabasesUpdateEmpty(t *testing.T) {
	d := withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"databases", "update", "dbID"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when update has no fields, got nil")
	}
	if got := d.count("PATCH /databases/dbID"); got != 0 {
		t.Errorf("expected no PATCH when update empty, got %d", got)
	}
}

// TestDatabasesMissingAPIKey asserts that when NOTION_API_KEY resolves empty,
// newDatabaseClient returns ErrMissingAPIKey (wrapped).
func TestDatabasesMissingAPIKey(t *testing.T) {
	_ = withDatabasesEnv(t)
	resetDatabasesFlags()
	resetRootCmdArgs()
	t.Setenv("NOTION_API_KEY", "")

	dc, err := newDatabaseClient()
	if err == nil {
		t.Fatal("expected error when NOTION_API_KEY empty, got nil")
	}
	if dc != nil {
		t.Errorf("expected nil client on error, got %+v", dc)
	}
	if !errors.Is(err, utils.ErrMissingAPIKey) {
		t.Errorf("expected errors.Is ErrMissingAPIKey, got %v", err)
	}
}

// TestPrintDatabase covers the nil and happy-path branches of the print
// helper — the nil branch is a guard that must never write, and the happy
// path must emit indented JSON carrying the database id.
func TestPrintDatabase(t *testing.T) {
	var buf strings.Builder
	printDatabase(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("printDatabase(nil) wrote %q; want empty", buf.String())
	}
	buf.Reset()
	printDatabase(&buf, &utils.Database{ID: "dbX", URL: "https://notion.so/dbX"})
	if !strings.Contains(buf.String(), `"dbX"`) {
		t.Errorf("printDatabase output missing id: %s", buf.String())
	}
}

// TestPrintQueryResults covers the empty-results branch (routes the
// "No results." banner through the supplied writer so jq-piped NDJSON
// consumers don't see stdout-direct banner noise) and the happy-path
// NDJSON branch.
func TestPrintQueryResults(t *testing.T) {
	var buf strings.Builder
	printQueryResults(&buf, nil)
	if !strings.Contains(buf.String(), "No results.") {
		t.Errorf("empty results did not emit banner via writer; got %q", buf.String())
	}

	buf.Reset()
	printQueryResults(&buf, []utils.Page{
		{Object: "page", ID: "p1", URL: "u1", Properties: map[string]interface{}{}},
		{Object: "page", ID: "p2", URL: "u2", Properties: map[string]interface{}{}},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "{") {
			t.Errorf("line not JSON: %q", ln)
		}
	}
}

// TestDatabasesDataSourcesDispatch covers the discovery command added for
// issue #94. Notion's documented answer to "where do I find a
// data_source_id" is to retrieve the parent database and read its
// data_sources array; before this the CLI could tell a user their id was
// not queryable and offer no way to reach the id that is.
func TestDatabasesDataSourcesDispatch(t *testing.T) {
	srv := withCmdEnv(t)
	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/databases/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"database","id":"db-1",
				"title":[{"plain_text":"Tracker"}],
				"data_sources":[{"id":"ds-aaa","name":"Source A"},{"id":"ds-bbb","name":"Source B"}]}`))
			return
		}
		orig.ServeHTTP(w, r)
	})

	t.Run("human", func(t *testing.T) {
		resetDatabasesFlags()
		resetRootCmdArgs()
		var out bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&out)
		rootCmd.SetArgs([]string{"databases", "data-sources", "db-1"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		for _, want := range []string{"ds-aaa", "Source A", "ds-bbb", "Source B"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q:\n%s", want, out.String())
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		resetDatabasesFlags()
		resetRootCmdArgs()
		var out bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&out)
		rootCmd.SetArgs([]string{"databases", "data-sources", "db-1", "--json"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("want 2 NDJSON lines, got %d:\n%s", len(lines), out.String())
		}
		var first struct{ ID, Name string }
		if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
			t.Fatalf("line 0 not JSON: %v", err)
		}
		if first.ID != "ds-aaa" || first.Name != "Source A" {
			t.Errorf("first data source = %+v", first)
		}
	})
}

// TestDatabasesQueryDataSourceFlag confirms --data-source bypasses the
// positional id, which is the only way to pick a source on a multi-source
// database (issue #94).
func TestDatabasesQueryDataSourceFlag(t *testing.T) {
	srv := withCmdEnv(t)
	var queried string
	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query") {
			queried = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","results":[],"has_more":false}`))
			return
		}
		orig.ServeHTTP(w, r)
	})

	resetDatabasesFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"databases", "query", "db-container", "--data-source", "ds-aaa"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(queried, "ds-aaa") {
		t.Errorf("query hit %q, want the --data-source id ds-aaa", queried)
	}
	if strings.Contains(queried, "db-container") {
		t.Errorf("query used the positional container id %q despite --data-source", queried)
	}
}

// TestDatabasesUpdateSchemaDispatch is the cmd-layer guard for the endpoint
// split: `databases update <ds-id> --properties-json` must PATCH the data
// source and must NOT touch the database endpoint, whose 200-and-ignore
// behaviour is what silently dropped schemas before.
func TestDatabasesUpdateSchemaDispatch(t *testing.T) {
	d := withDatabasesEnv(t)

	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schema, []byte(`{"Priority":{"select":{}}}`), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	resetDatabasesFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"databases", "update", "dsID", "--properties-json", schema})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := d.count("PATCH /data_sources/dsID"); got != 1 {
		t.Errorf("PATCH /data_sources/dsID fired %d times, want 1", got)
	}
	if got := d.count("PATCH /databases/dsID"); got != 0 {
		t.Errorf("PATCH /databases/dsID fired %d times, want 0 — that endpoint ignores properties", got)
	}
	// The rendered payload must be the data source that actually answered,
	// not a database envelope. (The green banner itself is written by
	// color.Green straight to os.Stdout rather than cmd.OutOrStdout, so it
	// is not captured here — see issue #100.)
	if !strings.Contains(out.String(), `"object": "data_source"`) {
		t.Errorf("expected the data_source envelope in the output, got: %s", out.String())
	}
}
