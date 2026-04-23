// Tests for the cross-command --json output plumbing (cmd/output.go,
// cmd/root.go persistent flags, per-command JSON branches).
//
// Each test uses a _JSON suffix to avoid name collisions with the
// existing dispatch/metadata tests. These tests do NOT call t.Parallel
// — they share the rootCmd singleton and package-level flag vars with
// the rest of the cmd package.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// ansiEscape matches any ANSI CSI sequence. Tests use this to assert
// JSON output is free of terminal escape codes so jq / downstream
// parsers never see colour noise bleed into stdout.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// assertNoANSI fails the test if s contains an ANSI escape. Called on
// JSON output paths only; human paths deliberately include colour.
func assertNoANSI(t *testing.T, s string) {
	t.Helper()
	if m := ansiEscape.FindString(s); m != "" {
		t.Errorf("output contains ANSI escape %q:\n%s", m, s)
	}
}

// assertNDJSON parses every non-empty line of s as a JSON object and
// returns the decoded objects. Fails the test on any parse error.
func assertNDJSON(t *testing.T, s string) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for i, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "{") {
			// Skip lines that aren't JSON objects (e.g. if a test
			// accidentally captures a banner) but surface them for
			// debugging.
			t.Logf("skipping non-JSON line %d: %q", i, line)
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v (%q)", i, err, line)
			continue
		}
		out = append(out, obj)
	}
	return out
}

// TestRootPersistentFlags verifies the persistent --json, --pretty, and
// --output flags are wired on rootCmd and resolvable via any subcommand.
func TestRootPersistentFlags(t *testing.T) {
	for _, name := range []string{"json", "pretty", "output"} {
		f := rootCmd.PersistentFlags().Lookup(name)
		if f == nil {
			t.Fatalf("rootCmd.PersistentFlags missing --%s", name)
		}
	}
	// Subcommand inheritance: users list must resolve --json from the
	// root's persistent flag set (it was migrated from a local flag).
	usersList := findUsersSubcommand(t, "list")
	if f := usersList.Flag("json"); f == nil {
		t.Error("users list: persistent --json not inherited")
	}
}

// TestApplyGlobalOutput covers the --output=text|json translation.
func TestApplyGlobalOutput(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	tests := []struct {
		in       string
		wantJSON bool
		wantErr  bool
	}{
		{"", false, false},
		{"text", false, false},
		{"json", true, false},
		{"yaml", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			resetGlobalOutputFlags()
			globalOutput = tt.in
			err := applyGlobalOutput()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && globalJSON != tt.wantJSON {
				t.Errorf("globalJSON=%v want %v", globalJSON, tt.wantJSON)
			}
		})
	}
}

// TestEmitJSON_Compact verifies compact output uses a single line.
func TestEmitJSON_Compact(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	resetGlobalOutputFlags()
	var buf bytes.Buffer
	if err := emitJSON(&buf, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	if got := buf.String(); got != "{\"a\":\"b\"}\n" {
		t.Errorf("compact emitJSON = %q, want {\"a\":\"b\"}\\n", got)
	}
}

// TestEmitJSON_Pretty verifies --pretty produces indented output.
func TestEmitJSON_Pretty(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	resetGlobalOutputFlags()
	globalPretty = true
	var buf bytes.Buffer
	if err := emitJSON(&buf, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\n  \"a\"") {
		t.Errorf("pretty emitJSON missing indent: %q", got)
	}
}

// TestEmitJSONLines writes NDJSON from a typed slice.
func TestEmitJSONLines(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	resetGlobalOutputFlags()
	var buf bytes.Buffer
	items := []utils.User{
		{ID: "u1", Name: "Ada"},
		{ID: "u2", Name: "Grace"},
	}
	if err := emitJSONLines(&buf, items); err != nil {
		t.Fatalf("emitJSONLines: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	for i, l := range lines {
		var u utils.User
		if err := json.Unmarshal([]byte(l), &u); err != nil {
			t.Errorf("line %d: %v", i, err)
		}
	}
	// Non-slice input must error.
	if err := emitJSONLines(&buf, map[string]int{"a": 1}); err == nil {
		t.Error("emitJSONLines: want error on non-slice input")
	}
}

// TestEmitError writes a single-line JSON error object.
func TestEmitError(t *testing.T) {
	var buf bytes.Buffer
	emitError(&buf, errorString("boom"))
	s := strings.TrimSpace(buf.String())
	var obj map[string]string
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("emitError output is not valid JSON: %v (%q)", err, s)
	}
	if obj["error"] != "boom" {
		t.Errorf("emitError payload = %q, want boom", obj["error"])
	}
	// nil error is a no-op.
	buf.Reset()
	emitError(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("emitError(nil) wrote %q, want empty", buf.String())
	}
}

// TestJSONErrorOr verifies the error path writes to stderr only when
// globalJSON is set, and always returns the original error.
func TestJSONErrorOr(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)

	c := &cobra.Command{}
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)

	resetGlobalOutputFlags()
	globalJSON = false
	if err := jsonErrorOr(c, errorString("oops")); err == nil || err.Error() != "oops" {
		t.Errorf("human path err=%v want oops", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("human path wrote to stderr: %q", stderr.String())
	}

	stderr.Reset()
	globalJSON = true
	if err := jsonErrorOr(c, errorString("boom")); err == nil {
		t.Fatal("json path expected err, got nil")
	}
	var obj map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &obj); err != nil {
		t.Fatalf("json path stderr not a JSON object: %v (%q)", err, stderr.String())
	}
	if obj["error"] != "boom" {
		t.Errorf("json path payload = %q want boom", obj["error"])
	}

	// Nil error is always a no-op.
	stderr.Reset()
	if err := jsonErrorOr(c, nil); err != nil {
		t.Errorf("jsonErrorOr(nil) returned %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("jsonErrorOr(nil) wrote %q", stderr.String())
	}
}

// TestPersistentPreRunE_DisablesColor verifies that running any
// subcommand with --json flips color.NoColor to true (suppressing ANSI
// escapes from fatih/color calls in downstream commands).
func TestPersistentPreRunE_DisablesColor(t *testing.T) {
	prev := color.NoColor
	t.Cleanup(func() { color.NoColor = prev })

	// Use a trivial subcommand to avoid hitting the HTTP mock plumbing.
	c := findTopLevelCmd(t, "search")
	_ = c // silence lint — we only need rootCmd to dispatch

	resetGlobalOutputFlags()
	resetRootCmdArgs()

	// Route via a tiny subcommand we register just for this test so we
	// don't depend on the search command's happy path.
	probeRan := false
	probe := &cobra.Command{
		Use: "jsonprobe",
		RunE: func(cmd *cobra.Command, args []string) error {
			probeRan = true
			return nil
		},
	}
	rootCmd.AddCommand(probe)
	t.Cleanup(func() { rootCmd.RemoveCommand(probe) })

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	color.NoColor = false
	rootCmd.SetArgs([]string{"jsonprobe", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !probeRan {
		t.Fatal("probe did not run")
	}
	if !color.NoColor {
		t.Error("--json did not disable color")
	}
}

// TestList_JSON runs `list --json` against the standard cmd mock and
// asserts stdout is NDJSON, free of ANSI escapes.
func TestList_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"list", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 todo block, got %d: %q", len(objs), out.String())
	}
	if got := objs[0]["type"]; got != "to_do" {
		t.Errorf("block type = %v, want to_do", got)
	}
}

// TestAdd_JSON runs `add "task" --json` and asserts the ok envelope.
func TestAdd_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"add", "buy milk", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 ok envelope, got %d: %q", len(objs), out.String())
	}
	if objs[0]["ok"] != true {
		t.Errorf("ok = %v, want true", objs[0]["ok"])
	}
	if objs[0]["text"] != "buy milk" {
		t.Errorf("text = %v, want buy milk", objs[0]["text"])
	}
}

// TestCheck_JSON, TestUncheck_JSON, TestDelete_JSON cover the top-level
// to-do commands. Each emits {"ok":true,"action":<verb>,"order":N}.
func TestCheck_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"check", "1", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 || objs[0]["action"] != "check" {
		t.Errorf("unexpected check envelope: %v", objs)
	}
}

func TestUncheck_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"uncheck", "1", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 || objs[0]["action"] != "uncheck" {
		t.Errorf("unexpected uncheck envelope: %v", objs)
	}
}

func TestDelete_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"delete", "1", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 || objs[0]["action"] != "delete" {
		t.Errorf("unexpected delete envelope: %v", objs)
	}
}

// TestBlocks_List_JSON asserts the blocks list command emits NDJSON.
func TestBlocks_List_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	// Reset the sticky blockType flag so a prior test cannot leak state.
	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "list", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) == 0 {
		t.Fatalf("no NDJSON lines: %q", out.String())
	}
}

// TestBlocks_Add_JSON asserts the blocks add command emits an ok envelope.
func TestBlocks_Add_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "hello", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 || objs[0]["action"] != "add" {
		t.Errorf("unexpected blocks add envelope: %v", objs)
	}
}

// TestBlocks_Delete_JSON asserts blocks delete emits ok envelope.
func TestBlocks_Delete_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "delete", "1", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 || objs[0]["action"] != "delete" {
		t.Errorf("unexpected blocks delete envelope: %v", objs)
	}
}

// TestBlocks_Add_JSON_InvalidType asserts the error path: a bad --type
// writes a JSON error object to stderr AND the command returns a non-nil
// error so cobra sets a non-zero exit code.
func TestBlocks_Add_JSON_InvalidType(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	blockType = ""
	resetRootCmdArgs()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"blocks", "add", "hello", "-t", "bogus", "--json"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error on invalid --type")
	}
	// Error envelope on stderr.
	var obj map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &obj); err != nil {
		t.Fatalf("stderr not a JSON error: %v (%q)", err, stderr.String())
	}
	if obj["error"] == "" {
		t.Error("error payload empty")
	}
}

// TestSearch_JSON verifies the search command still honors --json via
// the global persistent flag (previously search owned a local --json).
func TestSearch_JSON(t *testing.T) {
	var stats searchHandlerStats
	overlaySearchHandler(t, &stats, func(req map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"object":   "list",
			"has_more": false,
			"results": []map[string]interface{}{
				{"object": "page", "id": "pg-a", "url": "u", "last_edited_time": "2026-04-22T10:00:00.000Z"},
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
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Errorf("want 1 result, got %d: %q", len(objs), out.String())
	}
}

// TestComments_List_JSON drives `comments list` with --json.
func TestComments_List_JSON(t *testing.T) {
	srv := commentsWithCmdEnv(t)
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origHandler.ServeHTTP(w, r)
	})

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"comments", "list", "block-xyz", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) == 0 {
		t.Fatalf("no NDJSON lines: %q", out.String())
	}
}

// TestComments_Create_JSON drives `comments create` with --json.
func TestComments_Create_JSON(t *testing.T) {
	_ = commentsWithCmdEnv(t)
	resetGlobalOutputFlags()
	commentsCreateText = ""
	commentsCreateDiscID = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"comments", "create", "block-xyz", "--text", "hi", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Errorf("want 1 comment, got %d: %q", len(objs), out.String())
	}
}

// TestUsers_List_JSON / TestUsers_Get_JSON / TestUsers_Whoami_JSON —
// smoke tests for the three users subcommands under --json.
func TestUsers_List_JSON(t *testing.T) {
	_ = withUsersEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "list", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) == 0 {
		t.Fatalf("no user NDJSON: %q", out.String())
	}
}

func TestUsers_Get_JSON(t *testing.T) {
	_ = withUsersEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "get", "u1", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 user, got %d", len(objs))
	}
}

func TestUsers_Whoami_JSON(t *testing.T) {
	_ = withUsersEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "whoami", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 user, got %d", len(objs))
	}
}

// TestTeams_List_JSON asserts `teams list --json` emits NDJSON.
func TestTeams_List_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"teams", "list", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) == 0 {
		t.Fatalf("no team NDJSON: %q", out.String())
	}
}

// TestPages_Get_JSON drives `pages get <id> --json`.
func TestPages_Get_JSON(t *testing.T) {
	_ = withPagesEnv(t)
	resetGlobalOutputFlags()
	resetPagesFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "get", "pageID", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 page, got %d", len(objs))
	}
}

// TestPages_Archive_JSON asserts archive emits the ok envelope.
func TestPages_Archive_JSON(t *testing.T) {
	_ = withPagesEnv(t)
	resetGlobalOutputFlags()
	resetPagesFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "archive", "pageID", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 || objs[0]["action"] != "archive" {
		t.Errorf("unexpected archive envelope: %v", objs)
	}
}

// TestDatabases_Query_JSON drives `databases query <id> --json` and
// asserts an NDJSON per page row.
func TestDatabases_Query_JSON(t *testing.T) {
	// Build a tiny query server. The existing cmdMockServer does not
	// know about /databases/{id}/query so we spin up our own here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object":   "list",
				"has_more": false,
				"results": []map[string]interface{}{
					{"object": "page", "id": "p1", "url": "u"},
					{"object": "page", "id": "p2", "url": "u"},
				},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	_ = withCmdEnv(t)
	utils.SetBaseURL(srv.URL)
	resetGlobalOutputFlags()
	resetDatabasesFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"databases", "query", "dbID", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 2 {
		t.Fatalf("want 2 results, got %d: %q", len(objs), out.String())
	}
}

// TestDatabases_Query_JSONPretty verifies --pretty indents output.
// We can't require NDJSON in pretty mode (each element spans multiple
// lines), so we just assert the indent is present.
func TestDatabases_Query_JSONPretty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object":   "list",
			"has_more": false,
			"results": []map[string]interface{}{
				{"object": "page", "id": "p1", "url": "u"},
			},
		})
	}))
	defer srv.Close()

	_ = withCmdEnv(t)
	utils.SetBaseURL(srv.URL)
	resetGlobalOutputFlags()
	resetDatabasesFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"databases", "query", "dbID", "--json", "--pretty"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	if !strings.Contains(out.String(), "\n  \"") {
		t.Errorf("pretty output missing indent:\n%s", out.String())
	}
}

// TestViews_Create_JSON drives `views create` with --json against the
// existing cmd mock (which stubs POST /data_sources/{id}/views).
func TestViews_Create_JSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	// Reset views flag vars between runs.
	viewsCreateName = ""
	viewsCreateType = ""
	viewsCreateConfigFile = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n", "--type", "table", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 view, got %d: %q", len(objs), out.String())
	}
}

// TestFiles_AddFile_JSON drives `blocks add-file <path> --json` against
// the existing file-upload mock (two-step in cmdMockServer).
func TestFiles_AddFile_JSON(t *testing.T) {
	srv := withCmdEnv(t)
	_ = srv
	// Write a temp file so NewFileRef can open it.
	tmp := t.TempDir() + "/hello.txt"
	if err := writeTestFile(tmp, "hello"); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}
	resetGlobalOutputFlags()
	blocksAddFileName = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add-file", tmp, "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 fileref, got %d: %q", len(objs), out.String())
	}
	if id, _ := objs[0]["id"].(string); id == "" {
		t.Errorf("file ref missing id: %v", objs[0])
	}
}

// writeTestFile is a small helper so output_test.go stays self-contained.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
