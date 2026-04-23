package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// findCommentsSubcommand mirrors findBlocksSubcommand: walks commentsCmd's
// children and returns the one matching name, or fails.
func findCommentsSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	parent := findTopLevelCmd(t, "comments")
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("comments subcommand %q not found", name)
	return nil
}

// TestCommentsCmdRegistered verifies the `comments` command is registered
// on rootCmd and owns its two subcommands.
func TestCommentsCmdRegistered(t *testing.T) {
	c := findTopLevelCmd(t, "comments")
	want := map[string]bool{"list": false, "create": false}
	for _, sub := range c.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for n, seen := range want {
		if !seen {
			t.Errorf("comments subcommand %q not registered", n)
		}
	}
}

// TestCommentsListFlags ensures list declares --json (default false) and
// requires exactly one positional arg.
func TestCommentsListFlags(t *testing.T) {
	list := findCommentsSubcommand(t, "list")

	jf := list.Flag("json")
	if jf == nil {
		t.Fatal("comments list: --json flag not registered")
	}
	if jf.DefValue != "false" {
		t.Errorf("--json default = %q, want false", jf.DefValue)
	}
	if err := list.Args(list, []string{}); err == nil {
		t.Error("comments list: expected error with zero args")
	}
	if err := list.Args(list, []string{"id"}); err != nil {
		t.Errorf("comments list: one arg should be valid, got %v", err)
	}
	if err := list.Args(list, []string{"a", "b"}); err == nil {
		t.Error("comments list: expected error with two args")
	}
}

// TestCommentsCreateFlags ensures create declares --text, --discussion-id,
// --json and that positional arg count is exactly one.
func TestCommentsCreateFlags(t *testing.T) {
	create := findCommentsSubcommand(t, "create")

	for _, name := range []string{"text", "discussion-id", "json"} {
		if create.Flag(name) == nil {
			t.Errorf("comments create: --%s flag not registered", name)
		}
	}
	if v := create.Flag("text").DefValue; v != "" {
		t.Errorf("--text default = %q, want empty", v)
	}
	if err := create.Args(create, []string{}); err == nil {
		t.Error("comments create: expected error with zero args")
	}
	if err := create.Args(create, []string{"id"}); err != nil {
		t.Errorf("comments create: one arg should be valid, got %v", err)
	}
}

// commentsWithCmdEnv is a comments-specific variant of withCmdEnv that
// wires a mock server answering /comments GET+POST, because the shared
// cmdMockServer in testhelpers only knows about /blocks.
//
// Returns the server so tests can inspect call counts.
func commentsWithCmdEnv(t *testing.T) *httptest.Server {
	t.Helper()

	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/comments":
			writeJSON(w, utils.CommentList{
				Object: "list",
				Results: []utils.Comment{
					{
						Object:      "comment",
						ID:          "c-1",
						CreatedTime: "2026-04-22T10:00:00.000Z",
						CreatedBy:   utils.CommentUser{Object: "user", ID: "u-1"},
						RichText:    []utils.RichText{{PlainText: "hello"}},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/comments":
			writeJSON(w, utils.Comment{
				Object:      "comment",
				ID:          "c-new",
				CreatedTime: "2026-04-22T10:05:00.000Z",
				CreatedBy:   utils.CommentUser{Object: "user", ID: "u-1"},
				RichText:    []utils.RichText{{PlainText: "ack"}},
			})
		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
	priorBaseURL := utils.GetBaseURL()
	utils.SetBaseURL(srv.URL)
	t.Cleanup(func() {
		utils.SetBaseURL(priorBaseURL)
		srv.Close()
	})

	// Isolated cwd + env, mirrors testhelpers_test.withCmdEnv.
	emptyCwd := t.TempDir()
	emptyHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyCwd, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv("HOME", emptyHome)
	t.Setenv("NOTION_API_KEY", "test-key")
	t.Setenv("NOTION_PAGE_ID", "pageID")
	t.Setenv("LOCAL_TIMEZONE", "UTC")

	return srv
}

// TestCommentsListDispatch runs `notioncli comments list <id>` and asserts
// the GET /comments call happened with the right block_id.
func TestCommentsListDispatch(t *testing.T) {
	srv := commentsWithCmdEnv(t)

	var listed int64
	var gotBlockID string
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/comments" {
			atomic.AddInt64(&listed, 1)
			gotBlockID = r.URL.Query().Get("block_id")
		}
		origHandler.ServeHTTP(w, r)
	})

	// Reset shared flag state in case a previous test toggled it.
	commentsListJSON = false

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"comments", "list", "block-xyz"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(comments list): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Fatal("comments list did not call GET /comments")
	}
	if gotBlockID != "block-xyz" {
		t.Errorf("block_id query = %q, want %q", gotBlockID, "block-xyz")
	}
}

// TestCommentsCreateDispatch runs `notioncli comments create <id> --text ...`
// and asserts the POST body contains the expected parent.block_id.
func TestCommentsCreateDispatch(t *testing.T) {
	srv := commentsWithCmdEnv(t)

	var posted int64
	var body string
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/comments" {
			atomic.AddInt64(&posted, 1)
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			// Re-plumb the body for the downstream handler (not strictly
			// required because origHandler ignores the request body, but
			// keeps this robust to future handler changes).
			r.Body = io.NopCloser(strings.NewReader(body))
		}
		origHandler.ServeHTTP(w, r)
	})

	commentsCreateText = ""
	commentsCreateDiscID = ""
	commentsCreateJSON = false

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"comments", "create", "block-xyz", "--text", "hello"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(comments create): %v", err)
	}

	if atomic.LoadInt64(&posted) == 0 {
		t.Fatal("comments create did not POST /comments")
	}
	if !strings.Contains(body, `"block_id":"block-xyz"`) {
		t.Errorf("POST body missing expected parent.block_id: %s", body)
	}
	if !strings.Contains(body, `"rich_text"`) {
		t.Errorf("POST body missing rich_text: %s", body)
	}
}

// TestCommentsCreateReplyDispatch verifies --discussion-id routes to the
// discussion branch (no parent in body).
func TestCommentsCreateReplyDispatch(t *testing.T) {
	srv := commentsWithCmdEnv(t)

	var body string
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/comments" {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			r.Body = io.NopCloser(strings.NewReader(body))
		}
		origHandler.ServeHTTP(w, r)
	})

	commentsCreateText = ""
	commentsCreateDiscID = ""
	commentsCreateJSON = false

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"comments", "create", "-", "--text", "reply", "--discussion-id", "disc-1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(comments create reply): %v", err)
	}

	if !strings.Contains(body, `"discussion_id":"disc-1"`) {
		t.Errorf("POST body missing discussion_id: %s", body)
	}
	if strings.Contains(body, `"parent"`) {
		t.Errorf("reply POST body should not include parent: %s", body)
	}
}

// TestCommentsCreateMissingText verifies the --text guard fires client-side
// without hitting the network.
func TestCommentsCreateMissingText(t *testing.T) {
	srv := commentsWithCmdEnv(t)

	var posted int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt64(&posted, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	commentsCreateText = ""
	commentsCreateDiscID = ""

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"comments", "create", "block-xyz"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}
	if atomic.LoadInt64(&posted) != 0 {
		t.Error("missing --text should short-circuit before POST")
	}
}

// TestFormatComment locks in the human-output shape (truncation and
// empty-body fallback) so that string formatting changes are deliberate.
func TestFormatComment(t *testing.T) {
	long := strings.Repeat("a", 200)
	c := utils.Comment{
		ID:          "c-1",
		CreatedTime: "2026-04-22T10:00:00.000Z",
		CreatedBy:   utils.CommentUser{ID: "u-1"},
		RichText:    []utils.RichText{{PlainText: long}},
	}
	line := formatComment(c)
	if !strings.Contains(line, "u-1") {
		t.Errorf("expected author in line: %s", line)
	}
	if !strings.Contains(line, "c-1") {
		t.Errorf("expected id in line: %s", line)
	}
	if !strings.Contains(line, "...") {
		t.Errorf("expected truncation marker in line: %s", line)
	}

	empty := utils.Comment{ID: "c-2"}
	if got := formatComment(empty); !strings.Contains(got, "(empty)") {
		t.Errorf("expected (empty) fallback, got: %s", got)
	}
	if got := formatComment(empty); !strings.Contains(got, "(unknown)") {
		t.Errorf("expected (unknown) author fallback, got: %s", got)
	}
}

// TestCommentPlainText exercises the richtext collapse helper directly,
// including the empty-slice and empty-string fallback branches.
func TestCommentPlainText(t *testing.T) {
	tests := []struct {
		name string
		in   utils.Comment
		want string
	}{
		{"nil richtext", utils.Comment{}, "(empty)"},
		{"all empty", utils.Comment{RichText: []utils.RichText{{PlainText: ""}, {PlainText: ""}}}, "(empty)"},
		{"concat", utils.Comment{RichText: []utils.RichText{{PlainText: "hi "}, {PlainText: "there"}}}, "hi there"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commentPlainText(tt.in); got != tt.want {
				t.Errorf("commentPlainText = %q, want %q", got, tt.want)
			}
		})
	}
}
