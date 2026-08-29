package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// findBlocksSubcommand walks the blocks command's children and returns the
// child matching name, or fails the test.
func findBlocksSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	blocksC := findTopLevelCmd(t, "blocks")
	for _, c := range blocksC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("blocks subcommand %q not found", name)
	return nil
}

// TestBlocksCmdRegistered verifies `blocks` is a top-level command and
// owns its three subcommands (list, add, delete).
func TestBlocksCmdRegistered(t *testing.T) {
	blocksC := findTopLevelCmd(t, "blocks")

	want := map[string]bool{"list": false, "add": false, "delete": false}
	for _, c := range blocksC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("blocks subcommand %q not registered", name)
		}
	}
}

// TestBlocksListFlags asserts the list subcommand declares a --type/-t flag
// whose default is the empty string (no filter).
func TestBlocksListFlags(t *testing.T) {
	list := findBlocksSubcommand(t, "list")

	long := list.Flag("type")
	if long == nil {
		t.Fatal("blocks list: --type flag not registered")
	}
	if long.Shorthand != "t" {
		t.Errorf("blocks list: --type shorthand = %q, want %q", long.Shorthand, "t")
	}
	if long.DefValue != "" {
		t.Errorf("blocks list: --type default = %q, want empty", long.DefValue)
	}
	if long.Value.Type() != "string" {
		t.Errorf("blocks list: --type type = %q, want string", long.Value.Type())
	}
}

// TestBlocksAddFlags asserts the add subcommand declares --type/-t with
// default "paragraph" and that it takes at least one positional arg.
func TestBlocksAddFlags(t *testing.T) {
	add := findBlocksSubcommand(t, "add")

	long := add.Flag("type")
	if long == nil {
		t.Fatal("blocks add: --type flag not registered")
	}
	if long.Shorthand != "t" {
		t.Errorf("blocks add: --type shorthand = %q, want %q", long.Shorthand, "t")
	}
	if long.DefValue != "paragraph" {
		t.Errorf("blocks add: --type default = %q, want %q", long.DefValue, "paragraph")
	}

	// MinimumNArgs(1) means zero args must error.
	if err := add.Args(add, []string{}); err == nil {
		t.Error("blocks add: expected error on zero args, got nil")
	}
	if err := add.Args(add, []string{"hello"}); err != nil {
		t.Errorf("blocks add: expected no error on one arg, got %v", err)
	}
}

// TestBlocksDeleteArgs asserts the delete subcommand requires exactly one
// positional arg.
func TestBlocksDeleteArgs(t *testing.T) {
	del := findBlocksSubcommand(t, "delete")

	if err := del.Args(del, []string{}); err == nil {
		t.Error("blocks delete: expected error on zero args, got nil")
	}
	if err := del.Args(del, []string{"1"}); err != nil {
		t.Errorf("blocks delete: expected no error on one arg, got %v", err)
	}
	if err := del.Args(del, []string{"1", "2"}); err == nil {
		t.Error("blocks delete: expected error on two args, got nil")
	}
}

// TestBlocksListDispatch runs `notioncli blocks list` and confirms the
// listing call hit the mock server.
func TestBlocksListDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var listed int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&listed, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	// Reset any --type filter that a previous test may have set. blockType
	// is a package-level variable; a stale "to_do" here would narrow results.
	blockType = ""

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks list): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("blocks list did not hit /blocks/pageID/children")
	}
}

// TestBlocksAddDispatch runs `notioncli blocks add "hi" -t paragraph` and
// asserts the add PATCH was issued.
func TestBlocksAddDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	// Reset block type so the add command falls back to paragraph.
	blockType = ""

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "hello world", "-t", "paragraph"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks add): %v", err)
	}

	if atomic.LoadInt64(&patched) == 0 {
		t.Error("blocks add did not PATCH /blocks/pageID/children")
	}
}

// TestBlocksAdd_ExtendedFlags verifies the extended --url / --caption /
// --file-upload-id / --language flags are registered and that --url defaults
// to empty (the positional text remains the default URL source). --language
// defaults to "plain text" so Cobra's help output matches runtime behaviour.
func TestBlocksAdd_ExtendedFlags(t *testing.T) {
	add := findBlocksSubcommand(t, "add")

	for _, tc := range []struct {
		flag string
		want string
	}{
		{"url", ""},
		{"caption", ""},
		{"file-upload-id", ""},
		{"language", "plain text"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			f := add.Flag(tc.flag)
			if f == nil {
				t.Fatalf("blocks add: --%s flag not registered", tc.flag)
			}
			if f.DefValue != tc.want {
				t.Errorf("blocks add: --%s default = %q, want %q", tc.flag, f.DefValue, tc.want)
			}
			if f.Value.Type() != "string" {
				t.Errorf("blocks add: --%s type = %q, want string", tc.flag, f.Value.Type())
			}
		})
	}
}

// resetBlocksAddFlags clears every package-level flag variable the add
// command binds to. Cobra keeps these alive across rootCmd.Execute calls, so
// tests that flip --url or --file-upload-id would leak those settings into
// later tests without this reset. blockLanguage resets to the Cobra default
// ("plain text") so code-block tests see the same value the CLI user sees.
func resetBlocksAddFlags() {
	blockType = ""
	blockURL = ""
	blockCaption = ""
	blockFileID = ""
	blockLanguage = "plain text"
}

// TestBlocksAddDispatch_Image runs `blocks add https://... -t image` and
// asserts the wire-level JSON has an image.external.url payload.
func TestBlocksAddDispatch_Image(t *testing.T) {
	srv := withCmdEnv(t)

	var captured []byte
	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			body, _ := io.ReadAll(r.Body)
			captured = body
			atomic.AddInt64(&patched, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		origHandler.ServeHTTP(w, r)
	})

	resetBlocksAddFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "https://example.com/p.png", "-t", "image"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks add image): %v", err)
	}

	if atomic.LoadInt64(&patched) == 0 {
		t.Fatal("blocks add image did not PATCH children endpoint")
	}
	var parsed struct {
		Children []struct {
			Type  string `json:"type"`
			Image struct {
				Type     string `json:"type"`
				External struct {
					URL string `json:"url"`
				} `json:"external"`
			} `json:"image"`
		} `json:"children"`
	}
	if err := json.Unmarshal(captured, &parsed); err != nil {
		t.Fatalf("unmarshal captured: %v", err)
	}
	if len(parsed.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(parsed.Children))
	}
	child := parsed.Children[0]
	if child.Type != "image" || child.Image.Type != "external" || child.Image.External.URL != "https://example.com/p.png" {
		t.Errorf("unexpected child payload: %+v", child)
	}
}

// TestBlocksAddDispatch_Equation exercises the equation path through the
// cmd layer and asserts the expression lands in the PATCH body.
func TestBlocksAddDispatch_Equation(t *testing.T) {
	srv := withCmdEnv(t)

	var captured []byte
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			captured, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		origHandler.ServeHTTP(w, r)
	})

	resetBlocksAddFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "E=mc^2", "-t", "equation"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks add equation): %v", err)
	}

	var parsed struct {
		Children []struct {
			Equation struct {
				Expression string `json:"expression"`
			} `json:"equation"`
		} `json:"children"`
	}
	if err := json.Unmarshal(captured, &parsed); err != nil {
		t.Fatalf("unmarshal captured: %v", err)
	}
	if len(parsed.Children) != 1 || parsed.Children[0].Equation.Expression != "E=mc^2" {
		t.Errorf("expression not propagated: %s", captured)
	}
}

// TestBlocksAddDispatch_BookmarkWithURLFlag confirms --url overrides the
// positional text as the URL source for bookmark blocks.
func TestBlocksAddDispatch_BookmarkWithURLFlag(t *testing.T) {
	srv := withCmdEnv(t)

	var captured []byte
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			captured, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		origHandler.ServeHTTP(w, r)
	})

	resetBlocksAddFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{
		"blocks", "add", "home page", "-t", "bookmark",
		"--url", "https://example.com",
		"--caption", "the site",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks add bookmark): %v", err)
	}

	var parsed struct {
		Children []struct {
			Bookmark struct {
				URL     string `json:"url"`
				Caption []struct {
					Text struct {
						Content string `json:"content"`
					} `json:"text"`
				} `json:"caption"`
			} `json:"bookmark"`
		} `json:"children"`
	}
	if err := json.Unmarshal(captured, &parsed); err != nil {
		t.Fatalf("unmarshal captured: %v", err)
	}
	if len(parsed.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(parsed.Children))
	}
	bk := parsed.Children[0].Bookmark
	if bk.URL != "https://example.com" {
		t.Errorf("bookmark url = %q, want https://example.com", bk.URL)
	}
	if len(bk.Caption) != 1 || bk.Caption[0].Text.Content != "the site" {
		t.Errorf("caption not propagated: %+v", bk.Caption)
	}
}

// TestBlocksAdd_RejectsNonAddable asserts that `blocks add … -t table`
// surfaces a human-readable error, propagates it to cobra so the process
// exits non-zero, and does NOT issue a PATCH. Regression guard for
// PR #50 second-pass review [P1] — text-mode failures used to return nil.
func TestBlocksAdd_RejectsNonAddable(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetBlocksAddFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "ignored", "-t", "table"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("rootCmd.Execute returned nil; expected non-nil so the process exits non-zero")
	}
	if !strings.Contains(err.Error(), "cannot be created via") {
		t.Errorf("err = %v; want substring 'cannot be created via'", err)
	}
	if atomic.LoadInt64(&patched) != 0 {
		t.Error("blocks add -t table should not PATCH the API")
	}
}

// TestBlocksAdd_RejectsUnsupportedType asserts an unknown block type
// errors out before any HTTP traffic and the error is propagated to
// cobra (non-nil RunE return).
func TestBlocksAdd_RejectsUnsupportedType(t *testing.T) {
	srv := withCmdEnv(t)
	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetBlocksAddFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "ignored", "-t", "not-a-real-type"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error on unsupported block type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported block type") {
		t.Errorf("err = %v; want substring 'unsupported block type'", err)
	}
	if atomic.LoadInt64(&patched) != 0 {
		t.Error("blocks add with unsupported type should not PATCH the API")
	}
}

// TestBlocksAdd_RichTextJSONUnreadableFile confirms a missing
// --rich-text-json file produces a non-nil error from cobra so callers
// don't silently treat the failure as success.
func TestBlocksAdd_RichTextJSONUnreadableFile(t *testing.T) {
	_ = withCmdEnv(t)
	resetBlocksAddFlags()
	resetRootCmdArgs()

	missing := "/nonexistent/path/should-not-exist.json"
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "--rich-text-json", missing, "-t", "paragraph"})
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error on unreadable --rich-text-json file, got nil")
	}
	if !strings.Contains(err.Error(), "read --rich-text-json") {
		t.Errorf("err = %v; want substring 'read --rich-text-json'", err)
	}
}

// TestBlocksDelete_InvalidIndexExitsNonZero pins the post-#74 contract:
// `blocks delete <not-a-number>` must propagate a non-nil error from
// RunE so the process exits non-zero. Pre-fix the text-mode branch
// printed via color.Red and returned nil — every CI script piping
// through `blocks delete` would silently treat the failure as success.
func TestBlocksDelete_InvalidIndexExitsNonZero(t *testing.T) {
	_ = withCmdEnv(t)
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "delete", "not-a-number"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("blocks delete with non-numeric index should return non-nil error")
	}
	if !strings.Contains(err.Error(), "not a valid number") {
		t.Errorf("err = %v; want substring 'not a valid number'", err)
	}
}

// TestBlocksDelete_APIErrorExitsNonZero pins the same contract for the
// DELETE-failed branch — when the upstream call errors, RunE must
// propagate the error rather than swallowing it after color.Red.
func TestBlocksDelete_APIErrorExitsNonZero(t *testing.T) {
	srv := withCmdEnv(t)
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Force every request to a 500 so blocks delete's API call
		// fails. The default mock would 200 on the listing, so we
		// short-circuit here.
		if r.Method == http.MethodGet || r.Method == http.MethodDelete {
			http.Error(w, `{"object":"error","status":500,"code":"server_error"}`, http.StatusInternalServerError)
			return
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "delete", "1"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("blocks delete on a failing API should return non-nil error")
	}
	if !strings.Contains(err.Error(), "blocks delete") {
		t.Errorf("err = %v; want wrapping context 'blocks delete'", err)
	}
}

// TestBlocksDeleteDispatch runs `notioncli blocks delete 1` and asserts
// that both a listing (to resolve the target id) and a DELETE were issued.
func TestBlocksDeleteDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var listed, deleted int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children"):
			atomic.AddInt64(&listed, 1)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/blocks/"):
			atomic.AddInt64(&deleted, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	blockType = ""

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "delete", "1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks delete): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("blocks delete did not list children")
	}
	if atomic.LoadInt64(&deleted) == 0 {
		t.Error("blocks delete did not DELETE a block")
	}
}

// TestBlocksAddFlags_RichTextJSONFlag asserts the --rich-text-json flag
// is registered on the add subcommand with the expected shape.
func TestBlocksAddFlags_RichTextJSONFlag(t *testing.T) {
	add := findBlocksSubcommand(t, "add")
	f := add.Flag("rich-text-json")
	if f == nil {
		t.Fatal("blocks add: --rich-text-json flag not registered")
	}
	if f.Value.Type() != "string" {
		t.Errorf("blocks add: --rich-text-json type = %q, want string", f.Value.Type())
	}
	if f.DefValue != "" {
		t.Errorf("blocks add: --rich-text-json default = %q, want empty", f.DefValue)
	}
}

// TestBlocksAddRichTextJSON_Dispatch asserts the happy path: given a
// valid rich-text JSON file, the command issues a PATCH carrying the
// multi-segment body (annotations preserved, more than one segment).
func TestBlocksAddRichTextJSON_Dispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	var body []byte
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&patched, 1)
			body, _ = io.ReadAll(r.Body)
		}
		origHandler.ServeHTTP(w, r)
	})

	// Write a fixture file with two segments (annotations on segment 2)
	// in an isolated tempdir so the test survives repeated runs.
	dir := t.TempDir()
	spec := filepath.Join(dir, "rt.json")
	payload := `[
		{"type":"text","text":{"content":"hello "},"annotations":{"bold":false,"italic":false,"strikethrough":false,"underline":false,"code":false,"color":"default"}},
		{"type":"text","text":{"content":"world"},"annotations":{"bold":true,"italic":false,"strikethrough":false,"underline":false,"code":false,"color":"red"}}
	]`
	if err := os.WriteFile(spec, []byte(payload), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	blockType = ""
	blocksAddRichTextJSON = ""
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "--rich-text-json", spec, "-t", "paragraph"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks add --rich-text-json): %v", err)
	}

	if atomic.LoadInt64(&patched) == 0 {
		t.Fatal("blocks add --rich-text-json did not PATCH /blocks/pageID/children")
	}
	wire := string(body)
	// Outbound body must carry both segments with annotations.
	if !strings.Contains(wire, `"content":"hello "`) || !strings.Contains(wire, `"content":"world"`) {
		t.Errorf("outbound body missing segments: %s", wire)
	}
	if !strings.Contains(wire, `"bold":true`) {
		t.Errorf("outbound body missing annotation flag: %s", wire)
	}
	if !strings.Contains(wire, `"color":"red"`) {
		t.Errorf("outbound body missing color annotation: %s", wire)
	}
}

// TestBlocksAddRichTextJSON_Malformed asserts that invalid JSON files
// do not issue a PATCH and surface an error instead. In --json mode the
// error is written to stderr as a JSON envelope.
func TestBlocksAddRichTextJSON_Malformed(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	dir := t.TempDir()
	spec := filepath.Join(dir, "rt.json")
	if err := os.WriteFile(spec, []byte(`not-an-array`), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	blockType = ""
	blocksAddRichTextJSON = ""
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	resetRootCmdArgs()
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	// Use --json so the error surfaces as a returned error on stderr
	// (the text path swallows errors and returns nil from RunE).
	rootCmd.SetArgs([]string{"--json", "blocks", "add", "--rich-text-json", spec, "-t", "paragraph"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for malformed --rich-text-json, got nil")
	}
	if atomic.LoadInt64(&patched) != 0 {
		t.Error("malformed --rich-text-json should not issue a PATCH")
	}
	if !strings.Contains(errBuf.String(), "rich-text JSON") {
		t.Errorf("stderr missing parse error: %q", errBuf.String())
	}
}

// TestBlocksAddRichTextJSON_MissingFile asserts the I/O error surfaces
// cleanly and does not PATCH.
func TestBlocksAddRichTextJSON_MissingFile(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	blockType = ""
	blocksAddRichTextJSON = ""
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	resetRootCmdArgs()
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"--json", "blocks", "add", "--rich-text-json", "/does/not/exist.json", "-t", "paragraph"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for missing --rich-text-json file, got nil")
	}
	if atomic.LoadInt64(&patched) != 0 {
		t.Error("missing --rich-text-json should not issue a PATCH")
	}
}

// TestBlocksAddRichTextJSON_MutualExclusion verifies the Args check
// fires when both --rich-text-json and a positional text arg are
// supplied via the command line.
func TestBlocksAddRichTextJSON_MutualExclusion(t *testing.T) {
	withCmdEnv(t)

	dir := t.TempDir()
	spec := filepath.Join(dir, "rt.json")
	if err := os.WriteFile(spec, []byte(`[{"type":"text","text":{"content":"x"}}]`), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	blockType = ""
	blocksAddRichTextJSON = ""
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "hello", "--rich-text-json", spec})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected mutual-exclusion error, got nil")
	}
}

// TestBlocksList_RichTextJSON asserts that in --json mode the blocks
// list path emits the rich_text array verbatim (annotations, nested
// text.content fields) — not just the first plain_text segment.
func TestBlocksList_RichTextJSON(t *testing.T) {
	withCmdEnv(t)

	blockType = ""
	blocksAddRichTextJSON = ""
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--json", "blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks list --json): %v", err)
	}

	wire := out.String()
	// The shared mock server emits a to_do block; the --json path
	// should include the full rich_text shape, not just plain_text.
	for _, needle := range []string{`"rich_text"`, `"annotations"`, `"plain_text":"buy milk"`} {
		if !strings.Contains(wire, needle) {
			t.Errorf("blocks list --json wire missing %q\nfull output: %s", needle, wire)
		}
	}
}

// TestBlocksListJSONLossless guards issue #86: `blocks list --json`
// encoded the typed []utils.Block, which models only the block types the
// human renderer knows. Anything else — child_database, synced_block
// metadata, column/column_list, any newer shape — came out with an empty
// payload object, contradicting the command's "emit raw Notion block
// objects" godoc. The JSON path now emits Notion's own bytes.
//
// Both subtests pass --type explicitly. That keeps this test independent
// of the `blocks list` type-filter defect (#88, fixed separately): a
// parse-time --type assignment is honoured either way.
func TestBlocksListJSONLossless(t *testing.T) {
	// rawChildren serves a fixture whose blocks carry payloads and
	// top-level keys the typed Block struct does not model.
	rawChildren := func(t *testing.T) {
		t.Helper()
		srv := withCmdEnv(t)
		t.Setenv("NOTION_PAGE_ID", "rawPage")
		origHandler := srv.Config.Handler
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/blocks/rawPage/children" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","results":[
					{"object":"block","id":"b1","type":"child_database",
					 "has_children":false,"in_trash":false,
					 "child_database":{"title":"Q2 Tracker"}},
					{"object":"block","id":"b2","type":"paragraph",
					 "has_children":false,
					 "created_by":{"object":"user","id":"u-1"},
					 "paragraph":{"rich_text":[{"type":"text","plain_text":"hi"}],"color":"blue"}}
				],"has_more":false,"next_cursor":""}`))
				return
			}
			origHandler.ServeHTTP(w, r)
		})
	}

	run := func(t *testing.T, blockTypeArg string) map[string]interface{} {
		t.Helper()
		resetRootCmdArgs()
		var out bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&out)
		rootCmd.SetArgs([]string{"blocks", "list", "--json", "--type", blockTypeArg})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("rootCmd.Execute(blocks list --json --type %s): %v", blockTypeArg, err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("want 1 NDJSON line for --type %s, got %d:\n%s", blockTypeArg, len(lines), out.String())
		}
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatalf("output is not JSON: %v (%s)", err, lines[0])
		}
		return got
	}

	t.Run("unmodeled block payload survives", func(t *testing.T) {
		rawChildren(t)
		got := run(t, "child_database")

		cd, ok := got["child_database"].(map[string]interface{})
		if !ok || cd["title"] != "Q2 Tracker" {
			t.Errorf("child_database payload lost; got %v", got["child_database"])
		}
	})

	t.Run("unmodeled top-level keys survive", func(t *testing.T) {
		rawChildren(t)
		got := run(t, "paragraph")

		if _, ok := got["created_by"]; !ok {
			t.Errorf("created_by dropped from raw block; got keys %v", mapKeys(got))
		}
		para, ok := got["paragraph"].(map[string]interface{})
		if !ok || para["color"] != "blue" {
			t.Errorf("paragraph.color dropped; got %v", got["paragraph"])
		}
	})
}
