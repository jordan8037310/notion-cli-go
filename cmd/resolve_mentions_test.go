// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"notioncli/utils"
)

// resolveMentionsMockServer answers the two Notion endpoints the cmd
// layer hits when --resolve-mentions is on: GET /blocks/pageID/children
// (returns to_do blocks each carrying a page-mention run) and
// GET /pages/{id} (returns a Notion page with a title property).
// Counts page GETs so tests can assert the cache fires exactly once.
type resolveMentionsMockServer struct {
	srv          *httptest.Server
	pageGetCalls int64
	titles       map[string]string // page id → title; missing entry → 404
	blocks       []utils.Block
}

func newResolveMentionsMockServer(t *testing.T, blocks []utils.Block, titles map[string]string) *resolveMentionsMockServer {
	t.Helper()
	m := &resolveMentionsMockServer{blocks: blocks, titles: titles}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *resolveMentionsMockServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/blocks/pageID/children":
		_ = json.NewEncoder(w).Encode(utils.BlockList{Results: m.blocks})

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pages/"):
		atomic.AddInt64(&m.pageGetCalls, 1)
		id := strings.TrimPrefix(r.URL.Path, "/pages/")
		title, ok := m.titles[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "page",
			"id":     id,
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"id":   "title",
					"type": "title",
					"title": []interface{}{
						map[string]interface{}{
							"type":       "text",
							"plain_text": title,
							"text":       map[string]interface{}{"content": title},
						},
					},
				},
			},
		})

	default:
		http.Error(w, `{"code":"unexpected"}`, http.StatusBadRequest)
	}
}

// mentionBlock is a page-mention-bearing to_do fixture.
//
// plainText is empty in these tests on purpose. Notion sends the mentioned
// page's TITLE in plain_text and it now wins outright (#41), so the
// resolver is only reached in the degraded case — which is what these
// tests are about. This fixture used to hardcode "[page:<id>]", a shape
// Notion never sends.
//
// The mock
// server's /blocks/pageID/children endpoint returns these.
func mentionBlock(id, pageID string) utils.Block {
	return utils.Block{
		Object:         "block",
		ID:             id,
		Type:           "to_do",
		LastEditedTime: "2026-04-22T10:00:00.000Z",
		ToDo: &utils.ToDo{
			Checked: false,
			RichText: []utils.RichText{
				{
					Type:    "mention",
					Mention: &utils.Mention{Type: "page", Page: &utils.PageMention{ID: pageID}},
				},
			},
		},
	}
}

// withResolveMentionsEnv wires up the cmd-layer test environment with
// the resolver-aware mock server in place of the default one. It
// mirrors withCmdEnv but targets the resolver-specific endpoints.
func withResolveMentionsEnv(t *testing.T, blocks []utils.Block, titles map[string]string) *resolveMentionsMockServer {
	t.Helper()

	m := newResolveMentionsMockServer(t, blocks, titles)
	priorBaseURL := utils.GetBaseURL()
	utils.SetBaseURL(m.srv.URL)
	t.Cleanup(func() { utils.SetBaseURL(priorBaseURL) })

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

	return m
}

// TestBlocksList_ResolveMentions_Off verifies the default (flag absent)
// keeps the legacy "[page:<id>]" rendering and does NOT issue any
// /v1/pages/<id> GETs. This is the backward-compat contract — users
// who don't opt in must see pre-resolver output byte-for-byte.
func TestBlocksList_ResolveMentions_Off(t *testing.T) {
	m := withResolveMentionsEnv(t,
		[]utils.Block{mentionBlock("b-1", "p-42")},
		map[string]string{"p-42": "Should Not Be Used"},
	)

	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}

	if !strings.Contains(out.String(), "[page:p-42]") {
		t.Errorf("flag-off output missing legacy marker:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Should Not Be Used") {
		t.Errorf("flag-off output should not contain resolved title:\n%s", out.String())
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 0 {
		t.Errorf("flag-off should not hit /pages/, got %d calls", got)
	}
}

// TestBlocksList_ResolveMentions_On_Title locks the happy path:
// --resolve-mentions on + mock returns a title → snippet contains
// "[<title>]" instead of "[page:<id>]".
func TestBlocksList_ResolveMentions_On_Title(t *testing.T) {
	m := withResolveMentionsEnv(t,
		[]utils.Block{mentionBlock("b-1", "p-42")},
		map[string]string{"p-42": "Project Plan"},
	)

	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--resolve-mentions", "blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}

	if !strings.Contains(out.String(), "[Project Plan]") {
		t.Errorf("expected expanded title in output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "[page:p-42]") {
		t.Errorf("expanded-title path should not leak legacy marker:\n%s", out.String())
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 1 {
		t.Errorf("expected 1 page GET, got %d", got)
	}
}

// TestBlocksList_ResolveMentions_On_Error covers the network-error
// fallback: --resolve-mentions on + mock returns 404 → snippet still
// renders as "[page:<id>]" and no panic / no stderr spam breaks the
// pipeline. The issue explicitly calls this guarantee out.
func TestBlocksList_ResolveMentions_On_Error(t *testing.T) {
	m := withResolveMentionsEnv(t,
		[]utils.Block{mentionBlock("b-1", "p-missing")},
		map[string]string{}, // empty → every page GET is a 404
	)

	blockType = ""
	resetRootCmdArgs()
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"--resolve-mentions", "blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}

	if !strings.Contains(out.String(), "[page:p-missing]") {
		t.Errorf("404 fallback missing legacy marker:\n%s", out.String())
	}
	// The resolver is silent on errors (negative cache); we do NOT want
	// stderr spam for every missing page. A strict emptiness check is
	// too brittle — just assert the error message from the 404 is not
	// leaked into output.
	if strings.Contains(errBuf.String(), "404") {
		t.Errorf("stderr should not leak 404 details:\n%s", errBuf.String())
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 1 {
		t.Errorf("expected 1 page GET (negative cache), got %d", got)
	}
}

// TestBlocksList_ResolveMentions_Caching is the hit-count assertion:
// three mentions of the same page across three blocks must trigger
// exactly one /v1/pages/<id> call. Exercises the full cmd→utils→
// CachingPageResolver path end-to-end.
func TestBlocksList_ResolveMentions_Caching(t *testing.T) {
	m := withResolveMentionsEnv(t,
		[]utils.Block{
			mentionBlock("b-1", "p-shared"),
			mentionBlock("b-2", "p-shared"),
			mentionBlock("b-3", "p-shared"),
		},
		map[string]string{"p-shared": "Runbook"},
	)

	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--resolve-mentions", "blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}

	if n := strings.Count(out.String(), "[Runbook]"); n != 3 {
		t.Errorf("expected 3 expanded titles in output, got %d:\n%s", n, out.String())
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 1 {
		t.Errorf("expected 1 page GET (cache), got %d", got)
	}
}

// TestBlocksList_ResolveMentions_JSONPathIgnoresFlag locks the design
// contract: --resolve-mentions is a human-output affordance. The JSON
// path must continue to emit the raw rich_text mention shape so tooling
// sees the original Notion payload. Even with --resolve-mentions set,
// no /v1/pages/<id> calls may fire on the JSON path.
func TestBlocksList_ResolveMentions_JSONPathIgnoresFlag(t *testing.T) {
	m := withResolveMentionsEnv(t,
		[]utils.Block{mentionBlock("b-1", "p-42")},
		map[string]string{"p-42": "Should Not Appear"},
	)

	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--json", "--resolve-mentions", "blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}

	wire := out.String()
	// The JSON path emits the raw rich_text array; the mention shape
	// survives and no resolved title leaks in.
	if !strings.Contains(wire, `"type":"page"`) {
		t.Errorf("JSON output missing raw page mention shape:\n%s", wire)
	}
	if strings.Contains(wire, "Should Not Appear") {
		t.Errorf("JSON path should not resolve titles:\n%s", wire)
	}
	if got := atomic.LoadInt64(&m.pageGetCalls); got != 0 {
		t.Errorf("JSON path should not trigger /pages/ calls, got %d", got)
	}
}

// TestResolveMentionsFlag_Registered is a cheap-to-run smoke test that
// the persistent --resolve-mentions flag is wired on rootCmd with the
// correct type and default value.
func TestResolveMentionsFlag_Registered(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("resolve-mentions")
	if f == nil {
		t.Fatal("rootCmd: --resolve-mentions flag not registered")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--resolve-mentions type = %q, want bool", f.Value.Type())
	}
	if f.DefValue != "false" {
		t.Errorf("--resolve-mentions default = %q, want false", f.DefValue)
	}
}

// TestBuildPageResolver_Off verifies buildPageResolver returns
// utils.NoPageResolver when the flag is off (the default).
func TestBuildPageResolver_Off(t *testing.T) {
	globalResolveMentions = false
	t.Cleanup(func() { globalResolveMentions = false })

	r := buildPageResolver("any-key")
	if _, ok := r.(utils.NoPageResolver); !ok {
		t.Errorf("buildPageResolver with flag off = %T, want utils.NoPageResolver", r)
	}
}

// TestBuildPageResolver_On verifies buildPageResolver returns a
// *utils.CachingPageResolver when the flag is set. We only assert on
// the concrete type — the construction path is exercised end-to-end by
// the TestBlocksList_ResolveMentions_* tests above.
func TestBuildPageResolver_On(t *testing.T) {
	globalResolveMentions = true
	t.Cleanup(func() { globalResolveMentions = false })

	r := buildPageResolver("any-key")
	if _, ok := r.(*utils.CachingPageResolver); !ok {
		t.Errorf("buildPageResolver with flag on = %T, want *utils.CachingPageResolver", r)
	}
}
