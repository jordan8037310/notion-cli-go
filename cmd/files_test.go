package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"notioncli/utils"

	"github.com/fatih/color"
)

// TestFiles_Cmd_Registered asserts the four new subcommands (two under
// blocks, two under pages) are wired into rootCmd.
func TestFiles_Cmd_Registered(t *testing.T) {
	wantBlocks := []string{"add-image", "add-file"}
	for _, name := range wantBlocks {
		if findBlocksSubcommand(t, name) == nil {
			t.Errorf("blocks subcommand %q not registered", name)
		}
	}
	wantPages := []string{"set-icon", "set-cover"}
	for _, name := range wantPages {
		if findPagesSubcommand(t, name) == nil {
			t.Errorf("pages subcommand %q not registered", name)
		}
	}
}

// TestFiles_Cmd_AddFileHasNameFlag locks in the --name flag on add-file.
func TestFiles_Cmd_AddFileHasNameFlag(t *testing.T) {
	addFile := findBlocksSubcommand(t, "add-file")
	if addFile.Flags().Lookup("name") == nil {
		t.Error("blocks add-file: --name flag not registered")
	}
}

// TestFiles_NewFileClient_Cmd_ReturnsErrMissingAPIKey drives the
// cmd-layer helper directly.
func TestFiles_NewFileClient_Cmd_ReturnsErrMissingAPIKey(t *testing.T) {
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
	t.Setenv("NOTION_API_KEY", "")
	t.Setenv("NOTION_PAGE_ID", "pageID")
	t.Setenv("LOCAL_TIMEZONE", "UTC")

	got, err := newFileClient()
	if err == nil {
		t.Fatalf("newFileClient: want error, got %+v", got)
	}
	if !errors.Is(err, utils.ErrMissingAPIKey) {
		t.Errorf("newFileClient: want errors.Is ErrMissingAPIKey, got %v", err)
	}
	if got != nil {
		t.Errorf("newFileClient: want nil client, got %+v", got)
	}
}

// runFilesCmd runs a files-related subcommand through rootCmd and
// returns cobra's Execute error plus the color output buffer.
func runFilesCmd(t *testing.T, args []string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	resetRootCmdArgs()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)

	// Capture fatih/color output so the happy-path "Uploaded..." line
	// is visible to assertions.
	origColorOut := color.Output
	origColorErr := color.Error
	color.Output = &buf
	color.Error = &buf
	t.Cleanup(func() {
		color.Output = origColorOut
		color.Error = origColorErr
	})

	err := rootCmd.Execute()
	return buf.String(), err
}

// TestFiles_Cmd_DispatchHappyPath is the end-to-end smoke test for
// every subcommand: each runs the full two-step upload flow against the
// cmd mock server and prints the expected "Uploaded ..." / "Set ..."
// line with the file ID.
func TestFiles_Cmd_DispatchHappyPath(t *testing.T) {
	_ = withCmdEnv(t)

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "hello.png")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantFrag string
	}{
		{"blocks add-image", []string{"blocks", "add-image", filePath}, "Added image block"},
		{"blocks add-file", []string{"blocks", "add-file", filePath}, "Added file block"},
		{"blocks add-file with --name", []string{"blocks", "add-file", filePath, "--name", "nice-name.txt"}, "nice-name.txt"},
		{"pages set-icon", []string{"pages", "set-icon", "page-abc", filePath}, "Set icon on page"},
		{"pages set-cover", []string{"pages", "set-cover", "page-abc", filePath}, "Set cover on page"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Reset --name flag between rows since it's package-level.
			blocksAddFileName = ""
			out, err := runFilesCmd(t, tt.args)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v (output=%s)", tt.name, err, out)
			}
			if !strings.Contains(out, tt.wantFrag) {
				t.Errorf("%s: output missing %q:\n%s", tt.name, tt.wantFrag, out)
			}
			if !strings.Contains(out, "cmd-file-id") {
				t.Errorf("%s: output missing uploaded file id:\n%s", tt.name, out)
			}
		})
	}
}

// TestFiles_Cmd_Dispatch_PathValidation confirms per-path validation
// errors surface directly (not buried behind an auth error).
func TestFiles_Cmd_Dispatch_PathValidation(t *testing.T) {
	_ = withCmdEnv(t)

	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.bin")
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantFrag string
	}{
		{"add-image missing path", []string{"blocks", "add-image", missing}, "nope.bin"},
		{"add-file directory", []string{"blocks", "add-file", subdir}, "directory"},
		{"set-icon missing path", []string{"pages", "set-icon", "page-abc", missing}, "nope.bin"},
		{"set-cover directory", []string{"pages", "set-cover", "page-abc", subdir}, "directory"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			blocksAddFileName = ""
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want error, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantFrag) {
				t.Errorf("%s: want error to contain %q, got %v", tt.name, tt.wantFrag, err)
			}
		})
	}
}

// TestFiles_Cmd_Dispatch_ArgValidation asserts cobra's positional-arg
// constraints for each command.
func TestFiles_Cmd_Dispatch_ArgValidation(t *testing.T) {
	_ = withCmdEnv(t)

	tests := []struct {
		name string
		args []string
	}{
		{"add-image no args", []string{"blocks", "add-image"}},
		{"add-file no args", []string{"blocks", "add-file"}},
		{"set-icon one arg", []string{"pages", "set-icon", "page-abc"}},
		{"set-cover one arg", []string{"pages", "set-cover", "page-abc"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			blocksAddFileName = ""
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want cobra arg error, got nil", tt.name)
			}
		})
	}
}

// TestFiles_SetIconCover_IssuesPatch guards issue #82. `pages set-icon`
// and `pages set-cover` accepted a page id, uploaded the file, printed
// "Uploaded icon for page <id> — icon PATCH deferred" and exited 0
// without ever contacting that page. A typo'd or unshared id looked
// exactly like success.
//
// Asserts the whole flow now: the page is verified, the file is
// uploaded, and a PATCH lands on /pages/{id} carrying the file_upload
// envelope under the right key.
func TestFiles_SetIconCover_IssuesPatch(t *testing.T) {
	for _, tc := range []struct {
		cmdName string
		field   string
	}{
		{"set-icon", "icon"},
		{"set-cover", "cover"},
	} {
		t.Run(tc.cmdName, func(t *testing.T) {
			srv := withCmdEnv(t)

			tmp := t.TempDir()
			filePath := filepath.Join(tmp, "hello.png")
			if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
				t.Fatalf("write tmp file: %v", err)
			}

			var (
				mu        sync.Mutex
				pageGets  int
				patchBody []byte
				patchPath string
			)
			orig := srv.Config.Handler
			srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/") {
					pageGets++
				}
				if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/pages/") {
					patchBody, _ = io.ReadAll(r.Body)
					patchPath = r.URL.Path
				}
				mu.Unlock()
				orig.ServeHTTP(w, r)
			})

			out, err := runFilesCmd(t, []string{"pages", tc.cmdName, "page-abc", filePath})
			if err != nil {
				t.Fatalf("%s: unexpected error: %v (output=%s)", tc.cmdName, err, out)
			}

			mu.Lock()
			defer mu.Unlock()

			if pageGets == 0 {
				t.Error("page id was never verified before the upload")
			}
			if patchPath != "/pages/page-abc" {
				t.Fatalf("no PATCH landed on the page; got path %q", patchPath)
			}

			var body map[string]interface{}
			if err := json.Unmarshal(patchBody, &body); err != nil {
				t.Fatalf("PATCH body is not JSON: %v (%s)", err, patchBody)
			}
			field, ok := body[tc.field].(map[string]interface{})
			if !ok {
				t.Fatalf("PATCH body has no %q key: %s", tc.field, patchBody)
			}
			if field["type"] != "file_upload" {
				t.Errorf("%s.type = %v, want file_upload", tc.field, field["type"])
			}
			fu, ok := field["file_upload"].(map[string]interface{})
			if !ok || fu["id"] != "cmd-file-id" {
				t.Errorf("%s.file_upload = %v, want the uploaded file id", tc.field, field["file_upload"])
			}
			if strings.Contains(out, "deferred") {
				t.Errorf("output still claims the PATCH is deferred: %q", out)
			}
		})
	}
}

// TestFiles_SetIcon_BadPageIDFails guards the other half of #82: a page
// id that does not resolve must fail, not exit 0. It must also fail
// BEFORE the upload, so a typo does not strand an orphaned file upload
// in the workspace.
func TestFiles_SetIcon_BadPageIDFails(t *testing.T) {
	srv := withCmdEnv(t)

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "hello.png")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	var mu sync.Mutex
	uploads := 0
	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/pages/ghost" {
			http.Error(w, `{"object":"error","code":"object_not_found"}`, http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/file_uploads" {
			mu.Lock()
			uploads++
			mu.Unlock()
		}
		orig.ServeHTTP(w, r)
	})

	_, err := runFilesCmd(t, []string{"pages", "set-icon", "ghost", filePath})
	if err == nil {
		t.Fatal("set-icon on an unresolvable page id exited 0; want an error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the page id, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if uploads != 0 {
		t.Errorf("uploaded %d file(s) despite the page id not resolving; want 0", uploads)
	}
}

// TestFiles_AddImageAndFileAppendTheBlock guards issue #124. Both commands
// uploaded the file, reported success — add-file's JSON envelope even said
// "ok":true — and never created a block, so the page listed nothing
// afterwards. Same shape as #82, which fixed pages set-icon and left these
// two behind.
func TestFiles_AddImageAndFileAppendTheBlock(t *testing.T) {
	for _, tt := range []struct{ cmdName, blockType string }{
		{"add-image", "image"},
		{"add-file", "file"},
	} {
		t.Run(tt.cmdName, func(t *testing.T) {
			srv := withCmdEnv(t)

			tmp := t.TempDir()
			filePath := filepath.Join(tmp, "hello.png")
			if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
				t.Fatalf("write tmp file: %v", err)
			}

			var mu sync.Mutex
			var appendBody []byte
			orig := srv.Config.Handler
			srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/blocks/pageID/children") {
					mu.Lock()
					appendBody, _ = io.ReadAll(r.Body)
					mu.Unlock()
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("{}"))
					return
				}
				orig.ServeHTTP(w, r)
			})

			blocksAddFileName = ""
			out, err := runFilesCmd(t, []string{"blocks", tt.cmdName, filePath})
			if err != nil {
				t.Fatalf("%s: %v (output=%s)", tt.cmdName, err, out)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(appendBody) == 0 {
				t.Fatalf("%s uploaded but never appended a block — the page stays empty", tt.cmdName)
			}
			var sent struct {
				Children []map[string]interface{} `json:"children"`
			}
			if err := json.Unmarshal(appendBody, &sent); err != nil {
				t.Fatalf("append body is not JSON: %v (%s)", err, appendBody)
			}
			if len(sent.Children) != 1 || sent.Children[0]["type"] != tt.blockType {
				t.Fatalf("appended %+v, want one %s block", sent.Children, tt.blockType)
			}
			inner, _ := sent.Children[0][tt.blockType].(map[string]interface{})
			if inner["type"] != "file_upload" {
				t.Errorf("block does not reference the upload: %v", inner)
			}
			fu, _ := inner["file_upload"].(map[string]interface{})
			if fu["id"] != "cmd-file-id" {
				t.Errorf("block references %v, want the uploaded file id", inner["file_upload"])
			}
		})
	}
}

// TestFiles_AddResolvesPageBeforeUploading confirms a bad target fails
// without stranding an orphaned upload — the same ordering rule set-icon
// adopted in #82.
func TestFiles_AddResolvesPageBeforeUploading(t *testing.T) {
	srv := withCmdEnv(t)
	t.Setenv("NOTION_PAGE_ID", "")

	var mu sync.Mutex
	uploads := 0
	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/file_uploads" {
			mu.Lock()
			uploads++
			mu.Unlock()
		}
		orig.ServeHTTP(w, r)
	})

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "hello.png")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	blocksAddFileName = ""
	if _, err := runFilesCmd(t, []string{"blocks", "add-file", filePath}); err == nil {
		t.Fatal("no target page: want an error")
	}
	mu.Lock()
	defer mu.Unlock()
	if uploads != 0 {
		t.Errorf("uploaded %d file(s) despite having no target page; want 0", uploads)
	}
}

// TestBlocksDownload_WritesTheFile covers the cmd path end to end against
// the mock: resolve the block by index, follow its URL, write to disk.
func TestBlocksDownload_WritesTheFile(t *testing.T) {
	srv := withCmdEnv(t)
	const payload = "block-attachment-bytes"

	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/filePage/children":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","has_more":false,"results":[
				{"object":"block","id":"b1","type":"paragraph","paragraph":{"rich_text":[]}},
				{"object":"block","id":"b2","type":"file","file":{"type":"file",
				 "file":{"url":"` + srv.URL + `/dl/report.txt"}}}
			]}`))
		case r.URL.Path == "/dl/report.txt":
			_, _ = w.Write([]byte(payload))
		default:
			orig.ServeHTTP(w, r)
		}
	})
	t.Setenv("NOTION_PAGE_ID", "filePage")

	dir := t.TempDir()
	out := filepath.Join(dir, "saved.txt")

	blocksDownloadOut = out
	t.Cleanup(func() { blocksDownloadOut = "" })
	if o, err := runFilesCmd(t, []string{"blocks", "download", "2", "-o", out}); err != nil {
		t.Fatalf("blocks download: %v (%s)", err, o)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != payload {
		t.Errorf("downloaded %q, want %q", got, payload)
	}
}

// TestBlocksDownload_RejectsNonMediaBlocks. Asking for a paragraph should
// say why rather than write an empty file.
func TestBlocksDownload_RejectsNonMediaBlocks(t *testing.T) {
	withCmdEnv(t)
	t.Setenv("NOTION_PAGE_ID", "pageID") // default fixture: one to_do block

	blocksDownloadOut = ""
	out, err := runFilesCmd(t, []string{"blocks", "download", "1"})
	if err == nil {
		t.Fatalf("downloading a to_do should fail, got: %s", out)
	}
	if !strings.Contains(err.Error(), "carries no downloadable file") {
		t.Errorf("error should explain why, got: %v", err)
	}
}

// TestBlocksDownload_OutOfRangeNamesTheCount so the user can see how far
// the index was off.
func TestBlocksDownload_OutOfRangeNamesTheCount(t *testing.T) {
	withCmdEnv(t)
	t.Setenv("NOTION_PAGE_ID", "pageID")

	blocksDownloadOut = ""
	_, err := runFilesCmd(t, []string{"blocks", "download", "99"})
	if err == nil {
		t.Fatal("out-of-range index: want an error")
	}
	if !strings.Contains(err.Error(), "out of range") || !strings.Contains(err.Error(), "1 blocks") {
		t.Errorf("error should name the range, got: %v", err)
	}
}
