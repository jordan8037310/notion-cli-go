// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

// TestPagesParseProperty_TypedShapes pins the wire shape parseProperty
// emits for each typed-property prefix. These are the payloads Notion's
// PATCH /v1/pages/{id} validates against; sending the wrong shape 400s
// with "<key> is expected to be <type>" — see issue #51.
func TestPagesParseProperty_TypedShapes(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantKey   string
		wantValue map[string]interface{}
	}{
		{
			name:    "status",
			input:   "Status=status:In Progress",
			wantKey: "Status",
			wantValue: map[string]interface{}{
				"status": map[string]interface{}{"name": "In Progress"},
			},
		},
		{
			name:    "select",
			input:   "Brand=select:FacetInteractive.com",
			wantKey: "Brand",
			wantValue: map[string]interface{}{
				"select": map[string]interface{}{"name": "FacetInteractive.com"},
			},
		},
		{
			name:    "multi_select with whitespace",
			input:   "Tags=multi_select:alpha, beta ,gamma",
			wantKey: "Tags",
			wantValue: map[string]interface{}{
				"multi_select": []map[string]interface{}{
					{"name": "alpha"},
					{"name": "beta"},
					{"name": "gamma"},
				},
			},
		},
		{
			name:    "multi_select empty clears",
			input:   "Tags=multi_select:",
			wantKey: "Tags",
			wantValue: map[string]interface{}{
				"multi_select": []map[string]interface{}{},
			},
		},
		{
			name:      "number",
			input:     "Count=number:42.5",
			wantKey:   "Count",
			wantValue: map[string]interface{}{"number": 42.5},
		},
		{
			name:      "checkbox true",
			input:     "Done=checkbox:true",
			wantKey:   "Done",
			wantValue: map[string]interface{}{"checkbox": true},
		},
		{
			name:      "checkbox false",
			input:     "Done=checkbox:false",
			wantKey:   "Done",
			wantValue: map[string]interface{}{"checkbox": false},
		},
		{
			name:    "date single",
			input:   "Due=date:2026-05-01",
			wantKey: "Due",
			wantValue: map[string]interface{}{
				"date": map[string]interface{}{"start": "2026-05-01"},
			},
		},
		{
			name:    "date range",
			input:   "Due=date:2026-05-01..2026-05-08",
			wantKey: "Due",
			wantValue: map[string]interface{}{
				"date": map[string]interface{}{"start": "2026-05-01", "end": "2026-05-08"},
			},
		},
		{
			name:      "url with colons in value",
			input:     "Site=url:https://notion.so/abc",
			wantKey:   "Site",
			wantValue: map[string]interface{}{"url": "https://notion.so/abc"},
		},
		{
			name:      "email",
			input:     "Contact=email:hi@example.com",
			wantKey:   "Contact",
			wantValue: map[string]interface{}{"email": "hi@example.com"},
		},
		{
			name:      "phone alias",
			input:     "Cell=phone:+1-555-0100",
			wantKey:   "Cell",
			wantValue: map[string]interface{}{"phone_number": "+1-555-0100"},
		},
		{
			name:      "phone_number canonical",
			input:     "Cell=phone_number:+1-555-0100",
			wantKey:   "Cell",
			wantValue: map[string]interface{}{"phone_number": "+1-555-0100"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, val, err := parseProperty(tt.input)
			if err != nil {
				t.Fatalf("parseProperty(%q): %v", tt.input, err)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if !propertyEqual(val, tt.wantValue) {
				t.Errorf("payload mismatch:\n got: %#v\nwant: %#v", val, tt.wantValue)
			}
		})
	}
}

// TestPagesParseProperty_RawJSONPassthrough confirms a JSON object
// value is forwarded verbatim. This is the power-user escape hatch for
// property shapes the typed prefixes don't cover (relations, people,
// files, formula targets, rollup overrides, etc.).
func TestPagesParseProperty_RawJSONPassthrough(t *testing.T) {
	key, val, err := parseProperty(`Status={"select":{"name":"Done"}}`)
	if err != nil {
		t.Fatalf("parseProperty: %v", err)
	}
	if key != "Status" {
		t.Errorf("key = %q, want Status", key)
	}
	want := map[string]interface{}{"select": map[string]interface{}{"name": "Done"}}
	if !propertyEqual(val, want) {
		t.Errorf("payload mismatch:\n got: %#v\nwant: %#v", val, want)
	}
}

// TestPagesParseProperty_BareValueFallsBackToRichText guarantees
// scripts that have always passed `Key=Value` keep working — the bare
// form still serialises as rich_text.
func TestPagesParseProperty_BareValueFallsBackToRichText(t *testing.T) {
	_, val, err := parseProperty("Note=hello world")
	if err != nil {
		t.Fatalf("parseProperty: %v", err)
	}
	rt, ok := val["rich_text"].([]map[string]interface{})
	if !ok || len(rt) != 1 {
		t.Fatalf("expected rich_text slice with one segment, got %#v", val)
	}
	text, _ := rt[0]["text"].(map[string]interface{})
	if text["content"] != "hello world" {
		t.Errorf("rich_text content = %v, want 'hello world'", text["content"])
	}
}

// TestPagesParseProperty_TypedErrors covers the malformed-input paths
// for the typed prefixes (number/checkbox/JSON).
func TestPagesParseProperty_TypedErrors(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"bad number", "Count=number:not-a-number", "not a valid float"},
		{"bad checkbox", "Done=checkbox:maybe", "not a boolean"},
		{"bad json", `X={not valid json`, "failed to parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseProperty(tc.input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

// TestPagesParseProperty_UnknownPrefixFallsThrough confirms that a
// value of "weird:foo" (where "weird" isn't a known type prefix) is
// treated as bare rich_text rather than erroring — preserves
// back-compat for legacy values that happen to contain a colon.
func TestPagesParseProperty_UnknownPrefixFallsThrough(t *testing.T) {
	_, val, err := parseProperty("Note=weird:foo")
	if err != nil {
		t.Fatalf("parseProperty: %v", err)
	}
	rt, ok := val["rich_text"].([]map[string]interface{})
	if !ok || len(rt) != 1 {
		t.Fatalf("expected rich_text fallback, got %#v", val)
	}
	text, _ := rt[0]["text"].(map[string]interface{})
	if text["content"] != "weird:foo" {
		t.Errorf("rich_text content = %v, want 'weird:foo' verbatim", text["content"])
	}
}

// propertyEqual is a recursive comparator that handles the loose
// map[string]interface{} shapes parseProperty emits. reflect.DeepEqual
// would work, but it treats []map vs []interface{} as unequal; the
// helper normalises both sides before comparing.
func propertyEqual(a, b map[string]interface{}) bool {
	aj, errA := json.Marshal(a)
	bj, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(aj) == string(bj)
}

// pagesDispatchServer swaps the cmd-layer mock with a pages-aware handler.
// It returns a counter map keyed by method+path, the most recent request
// body for each (method+path), and a close func. Body capture lets tests
// assert wire shapes — e.g. the parent discriminator on POST /pages —
// without standing up a separate fixture.
type pagesDispatchServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  map[string]int64
	bodies map[string][]byte
}

func newPagesDispatchServer(t *testing.T) *pagesDispatchServer {
	t.Helper()
	d := &pagesDispatchServer{calls: map[string]int64{}, bodies: map[string][]byte{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
		}
		d.mu.Lock()
		d.calls[key]++
		if len(body) > 0 {
			d.bodies[key] = body
		}
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

// body returns the most recent request body captured for the given
// method+path key. Returns nil when nothing was recorded; tests should
// fail loudly on a nil body for endpoints they expect to have hit.
func (d *pagesDispatchServer) body(key string) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bodies[key]
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
	pagesCreateParentDB = ""
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

// TestPagesCreateDispatch runs `pages create --parent p --title t` and
// asserts the body serialises a page-id parent (not a database id).
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
	body := d.body("POST /pages")
	if body == nil {
		t.Fatal("POST /pages: no body captured")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	parent, _ := got["parent"].(map[string]interface{})
	if parent["page_id"] != "parentID" {
		t.Errorf("parent.page_id=%v want parentID (parent=%v)", parent["page_id"], parent)
	}
	if _, hasDB := parent["database_id"]; hasDB {
		t.Errorf("parent must not include database_id when --parent set: %v", parent)
	}
}

// TestPagesCreateDatabaseParent confirms --parent-database emits the
// database_id discriminator the API requires for database-parented
// pages. Regression test for PR #50 review [P1].
func TestPagesCreateDatabaseParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "create", "--parent-database", "dbID", "--title", "Row"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	body := d.body("POST /pages")
	if body == nil {
		t.Fatal("POST /pages: no body captured")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	parent, _ := got["parent"].(map[string]interface{})
	if parent["database_id"] != "dbID" {
		t.Errorf("parent.database_id=%v want dbID (parent=%v)", parent["database_id"], parent)
	}
	if _, hasPage := parent["page_id"]; hasPage {
		t.Errorf("parent must not include page_id when --parent-database set: %v", parent)
	}
}

// TestPagesCreateBothParentFlagsErrors confirms the CLI rejects the
// ambiguous case where both flags are populated, before any HTTP call.
func TestPagesCreateBothParentFlagsErrors(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetArgs([]string{"pages", "create", "--parent", "p", "--parent-database", "db"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when both --parent and --parent-database set, got nil")
	}
	if got := d.count("POST /pages"); got != 0 {
		t.Errorf("expected no POST /pages on conflicting flags, got %d", got)
	}
}

// TestPagesCreateMissingParent confirms --parent is required and that the
// missing-flag case propagates a non-nil error out of RunE so cobra sets a
// non-zero shell exit code.
func TestPagesCreateMissingParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "create", "--title", "x"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when --parent missing, got nil")
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

// TestPagesMoveMissingParent confirms --parent is required for move and
// that RunE returns a non-nil error for the missing-flag case.
func TestPagesMoveMissingParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "move", "pageID"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when --parent missing, got nil")
	}
	if got := d.count("PATCH /pages/pageID"); got != 0 {
		t.Errorf("expected no PATCH when --parent missing, got %d", got)
	}
}

// TestPagesMissingAPIKey asserts that when NOTION_API_KEY resolves empty,
// newPageClient returns ErrMissingAPIKey (wrapped) instead of silently
// building a Client that would later 401.
func TestPagesMissingAPIKey(t *testing.T) {
	_ = withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	// Blank out the API key after the usual cmd env has been set up. The
	// .env file in the test cwd is empty, so os.LookupEnv sees the empty
	// value we Setenv here, and SetAPIConfig returns "" for apiKey.
	t.Setenv("NOTION_API_KEY", "")

	pc, err := newPageClient()
	if err == nil {
		t.Fatal("expected error when NOTION_API_KEY empty, got nil")
	}
	if pc != nil {
		t.Errorf("expected nil client on error, got %+v", pc)
	}
	if !errors.Is(err, utils.ErrMissingAPIKey) {
		t.Errorf("expected errors.Is ErrMissingAPIKey, got %v", err)
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

// TestPagesDuplicateMissingParent confirms --parent is required and that
// RunE surfaces the missing-flag error so shell callers see a non-zero exit.
func TestPagesDuplicateMissingParent(t *testing.T) {
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"pages", "duplicate", "srcID"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when --parent missing, got nil")
	}
	if got := d.count("POST /pages"); got != 0 {
		t.Errorf("expected no POST /pages when --parent missing, got %d", got)
	}
}
