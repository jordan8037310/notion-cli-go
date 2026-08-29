package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// searchHandlerStats captures what the mock server saw on POST /search so
// dispatch tests can assert on the outgoing request.
type searchHandlerStats struct {
	calls  int64
	query  atomic.Value // last request query string
	cursor atomic.Value // last request start_cursor
	filter atomic.Value // last request filter.value
}

// overlaySearchHandler wraps cmdMockServer's handler with POST /search
// support. The test-supplied resp function gets the decoded request body
// and returns the JSON object to write.
func overlaySearchHandler(t *testing.T, stats *searchHandlerStats, resp func(req map[string]interface{}) map[string]interface{}) {
	t.Helper()
	srv := withCmdEnv(t)
	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/search" {
			atomic.AddInt64(&stats.calls, 1)
			body, _ := io.ReadAll(r.Body)
			var in map[string]interface{}
			_ = json.Unmarshal(body, &in)
			if q, ok := in["query"].(string); ok {
				stats.query.Store(q)
			}
			if c, ok := in["start_cursor"].(string); ok {
				stats.cursor.Store(c)
			}
			if f, ok := in["filter"].(map[string]interface{}); ok {
				if v, ok := f["value"].(string); ok {
					stats.filter.Store(v)
				}
			}
			out := resp(in)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		orig.ServeHTTP(w, r)
	})
}

// seedResult builds a utils.SearchResult from a raw JSON string via the
// real UnmarshalJSON so Raw is populated exactly the way the production
// code path would see it.
func seedResult(t *testing.T, raw string) utils.SearchResult {
	t.Helper()
	var r utils.SearchResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("seedResult: %v", err)
	}
	return r
}

// TestSearchCmdRegistered verifies `search` is a top-level command.
func TestSearchCmdRegistered(t *testing.T) {
	c := findTopLevelCmd(t, "search")
	if c.Use == "" {
		t.Fatal("search command has empty Use")
	}
}

// TestSearchCmdFlags asserts every documented flag is declared with the
// right default and type. This is a pure metadata check — no network.
func TestSearchCmdFlags(t *testing.T) {
	c := findTopLevelCmd(t, "search")

	tests := []struct {
		flag     string
		typeName string
		def      string
	}{
		{"type", "string", ""},
		{"limit", "int", "0"},
		{"page-size", "int", "0"},
		// Note: --json moved from a search-local flag to a persistent
		// flag on rootCmd. Verified separately in TestRootPersistentFlags.
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			f := c.Flag(tt.flag)
			if f == nil {
				t.Fatalf("--%s flag not registered", tt.flag)
			}
			if f.Value.Type() != tt.typeName {
				t.Errorf("--%s type = %q want %q", tt.flag, f.Value.Type(), tt.typeName)
			}
			if f.DefValue != tt.def {
				t.Errorf("--%s default = %q want %q", tt.flag, f.DefValue, tt.def)
			}
		})
	}
}

// TestSearchCmdArgs asserts the command accepts 0 or 1 positional args.
func TestSearchCmdArgs(t *testing.T) {
	c := findTopLevelCmd(t, "search")
	if err := c.Args(c, []string{}); err != nil {
		t.Errorf("zero args should be allowed: %v", err)
	}
	if err := c.Args(c, []string{"roadmap"}); err != nil {
		t.Errorf("one arg should be allowed: %v", err)
	}
	if err := c.Args(c, []string{"a", "b"}); err == nil {
		t.Error("two args should fail")
	}
}

// TestBuildSearchFilter covers the --type flag translation table.
func TestBuildSearchFilter(t *testing.T) {
	tests := []struct {
		in      string
		wantVal string
		wantErr bool
	}{
		{"", "", false},
		{"pages", "page", false},
		{"page", "page", false},
		// Notion-Version 2026-03-11 returns data_source objects; the
		// CLI's --type databases alias maps to that wire value (#79).
		{"databases", "data_source", false},
		{"database", "data_source", false},
		{"data_source", "data_source", false},
		{"data_sources", "data_source", false},
		{"users", "", true},
		{"foo", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := buildSearchFilter(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.wantVal == "" {
				if got != nil {
					t.Errorf("got filter=%+v, want nil", got)
				}
				return
			}
			if got == nil || got.Value != tt.wantVal {
				t.Errorf("got=%+v want Value=%q", got, tt.wantVal)
			}
		})
	}
}

// TestSearchCmdDispatch runs `notioncli search roadmap` end-to-end through
// rootCmd, confirms the POST /search request was made, and checks that the
// table output names the result.
func TestSearchCmdDispatch(t *testing.T) {
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"object":   "list",
			"has_more": false,
			"results": []map[string]interface{}{
				{
					"object":           "page",
					"id":               "pg-1",
					"url":              "https://notion.so/pg-1",
					"last_edited_time": "2026-04-22T10:00:00.000Z",
					"icon":             map[string]interface{}{"type": "emoji", "emoji": "📄"},
					"properties": map[string]interface{}{
						"Name": map[string]interface{}{
							"type": "title",
							"title": []map[string]interface{}{
								{"plain_text": "Roadmap Q2"},
							},
						},
					},
				},
			},
		}
	})

	resetSearchFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"search", "roadmap"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(search): %v", err)
	}

	if atomic.LoadInt64(&stats.calls) == 0 {
		t.Fatal("search did not POST /search")
	}
	if q, _ := stats.query.Load().(string); q != "roadmap" {
		t.Errorf("query sent to API = %q, want %q", q, "roadmap")
	}
	if !strings.Contains(out.String(), "Roadmap Q2") {
		t.Errorf("output did not include title: %q", out.String())
	}
}

// TestSearchCmdTypeFilter verifies --type databases translates to the
// right API filter payload.
func TestSearchCmdTypeFilter(t *testing.T) {
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"object":   "list",
			"has_more": false,
			"results":  []map[string]interface{}{},
		}
	})

	resetSearchFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"search", "x", "--type", "databases"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(search --type databases): %v", err)
	}

	// On Notion-Version 2026-03-11 the wire value is `data_source`
	// (issue #79). The CLI keeps `--type databases` as the user-facing
	// alias because that's the term users still type.
	if got, _ := stats.filter.Load().(string); got != "data_source" {
		t.Errorf("filter sent = %q, want %q", got, "data_source")
	}
}

// TestSearchCmdJSONOutput verifies --json emits one NDJSON line per result.
func TestSearchCmdJSONOutput(t *testing.T) {
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"object":   "list",
			"has_more": false,
			"results": []map[string]interface{}{
				{"object": "page", "id": "pg-a", "url": "u1", "last_edited_time": "2026-04-22T10:00:00.000Z"},
				{"object": "database", "id": "db-b", "url": "u2", "last_edited_time": "2026-04-22T11:00:00.000Z"},
			},
		}
	})

	resetSearchFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"search", "x", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(search --json): %v", err)
	}

	var jsonLines []string
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "{") {
			jsonLines = append(jsonLines, l)
		}
	}
	if len(jsonLines) != 2 {
		t.Fatalf("want 2 NDJSON lines, got %d: %q", len(jsonLines), out.String())
	}
	for i, line := range jsonLines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v (%q)", i, err, line)
		}
	}
}

// TestSearchCmdInvalidType verifies that --type foo surfaces an error and
// does NOT issue a search request.
func TestSearchCmdInvalidType(t *testing.T) {
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"object": "list", "has_more": false, "results": []map[string]interface{}{}}
	})

	resetSearchFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"search", "x", "--type", "bogus"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error on invalid --type, got nil")
	}
	if atomic.LoadInt64(&stats.calls) != 0 {
		t.Errorf("search request should not be issued on bad --type; got %d calls", stats.calls)
	}
}

// TestExtractSearchTitle covers database + page + fallback shapes.
func TestExtractSearchTitle(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "database title at top level",
			raw:  `{"object":"database","id":"db","title":[{"plain_text":"Tracker"}]}`,
			want: "Tracker",
		},
		{
			name: "page title via properties",
			raw:  `{"object":"page","id":"pg","properties":{"Name":{"type":"title","title":[{"plain_text":"Hello"}]}}}`,
			want: "Hello",
		},
		{
			name: "page title via renamed property",
			raw:  `{"object":"page","id":"pg","properties":{"Subject":{"type":"title","title":[{"plain_text":"Renamed"}]}}}`,
			want: "Renamed",
		},
		{
			// Issue #77: only the first run was read, so a title split
			// across runs by formatting/mentions/links was truncated.
			name: "database title split across runs",
			raw:  `{"object":"database","id":"db","title":[{"plain_text":"Project: "},{"plain_text":"Q2 Plan"},{"plain_text":""}]}`,
			want: "Project: Q2 Plan",
		},
		{
			name: "page title split across runs",
			raw:  `{"object":"page","id":"pg","properties":{"Name":{"type":"title","title":[{"plain_text":"Weekly "},{"plain_text":"Sync"},{"plain_text":" — 2026"}]}}}`,
			want: "Weekly Sync — 2026",
		},
		{
			name: "no title fields",
			raw:  `{"object":"page","id":"pg"}`,
			want: "",
		},
		{
			name: "properties malformed",
			raw:  `{"object":"page","id":"pg","properties":{"Name":"oops"}}`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := seedResult(t, tt.raw)
			if got := extractSearchTitle(r); got != tt.want {
				t.Errorf("extractSearchTitle=%q want=%q", got, tt.want)
			}
		})
	}
}

// TestFormatSearchTime covers valid, invalid, and empty cases.
func TestFormatSearchTime(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"2026-04-22T10:30:00.000Z", "2026-04-22 10:30"},
		{"not-a-time", "not-a-time"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := formatSearchTime(tt.in); got != tt.want {
				t.Errorf("formatSearchTime(%q)=%q want=%q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEmitSearchTable exercises the table output helper directly.
func TestEmitSearchTable(t *testing.T) {
	results := []utils.SearchResult{
		seedResult(t, `{"object":"page","id":"pg","url":"https://notion.so/pg","last_edited_time":"2026-04-22T10:30:00.000Z","icon":{"type":"emoji","emoji":"📄"},"properties":{"Name":{"type":"title","title":[{"plain_text":"Title"}]}}}`),
		seedResult(t, `{"object":"database","id":"db","url":"https://notion.so/db","last_edited_time":"2026-04-22T10:30:00.000Z","title":[{"plain_text":"`+strings.Repeat("VeryLong", 20)+`"}]}`),
		// No icon, no title — exercises the fallbacks.
		seedResult(t, `{"object":"page","id":"plain","url":"https://notion.so/plain","last_edited_time":"2026-04-22T10:30:00.000Z"}`),
	}
	c := &cobra.Command{}
	var out bytes.Buffer
	c.SetOut(&out)
	if err := emitSearchTable(c, results); err != nil {
		t.Fatalf("emitSearchTable: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Title") {
		t.Errorf("table output missing title: %q", s)
	}
	if !strings.Contains(s, "(untitled)") {
		t.Errorf("table output missing (untitled) fallback: %q", s)
	}
	if !strings.Contains(s, "...") {
		t.Errorf("table output did not truncate long title: %q", s)
	}

	// Empty results hits the early-return path.
	out.Reset()
	if err := emitSearchTable(c, nil); err != nil {
		t.Fatalf("emitSearchTable empty: %v", err)
	}
}

// TestEmitSearchJSON directly exercises the NDJSON writer.
func TestEmitSearchJSON(t *testing.T) {
	results := []utils.SearchResult{
		seedResult(t, `{"object":"page","id":"pg-1","url":"u","last_edited_time":"2026-04-22T10:00:00.000Z"}`),
		seedResult(t, `{"object":"database","id":"db-1","url":"u","last_edited_time":"2026-04-22T10:00:00.000Z"}`),
	}
	c := &cobra.Command{}
	var out bytes.Buffer
	c.SetOut(&out)
	if err := emitSearchJSON(c, results); err != nil {
		t.Fatalf("emitSearchJSON: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), out.String())
	}
	for i, l := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(l), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v", i, err)
		}
	}

	// Zero-Raw result exercises the re-marshal fallback.
	out.Reset()
	synth := utils.SearchResult{ID: "synth", Object: "page", URL: "u"}
	if err := emitSearchJSON(c, []utils.SearchResult{synth}); err != nil {
		t.Fatalf("emitSearchJSON synth: %v", err)
	}
	if !strings.Contains(out.String(), `"id":"synth"`) {
		t.Errorf("re-marshal fallback did not include synthesized id: %q", out.String())
	}
}

// TestEmitSearchError verifies both the human and JSON branches write to
// cmd.ErrOrStderr(). The JSON branch must emit a parseable single-line
// error object so stdout stays valid NDJSON.
func TestEmitSearchError(t *testing.T) {
	// Restore package-level flag even if the test aborts mid-run. The
	// JSON toggle moved to the global --json plumbing so we touch
	// globalJSON here rather than a search-local flag.
	prev := globalJSON
	t.Cleanup(func() { globalJSON = prev })

	c := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)

	globalJSON = false
	emitSearchError(c, errorString("human"))
	if !strings.Contains(stderr.String(), "human") {
		t.Errorf("human branch did not write to stderr: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("human branch wrote to stdout: %q", stdout.String())
	}

	stderr.Reset()
	stdout.Reset()
	globalJSON = true
	emitSearchError(c, errorString("boom"))
	if stdout.Len() != 0 {
		t.Errorf("json branch wrote to stdout: %q", stdout.String())
	}
	var obj map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &obj); err != nil {
		t.Fatalf("json branch stderr not a valid JSON object: %v (%q)", err, stderr.String())
	}
	if obj["error"] != "boom" {
		t.Errorf("json error payload = %q, want %q", obj["error"], "boom")
	}
}

func TestResetSearchFlags(t *testing.T) {
	searchType = "pages"
	searchLimit = 42
	searchPageSize = 17
	globalJSON = true
	resetSearchFlags()
	if searchType != "" || searchLimit != 0 || searchPageSize != 0 || globalJSON {
		t.Errorf("flags not reset: type=%q limit=%d pageSize=%d json=%v",
			searchType, searchLimit, searchPageSize, globalJSON)
	}
}

func TestRunSearch(t *testing.T) {
	// Covered end-to-end by TestSearchCmdDispatch; this test ensures a
	// direct RunE invocation hits the happy path with zero args.
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"object": "list", "has_more": false, "results": []map[string]interface{}{}}
	})
	resetSearchFlags()
	c := &cobra.Command{}
	var out bytes.Buffer
	c.SetOut(&out)
	if err := runSearch(c, nil); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
	if atomic.LoadInt64(&stats.calls) != 1 {
		t.Errorf("want exactly 1 search call, got %d", stats.calls)
	}
}

// TestTruncateRunes asserts the rune-safe truncator preserves multi-byte
// characters and appends an ellipsis only when it actually cuts.
func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"shorter than max", "abc", 10, "abc"},
		{"exactly max ascii", "abcdefghij", 10, "abcdefghij"},
		{"truncate ascii", "abcdefghijk", 10, "abcdefg..."},
		{"truncate emoji mid-string preserves runes", "hello " + strings.Repeat("🙂", 20), 10, "hello 🙂..."},
		{"truncate CJK preserves runes", strings.Repeat("日本語", 20), 10, "日本語日本語日..."},
		{"max below 4 passes through", "abcdefghij", 3, "abcdefghij"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.in, tt.max)
			if got != tt.want {
				t.Errorf("truncateRunes(%q, %d)=%q want=%q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

// TestValidateSearchPageSize covers the client-side 1-100 bounds check.
func TestValidateSearchPageSize(t *testing.T) {
	tests := []struct {
		in      int
		wantErr bool
	}{
		{0, false},
		{1, false},
		{50, false},
		{100, false},
		{-1, true},
		{101, true},
		{99999, true},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			err := validateSearchPageSize(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSearchPageSize(%d) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// TestSearchCmdInvalidPageSize verifies the client-side bounds check rejects
// out-of-range --page-size values before issuing any HTTP request.
func TestSearchCmdInvalidPageSize(t *testing.T) {
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"object": "list", "has_more": false, "results": []map[string]interface{}{}}
	})

	resetSearchFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"search", "x", "--page-size", "500"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error on --page-size 500, got nil")
	}
	if atomic.LoadInt64(&stats.calls) != 0 {
		t.Errorf("search should not be issued on bad --page-size; got %d calls", stats.calls)
	}
}

// errorString is a tiny error that avoids pulling in fmt just for a literal.
type errorString string

func (e errorString) Error() string { return string(e) }
