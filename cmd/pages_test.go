// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// findPagesSubcommand walks the pages command's children and returns the
// child matching name, or fails the test.
func findPagesSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	pagesC := findTopLevelCmd(t, "pages")
	for _, c := range pagesC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("pages subcommand %q not found", name)
	return nil
}

// TestPagesCmdRegistered confirms every expected pages subcommand is wired
// onto the root command.
func TestPagesCmdRegistered(t *testing.T) {
	pagesC := findTopLevelCmd(t, "pages")
	want := map[string]bool{
		"get": false, "create": false, "update": false,
		"archive": false, "unarchive": false, "move": false, "duplicate": false,
	}
	for _, c := range pagesC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("pages subcommand %q not registered", name)
		}
	}
}

// TestPagesCreateFlags asserts the create subcommand exposes --parent and --title.
func TestPagesCreateFlags(t *testing.T) {
	create := findPagesSubcommand(t, "create")
	if create.Flag("parent") == nil {
		t.Error("pages create: --parent flag missing")
	}
	if create.Flag("title") == nil {
		t.Error("pages create: --title flag missing")
	}
}

// TestPagesUpdateFlags asserts the update subcommand exposes --title and --property.
func TestPagesUpdateFlags(t *testing.T) {
	update := findPagesSubcommand(t, "update")
	if update.Flag("title") == nil {
		t.Error("pages update: --title flag missing")
	}
	prop := update.Flag("property")
	if prop == nil {
		t.Fatal("pages update: --property flag missing")
	}
	if prop.Value.Type() != "stringArray" {
		t.Errorf("pages update: --property type=%q want stringArray", prop.Value.Type())
	}

	// update requires exactly one positional arg (the page id).
	if err := update.Args(update, []string{}); err == nil {
		t.Error("pages update: expected error on zero args")
	}
	if err := update.Args(update, []string{"id"}); err != nil {
		t.Errorf("pages update: expected no error on one arg, got %v", err)
	}
}

// TestPagesMoveFlags confirms --parent is present on move.
func TestPagesMoveFlags(t *testing.T) {
	move := findPagesSubcommand(t, "move")
	if move.Flag("parent") == nil {
		t.Error("pages move: --parent flag missing")
	}
}

// TestPagesDuplicateFlags confirms --parent is present on duplicate.
func TestPagesDuplicateFlags(t *testing.T) {
	dup := findPagesSubcommand(t, "duplicate")
	if dup.Flag("parent") == nil {
		t.Error("pages duplicate: --parent flag missing")
	}
}

// TestPagesArgs confirms each subcommand's positional-arg contract.
func TestPagesArgs(t *testing.T) {
	for _, name := range []string{"get", "update", "archive", "unarchive", "move", "duplicate"} {
		sub := findPagesSubcommand(t, name)
		if err := sub.Args(sub, []string{}); err == nil {
			t.Errorf("pages %s: expected error on zero args", name)
		}
		if err := sub.Args(sub, []string{"id"}); err != nil {
			t.Errorf("pages %s: expected no error on one arg, got %v", name, err)
		}
	}
}

// TestPagesParseProperty covers the key=value parser shared by pages update.
func TestPagesParseProperty(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantErr bool
	}{
		{name: "happy", input: "Status=Done", wantKey: "Status"},
		{name: "value with equals", input: "Note=a=b", wantKey: "Note"},
		{name: "missing equals", input: "oops", wantErr: true},
		{name: "empty key", input: "=nope", wantErr: true},
		{name: "whitespace key", input: " =v", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, val, err := parseProperty(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if key != tt.wantKey {
				t.Errorf("key=%q want %q", key, tt.wantKey)
			}
			if val == nil {
				t.Error("expected non-nil value payload")
			}
		})
	}
}

// pagesDispatchServer swaps the cmd-layer mock with a pages-aware handler.
// It returns a counter map keyed by method+path and a close func.
type pagesDispatchServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls map[string]int64
}

func newPagesDispatchServer(t *testing.T) *pagesDispatchServer {
	t.Helper()
	d := &pagesDispatchServer{calls: map[string]int64{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.calls[r.Method+" "+r.URL.Path]++
		d.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
			writeDispatchPage(w, strings.TrimPrefix(r.URL.Path, "/pages/"))
		case r.Method == http.MethodPost && r.URL.Path == "/pages":
			writeDispatchPage(w, "newPageID")
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/pages/"):
			writeDispatchPage(w, strings.TrimPrefix(r.URL.Path, "/pages/"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/children"):
			_ = json.NewEncoder(w).Encode(utils.BlockList{Results: []utils.Block{{
				Object: "block", ID: "b1", Type: "paragraph",
				Paragraph: &utils.RichTextBlock{RichText: []utils.RichText{{PlainText: "hi"}}},
			}}})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/children"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func writeDispatchPage(w http.ResponseWriter, id string) {
	page := map[string]interface{}{
		"object":           "page",
		"id":               id,
		"created_time":     "2026-04-22T10:00:00.000Z",
		"last_edited_time": "2026-04-22T10:00:00.000Z",
		"url":              "https://notion.so/" + id,
		"parent":           map[string]interface{}{"type": "page_id", "page_id": "parent"},
		"properties":       map[string]interface{}{},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func (d *pagesDispatchServer) count(key string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[key]
}

// withPagesEnv swaps in the pages dispatch server and sets env the way the
// blocks tests do.
func withPagesEnv(t *testing.T) *pagesDispatchServer {
	t.Helper()
	// Inherit the usual cmd-layer env (.env, HOME, etc.), but redirect
	// utils.baseURL to our pages-aware server.
	_ = withCmdEnv(t)
	d := newPagesDispatchServer(t)
	priorBaseURL := utils.GetBaseURL()
	utils.SetBaseURL(d.srv.URL)
	t.Cleanup(func() { utils.SetBaseURL(priorBaseURL) })
	return d
}

// resetPagesFlags wipes the package-level flag vars between tests. cobra
// persists bound flag values across executions.
func resetPagesFlags() {
	pagesCreateParent = ""
	pagesCreateTitle = ""
	pagesUpdateTitle = ""
	pagesUpdateProps = nil
	pagesMoveParent = ""
	pagesDuplicateParent = ""
}

// TestPagesGetDispatch runs `pages get <id>` end-to-end against the mock.
func TestPagesGetDispatch(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "get", "pageID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("GET /pages/pageID"); got == 0 {
		t.Errorf("pages get did not GET /pages/pageID (calls=%v)", d.calls)
	}
}

// TestPagesCreateDispatch runs `pages create --parent p --title t`.
func TestPagesCreateDispatch(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "create", "--parent", "parentID", "--title", "New thing"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("POST /pages"); got != 1 {
		t.Errorf("POST /pages count=%d want 1 (calls=%v)", got, d.calls)
	}
}

// TestPagesCreateMissingParent confirms --parent is required.
func TestPagesCreateMissingParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "create", "--title", "x"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// No POST should have happened.
	if got := d.count("POST /pages"); got != 0 {
		t.Errorf("expected no POST /pages when --parent missing, got %d", got)
	}
}

// TestPagesUpdateDispatch runs `pages update id --title ... --property k=v`.
func TestPagesUpdateDispatch(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "update", "pageID", "--title", "Renamed", "--property", "Status=Done"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("PATCH /pages/pageID"); got != 1 {
		t.Errorf("PATCH /pages/pageID count=%d want 1", got)
	}
}

// TestPagesArchiveUnarchiveDispatch covers both archive commands.
func TestPagesArchiveUnarchiveDispatch(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "archive", "pageID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("archive Execute: %v", err)
	}
	resetRootCmdArgs()
	rootCmd.SetArgs([]string{"pages", "unarchive", "pageID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unarchive Execute: %v", err)
	}

	if got := d.count("PATCH /pages/pageID"); got != 2 {
		t.Errorf("PATCH /pages/pageID count=%d want 2", got)
	}
}

// TestPagesMoveDispatch runs `pages move id --parent newParent`.
func TestPagesMoveDispatch(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "move", "pageID", "--parent", "newParentID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("PATCH /pages/pageID"); got == 0 {
		t.Errorf("pages move did not PATCH /pages/pageID")
	}
}

// TestPagesMoveMissingParent confirms --parent is required for move.
func TestPagesMoveMissingParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "move", "pageID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("PATCH /pages/pageID"); got != 0 {
		t.Errorf("expected no PATCH when --parent missing, got %d", got)
	}
}

// TestPagesDuplicateDispatch confirms the duplicate emulation sequence.
func TestPagesDuplicateDispatch(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "duplicate", "srcID", "--parent", "parentID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := d.count("POST /pages"); got != 1 {
		t.Errorf("duplicate POST /pages count=%d want 1", got)
	}
	if got := d.count("GET /blocks/srcID/children"); got == 0 {
		t.Errorf("duplicate did not GET source children")
	}
	if got := d.count("PATCH /blocks/newPageID/children"); got == 0 {
		t.Errorf("duplicate did not PATCH new page children (calls=%v)", d.calls)
	}
}

// TestPagesDuplicateMissingParent confirms --parent is required.
func TestPagesDuplicateMissingParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "duplicate", "srcID"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := d.count("POST /pages"); got != 0 {
		t.Errorf("expected no POST /pages when --parent missing, got %d", got)
	}
}
