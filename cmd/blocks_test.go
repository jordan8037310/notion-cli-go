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
// surfaces a human-readable error and does NOT issue a PATCH.
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
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute returned err: %v (expected swallowed)", err)
	}
	if atomic.LoadInt64(&patched) != 0 {
		t.Error("blocks add -t table should not PATCH the API")
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

// TestBlocksAddArgs_RichTextJSONXORText covers the mutual-exclusion
// between --rich-text-json and the positional text arg. Uses the Args
// func directly so no mock is needed.
func TestBlocksAddArgs_RichTextJSONXORText(t *testing.T) {
	add := findBlocksSubcommand(t, "add")
	t.Cleanup(func() { blocksAddRichTextJSON = "" })

	// With flag set: zero args ok, one arg must error.
	blocksAddRichTextJSON = "spec.json"
	if err := add.Args(add, []string{}); err != nil {
		t.Errorf("zero args with --rich-text-json: err = %v, want nil", err)
	}
	if err := add.Args(add, []string{"oops"}); err == nil {
		t.Error("positional + --rich-text-json: err = nil, want mutual-exclusion error")
	}

	// Flag cleared: the old MinimumNArgs(1) contract returns.
	blocksAddRichTextJSON = ""
	if err := add.Args(add, []string{}); err == nil {
		t.Error("zero args without flag: err = nil, want MinimumNArgs error")
	}
	if err := add.Args(add, []string{"hello"}); err != nil {
		t.Errorf("one arg without flag: err = %v, want nil", err)
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
