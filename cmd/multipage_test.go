// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"notioncli/utils"
)

// TestResolvePageID_Precedence locks the documented resolution order:
// --page > NOTION_PAGE_ID > error. All three branches live in one test
// so a future refactor has to confirm each edge in the same place.
func TestResolvePageID_Precedence(t *testing.T) {
	t.Run("flag_wins_over_env", func(t *testing.T) {
		t.Setenv("NOTION_PAGE_ID", "env-page")
		defer func() { globalPage = "" }()
		globalPage = "11111111111111111111111111111111"
		got, err := resolvePageID()
		if err != nil {
			t.Fatalf("resolvePageID: %v", err)
		}
		if got != "11111111111111111111111111111111" {
			t.Errorf("got=%q, want uuid (flag beats env)", got)
		}
	})

	t.Run("alias_resolves_via_store", func(t *testing.T) {
		t.Setenv("NOTION_PAGE_ID", "env-page")
		store := aliasTestEnv(t)
		if err := store.Set("work", "22222222222222222222222222222222"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		defer func() { globalPage = "" }()
		globalPage = "work"
		got, err := resolvePageID()
		if err != nil {
			t.Fatalf("resolvePageID: %v", err)
		}
		if got != "22222222222222222222222222222222" {
			t.Errorf("alias did not resolve: got=%q", got)
		}
	})

	t.Run("env_used_when_flag_absent", func(t *testing.T) {
		t.Setenv("NOTION_PAGE_ID", "env-page")
		globalPage = ""
		got, err := resolvePageID()
		if err != nil {
			t.Fatalf("resolvePageID: %v", err)
		}
		if got != "env-page" {
			t.Errorf("env fallback not used: got=%q", got)
		}
	})

	t.Run("error_when_neither_set", func(t *testing.T) {
		t.Setenv("NOTION_PAGE_ID", "")
		globalPage = ""
		_, err := resolvePageID()
		if err == nil {
			t.Fatal("expected error when neither --page nor NOTION_PAGE_ID is set")
		}
		if !strings.Contains(err.Error(), "no target page") {
			t.Errorf("unexpected error text: %v", err)
		}
	})

	t.Run("unknown_alias_errors", func(t *testing.T) {
		aliasTestEnv(t)
		defer func() { globalPage = "" }()
		globalPage = "not-a-uuid-not-an-alias"
		_, err := resolvePageID()
		if err == nil {
			t.Fatal("expected error for unknown alias")
		}
		if !strings.Contains(err.Error(), "not-a-uuid-not-an-alias") {
			t.Errorf("error should cite the alias name: %v", err)
		}
	})
}

// TestListCmd_PageAlias drives `notioncli list --page work` against a
// mock server; it verifies that the alias resolves to the configured uuid
// and that the list fetch actually targets the aliased page's blocks
// endpoint (not the env-var pageID path).
func TestListCmd_PageAlias(t *testing.T) {
	srv := withCmdEnv(t)
	store := aliasTestEnv(t)
	if err := store.Set("work", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	var aliasHits, defaultHits int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			switch {
			case strings.Contains(r.URL.Path, "/blocks/11111111111111111111111111111111/children"):
				atomic.AddInt64(&aliasHits, 1)
			case strings.Contains(r.URL.Path, "/blocks/pageID/children"):
				atomic.AddInt64(&defaultHits, 1)
			}
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"list", "--page", "work"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if atomic.LoadInt64(&aliasHits) == 0 {
		t.Error("--page alias did not target the aliased page")
	}
	if atomic.LoadInt64(&defaultHits) != 0 {
		t.Error("--page alias unexpectedly fell through to NOTION_PAGE_ID")
	}
}

// TestBlocksListCmd_PageRawID mirrors TestListCmd_PageAlias but passes a
// raw uuid to --page, confirming the passthrough branch of resolvePageID
// reaches the blocks list path without touching the alias store.
func TestBlocksListCmd_PageRawID(t *testing.T) {
	srv := withCmdEnv(t)
	// Intentionally do NOT seed the alias store. A raw uuid must resolve
	// without any lookup.

	var aliasHits int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/11111111111111111111111111111111/children") {
			atomic.AddInt64(&aliasHits, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	blockType = ""
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "list", "--page", "11111111111111111111111111111111"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if atomic.LoadInt64(&aliasHits) == 0 {
		t.Error("raw --page uuid did not target the given page")
	}
}

// TestListCmd_EnvFallback asserts that leaving --page empty preserves the
// legacy NOTION_PAGE_ID path so existing users' workflows do not break.
func TestListCmd_EnvFallback(t *testing.T) {
	srv := withCmdEnv(t)
	// Clear any override a prior test may have left; the env var set by
	// withCmdEnv is the only page source for this test.
	aliasStoreOverride = nil

	var defaultHits int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&defaultHits, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if atomic.LoadInt64(&defaultHits) == 0 {
		t.Error("NOTION_PAGE_ID fallback did not target pageID")
	}
}

// TestPagesAliasSubcommands_Registered confirms the two new alias
// subcommands are wired into the pages command tree. This is a
// lightweight guard — the behavioural tests live elsewhere — so a
// regression that fails to register either command fails loudly here
// rather than at end-to-end invocation time.
func TestPagesAliasSubcommands_Registered(t *testing.T) {
	pagesC := findTopLevelCmd(t, "pages")
	want := map[string]bool{"add-alias": false, "list-aliases": false}
	for _, c := range pagesC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("pages subcommand %q not registered", name)
		}
	}

	// Sanity: the in-cmd helper aliasStore() should not panic when the
	// override is nil. We can't actually call DefaultAliasStore() without
	// HOME set — utils.DefaultAliasStore does its own UserHomeDir lookup
	// — so we just confirm the override shortcut returns the seeded store.
	store := &utils.AliasStore{Path: "/tmp/never-written"}
	prior := aliasStoreOverride
	aliasStoreOverride = store
	defer func() { aliasStoreOverride = prior }()
	got, err := aliasStore()
	if err != nil {
		t.Fatalf("aliasStore: %v", err)
	}
	if got != store {
		t.Error("aliasStore did not return the installed override")
	}
}
