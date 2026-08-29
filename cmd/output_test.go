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

// TestEmitList_CompactNDJSON verifies the default list path emits one
// compact JSON object per line (NDJSON).
func TestEmitList_CompactNDJSON(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	resetGlobalOutputFlags()
	var buf bytes.Buffer
	items := []utils.User{
		{ID: "u1", Name: "Ada"},
		{ID: "u2", Name: "Grace"},
	}
	if err := emitList(&buf, items); err != nil {
		t.Fatalf("emitList: %v", err)
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
		if strings.Contains(l, "  ") {
			t.Errorf("line %d unexpectedly indented: %q", i, l)
		}
	}
	// Non-slice input must error.
	if err := emitList(&buf, map[string]int{"a": 1}); err == nil {
		t.Error("emitList: want error on non-slice input")
	}
}

// TestEmitList_PrettyArray verifies --pretty switches the list path to
// a single indented JSON array (still valid JSON — this is the fix for
// the multi-line-NDJSON bug flagged in PR #28 review).
func TestEmitList_PrettyArray(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	resetGlobalOutputFlags()
	globalPretty = true
	var buf bytes.Buffer
	items := []utils.User{
		{ID: "u1", Name: "Ada"},
		{ID: "u2", Name: "Grace"},
	}
	if err := emitList(&buf, items); err != nil {
		t.Fatalf("emitList: %v", err)
	}
	// The whole output must be a single valid JSON array — not NDJSON.
	var arr []utils.User
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("pretty output is not a single JSON array: %v (%q)", err, buf.String())
	}
	if len(arr) != 2 {
		t.Fatalf("want 2 elements, got %d", len(arr))
	}
	// Must be indented — the indent marker appears inside the array.
	if !strings.Contains(buf.String(), "\n  ") {
		t.Errorf("pretty output missing indent:\n%s", buf.String())
	}
	// Must start with '[' and end with ']\n' so downstream parsers
	// treating it as a JSON document work.
	s := strings.TrimRight(buf.String(), "\n")
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		t.Errorf("pretty output is not a single array: %q", s)
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

// TestList_JSON_NoLocalTimezone confirms `list --json` does not fail
// on machines that ship without LOCAL_TIMEZONE set. JSON mode emits raw
// block objects with un-humanised timestamps, so the timezone lookup
// belongs strictly on the human path. Regression guard for PR #50
// second-pass review [P2].
func TestList_JSON_NoLocalTimezone(t *testing.T) {
	_ = withCmdEnv(t)
	// withCmdEnv pins LOCAL_TIMEZONE=UTC for human-mode determinism;
	// override it here so the assertion is meaningful — the bug only
	// reproduces when the env var is absent.
	t.Setenv("LOCAL_TIMEZONE", "")
	if err := os.Unsetenv("LOCAL_TIMEZONE"); err != nil {
		t.Fatalf("unset LOCAL_TIMEZONE: %v", err)
	}

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"list", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("list --json failed without LOCAL_TIMEZONE: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) == 0 {
		t.Fatalf("want at least 1 todo block, got 0: %q", out.String())
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

// TestTeams_List_JSON_StubError asserts `teams list --json` emits a
// JSON error envelope while the underlying API is stubbed (#37). The
// stderr stream should be a single valid JSON line whose error field
// mentions the unavailable API. When Notion restores /v1/teams, flip
// this back to NDJSON-of-team-objects.
func TestTeams_List_JSON_StubError(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	resetRootCmdArgs()

	// Need stderr captured separately — the JSON error envelope goes
	// to ErrOrStderr, not OutOrStdout (which would be the success
	// stream).
	var stderr bytes.Buffer
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(&stderr)
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.SetArgs([]string{"teams", "list", "--json"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("teams list --json should return an error while stubbed")
	}
	assertNoANSI(t, stderr.String())
	if !strings.Contains(stderr.String(), `"error"`) {
		t.Errorf("expected JSON error envelope on stderr, got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "teams API unavailable") {
		t.Errorf("expected stderr to mention API status, got: %q", stderr.String())
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

// TestDatabases_Query_JSON_Pretty verifies --pretty on a list command
// emits a single indented JSON array (the whole output parses as a
// single JSON document), not broken multi-line NDJSON.
func TestDatabases_Query_JSON_Pretty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object":   "list",
			"has_more": false,
			"results": []map[string]interface{}{
				{"object": "page", "id": "p1", "url": "u"},
				{"object": "page", "id": "p2", "url": "u"},
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
	// Whole output must parse as a single JSON array.
	var arr []map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &arr); err != nil {
		t.Fatalf("pretty list output is not a single JSON array: %v (%q)", err, out.String())
	}
	if len(arr) != 2 {
		t.Errorf("want 2 elements, got %d: %q", len(arr), out.String())
	}
	// Pretty-printed arrays put each element on its own line; an
	// element key sits at 4-space indent (2 for array nesting + 2 for
	// object member).
	if !strings.Contains(out.String(), "\n    \"") {
		t.Errorf("pretty output missing indented member:\n%s", out.String())
	}
}

// TestBlocks_List_JSON_Pretty asserts the same array-not-NDJSON shape
// on the blocks list command specifically (the one called out in the
// review).
func TestBlocks_List_JSON_Pretty(t *testing.T) {
	_ = withCmdEnv(t)
	resetGlobalOutputFlags()
	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "list", "--json", "--pretty"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNoANSI(t, out.String())
	// Must parse as a single JSON array (not NDJSON).
	var arr []map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &arr); err != nil {
		t.Fatalf("blocks list --pretty is not a JSON array: %v (%q)", err, out.String())
	}
	if len(arr) == 0 {
		t.Fatalf("no elements in pretty array: %q", out.String())
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
// the existing file-upload mock (two-step in cmdMockServer) and asserts
// the envelope shape: {"ok":true, "action":"add-file", "id":"...",
// "name":"<basename>", "ref":{...}}.
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
		t.Fatalf("want 1 envelope, got %d: %q", len(objs), out.String())
	}
	env := objs[0]
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	if env["action"] != "add-file" {
		t.Errorf("action = %v, want add-file", env["action"])
	}
	if id, _ := env["id"].(string); id == "" {
		t.Errorf("envelope missing id: %v", env)
	}
	// Without --name, name falls back to the upload ref's name.
	if name, _ := env["name"].(string); name == "" {
		t.Errorf("envelope missing name: %v", env)
	}
	if _, ok := env["ref"].(map[string]interface{}); !ok {
		t.Errorf("envelope missing ref object: %v", env)
	}
}

// TestFiles_AddFile_JSON_NameOverride verifies --name is surfaced in
// the JSON envelope so callers can correlate the name they asked for
// with the upload result. Addresses PR #28 reviewer's ask that --name
// be visible in JSON output.
func TestFiles_AddFile_JSON_NameOverride(t *testing.T) {
	_ = withCmdEnv(t)
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
	rootCmd.SetArgs([]string{"blocks", "add-file", tmp, "--name", "display-name.txt", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	objs := assertNDJSON(t, out.String())
	if len(objs) != 1 {
		t.Fatalf("want 1 envelope, got %d: %q", len(objs), out.String())
	}
	if got := objs[0]["name"]; got != "display-name.txt" {
		t.Errorf("name = %v, want display-name.txt (envelope=%v)", got, objs[0])
	}
}

// writeTestFile is a small helper so output_test.go stays self-contained.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// TestEmitList_NilSliceIsEmptyArray guards issue #72. A paginator that
// returns zero results returns a nil slice, and encoding/json serializes
// a nil slice as `null`, not `[]`. `databases query <empty> --json
// --pretty` therefore emitted `null`, which every JSON array consumer
// rejects. emitList normalises at the single choke point every list
// command already funnels through.
func TestEmitList_NilSliceIsEmptyArray(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)

	tests := []struct {
		name   string
		items  interface{}
		pretty bool
		want   string
	}{
		{name: "nil typed slice pretty", items: []utils.Block(nil), pretty: true, want: "[]\n"},
		{name: "nil map slice pretty", items: []map[string]string(nil), pretty: true, want: "[]\n"},
		{name: "empty non-nil slice pretty", items: []utils.Block{}, pretty: true, want: "[]\n"},
		// NDJSON mode emits nothing for an empty set, which is already
		// the correct shape for a line-delimited stream.
		{name: "nil typed slice ndjson", items: []utils.Block(nil), pretty: false, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalOutputFlags()
			globalPretty = tt.pretty
			var buf bytes.Buffer
			if err := emitList(&buf, tt.items); err != nil {
				t.Fatalf("emitList: %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("emitList = %q, want %q", got, tt.want)
			}
			if strings.Contains(buf.String(), "null") {
				t.Errorf("emitList emitted null for an empty result set: %q", buf.String())
			}
		})
	}
}

// TestEmitRaw covers the loss-free output helper added for #80/#86: raw
// bytes pass through verbatim in compact mode and are re-indented under
// --pretty, always with exactly one trailing newline.
func TestEmitRaw(t *testing.T) {
	t.Cleanup(resetGlobalOutputFlags)
	raw := json.RawMessage(`{"a":1,"b":{"c":2}}`)

	resetGlobalOutputFlags()
	var compact bytes.Buffer
	if err := emitRaw(&compact, raw); err != nil {
		t.Fatalf("emitRaw: %v", err)
	}
	if compact.String() != `{"a":1,"b":{"c":2}}`+"\n" {
		t.Errorf("emitRaw compact = %q", compact.String())
	}

	resetGlobalOutputFlags()
	globalPretty = true
	var pretty bytes.Buffer
	if err := emitRaw(&pretty, raw); err != nil {
		t.Fatalf("emitRaw pretty: %v", err)
	}
	if !strings.Contains(pretty.String(), "\n  \"a\": 1") {
		t.Errorf("emitRaw pretty did not indent: %q", pretty.String())
	}
	var round map[string]interface{}
	if err := json.Unmarshal(pretty.Bytes(), &round); err != nil {
		t.Errorf("emitRaw pretty output is not valid JSON: %v", err)
	}
}
