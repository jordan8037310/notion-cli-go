package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"notioncli/utils"
	"os"
	"path/filepath"
	"testing"
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
		// GET children: default page has one to_do item.
		case r.Method == http.MethodGet && r.URL.Path == "/blocks/pageID/children":
			writeJSON(w, utils.BlockList{Results: []utils.Block{
				todo("blockID", "buy milk", false),
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

	// 1. Mock server + redirect utils package baseURL.
	srv := cmdMockServer(t)
	utils.SetBaseURL(srv.URL)
	t.Cleanup(func() {
		// Restore to something harmless. The utils package default would be
		// https://api.notion.com/v1 but we don't export it; pointing at the
		// (now-closed) mock server URL is safe because no further calls run.
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
func resetRootCmdArgs(t *testing.T) {
	t.Helper()
	rootCmd.SetArgs(nil)
}
