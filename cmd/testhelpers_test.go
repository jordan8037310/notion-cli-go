// Tests in this package must not call t.Parallel(): several helpers mutate
// package-level state (utils.baseURL, blockType, cwd) that cannot safely be
// shared across concurrent sub-tests. Running serially is the explicit design.
package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// cmdMockServer returns an httptest.Server that answers every Notion endpoint
// the cmd/ package indirectly hits through utils. It mirrors the enhanced mock
// in utils/block_extra_test.go but lives here so the cmd tests stay
// self-contained and don't depend on test-package internals across directories.
//
// Supported page fixtures:
//   - pageID              : one unchecked to_do "buy milk"
//   - emptyPage           : no results
//
// Supported block id:
//   - blockID             : any PATCH/DELETE to /blocks/blockID → 200
func cmdMockServer(t *testing.T) *httptest.Server {
	t.Helper()

	todo := func(id, text string, checked bool) utils.Block {
		return utils.Block{
			Object:         "block",
			ID:             id,
			Type:           "to_do",
			LastEditedTime: "2026-04-22T10:00:00.000Z",
			ToDo:           &utils.ToDo{Checked: checked, RichText: []utils.RichText{{PlainText: text}}},
		}
	}
	paragraph := func(id, text string) utils.Block {
		return utils.Block{
			Object:         "block",
			ID:             id,
			Type:           "paragraph",
			LastEditedTime: "2026-04-22T11:00:00.000Z",
			Paragraph:      &utils.RichTextBlock{RichText: []utils.RichText{{PlainText: text}}},
		}
	}
	heading1 := func(id, text string) utils.Block {
		return utils.Block{
			Object:         "block",
			ID:             id,
			Type:           "heading_1",
			LastEditedTime: "2026-04-22T12:00:00.000Z",
			Heading1:       &utils.RichTextBlock{RichText: []utils.RichText{{PlainText: text}}},
		}
	}

	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// GET /teams: default response exercises pagination-free list.
		case r.Method == http.MethodGet && r.URL.Path == "/teams":
			writeJSON(w, utils.TeamList{
				Object: "list",
				Results: []utils.Team{
					{Object: "team", ID: "team-1", Name: "Marketing"},
				},
			})

		// POST /data_sources/{id}/views: views create.
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/data_sources/") && strings.HasSuffix(r.URL.Path, "/views"):
			writeJSON(w, utils.View{
				Object: "view",
				ID:     "view-created-id",
				Name:   "n",
				Type:   "table",
			})

		// PATCH /views/{id}: views update.
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/views/"):
			writeJSON(w, utils.View{
				Object: "view",
				ID:     "view-updated-id",
				Name:   "Renamed",
				Type:   "table",
			})

		// POST /file_uploads: step 1 of the upload flow.
		case r.Method == http.MethodPost && r.URL.Path == "/file_uploads":
			// Echo the filename from the JSON body so step 2's
			// mock response reuses the request's filename field.
			var fu utils.FileUploadRequest
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &fu)
			upload := "http://" + r.Host + "/file_uploads/cmd-file-id/send"
			writeJSON(w, utils.FileUploadResponse{
				Object:    "file_upload",
				ID:        "cmd-file-id",
				UploadURL: upload,
				Status:    "pending",
				Filename:  fu.Filename,
			})

		// POST /file_uploads/{id}/send: step 2.
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/file_uploads/") && strings.HasSuffix(r.URL.Path, "/send"):
			writeJSON(w, utils.FileUploadResponse{
				Object: "file_upload",
				ID:     "cmd-file-id",
				Status: "uploaded",
			})

		// GET children: default page has one to_do item.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/pageID/children":
			writeJSON(w, utils.BlockList{Results: []utils.Block{
				todo("blockID", "buy milk", false),
			}})

		// GET children: aliased page id used by the multi-page --page
		// integration tests. The page id below matches the uuid we
		// register under the "work" alias in alias-aware test fixtures.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/11111111111111111111111111111111/children":
			writeJSON(w, utils.BlockList{Results: []utils.Block{
				todo("aliasBlock", "aliased task", false),
			}})

		// GET children: mixed page for blocks subcommand tests.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/mixedPage/children":
			writeJSON(w, utils.BlockList{Results: []utils.Block{
				heading1("m1", "Title"),
				paragraph("m2", "Body"),
				todo("m3", "A task", false),
			}})

		// GET children: empty page.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/emptyPage/children":
			writeJSON(w, utils.BlockList{Results: []utils.Block{}})

		// GET /pages/{id}: a minimal page object. `pages set-icon` and
		// `pages set-cover` verify the page id before uploading, so the
		// happy path needs this to resolve (issue #82).
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
			writeJSON(w, utils.Page{
				Object: "page",
				ID:     strings.TrimPrefix(r.URL.Path, "/pages/"),
				URL:    "https://notion.so" + r.URL.Path,
			})

		// PATCH to /blocks/{id}/children (AddNewToDoItem, AddBlock) → 200
		// PATCH to /blocks/{id} (MarkChecked, MarkUnchecked) → 200
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))

		// DELETE any block → 200
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))

		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))
}

// withCmdEnv wires up everything a cmd-layer test needs:
//   - an httptest mock server, with utils.baseURL pointed at it
//   - NOTION_API_KEY / NOTION_PAGE_ID / LOCAL_TIMEZONE set to sane defaults
//   - a temp cwd containing an empty .env, and an empty HOME, so that
//     godotenv.Load in utils.SetAPIConfig succeeds without picking up the
//     developer's real ~/.config/notioncli/.env
//
// Returns the mock server so tests can inspect request counts if they want.
// All state is restored by t.Cleanup.
func withCmdEnv(t *testing.T) *httptest.Server {
	t.Helper()

	// 1. Mock server + redirect utils package baseURL. Snapshot the prior
	// value so a later test running in the same process without going
	// through withCmdEnv doesn't inherit a pointer to a closed server.
	srv := cmdMockServer(t)
	priorBaseURL := utils.GetBaseURL()
	utils.SetBaseURL(srv.URL)
	t.Cleanup(func() {
		utils.SetBaseURL(priorBaseURL)
		srv.Close()
	})

	// 2. Isolated cwd with an empty .env so godotenv.Load returns nil.
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

	// 3. Env vars.
	t.Setenv("HOME", emptyHome)
	t.Setenv("NOTION_API_KEY", "test-key")
	t.Setenv("NOTION_PAGE_ID", "pageID")
	t.Setenv("LOCAL_TIMEZONE", "UTC")

	return srv
}

// resetRootCmdArgs clears any args previously set on rootCmd. cobra retains
// args across calls, so tests that share rootCmd must reset between runs.
// The global output flags (--json, --pretty, --output) are also reset here
// because cobra binds bool/string flags to package-level vars that persist
// across Execute calls — without the reset one --json test would flip every
// subsequent test into JSON mode and break assertions on human output.
func resetRootCmdArgs() {
	rootCmd.SetArgs(nil)
	resetGlobalOutputFlags()
}

// findTopLevelCmd returns the rootCmd child with the given Name, or fails
// the test. Used by command-specific test files to locate the real *cobra.Command
// instance without duplicating the linear scan across files.
func findTopLevelCmd(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("%q command not registered on rootCmd", name)
	return nil
}
