// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"notioncli/utils"
)

// aliasTestEnv wires up an alias store rooted in t.TempDir and installs
// it as the aliasStoreOverride so the pages alias subcommands write to
// the temp location instead of the user's real ~/.config. The returned
// store is the same one resolvePageID will consult during the test.
func aliasTestEnv(t *testing.T) *utils.AliasStore {
	t.Helper()
	dir := t.TempDir()
	store := &utils.AliasStore{Path: filepath.Join(dir, "pages.yaml")}
	prior := aliasStoreOverride
	aliasStoreOverride = store
	t.Cleanup(func() { aliasStoreOverride = prior })
	return store
}

// TestPages_AddAlias_Happy drives `pages add-alias <name> <id>` end-to-
// end and verifies the store picked up the new entry.
func TestPages_AddAlias_Happy(t *testing.T) {
	store := aliasTestEnv(t)

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "add-alias", "work", "11111111111111111111111111111111"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m["work"] != "11111111111111111111111111111111" {
		t.Errorf("store[work]=%q want uuid", m["work"])
	}
}

// TestPages_AddAlias_JSON asserts the --json envelope shape so scripts
// can rely on {"ok":true,"action":"add-alias","name":"...","id":"..."}.
func TestPages_AddAlias_JSON(t *testing.T) {
	aliasTestEnv(t)

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "add-alias", "j", "22222222-2222-2222-2222-222222222222", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var env map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("json decode: %v; raw=%q", err, out.String())
	}
	if env["ok"] != true {
		t.Errorf("ok=%v want true", env["ok"])
	}
	if env["action"] != "add-alias" {
		t.Errorf("action=%v", env["action"])
	}
	if env["name"] != "j" {
		t.Errorf("name=%v", env["name"])
	}
	if env["id"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("id=%v", env["id"])
	}
}

// TestPages_AddAlias_RejectsBadID verifies the uuid-shape guard so a
// typo like "work-notes" as the id bounces with a descriptive error
// before touching the filesystem.
func TestPages_AddAlias_RejectsBadID(t *testing.T) {
	aliasTestEnv(t)

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "add-alias", "work", "not-a-uuid"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for malformed id")
	}
	if !strings.Contains(err.Error(), "not a valid Notion page id") {
		t.Errorf("error missing hint: %v", err)
	}
}

// TestPages_ListAliases_Empty covers the "no aliases yet" branch — the
// command must succeed with a yellow notice rather than erroring out.
func TestPages_ListAliases_Empty(t *testing.T) {
	aliasTestEnv(t)

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "list-aliases"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "No aliases configured") {
		t.Errorf("empty-state message missing: %q", out.String())
	}
}

// TestPages_ListAliases_Populated seeds the store, runs list-aliases,
// and asserts both entries appear in the tabular output in stable
// (alphabetical) order.
func TestPages_ListAliases_Populated(t *testing.T) {
	store := aliasTestEnv(t)
	if err := store.Set("work", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.Set("journal", "22222222222222222222222222222222"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "list-aliases"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "ID") {
		t.Errorf("table header missing: %q", got)
	}
	ji := strings.Index(got, "journal")
	wi := strings.Index(got, "work")
	if ji == -1 || wi == -1 {
		t.Fatalf("entries missing: %q", got)
	}
	if ji > wi {
		t.Errorf("alphabetical order expected (journal before work): %q", got)
	}
}

// TestPages_ListAliases_JSON_LineCount is the regression test for the
// reviewer's --json double-emit finding: with N aliases the command must
// emit exactly N lines on stdout, no header, no trailing envelope, no
// spillover from the human-output path. Three aliases is the minimum
// cardinality that would surface "one extra" or "one too many" bugs in
// either direction (empty trailing newline, double-encode, stray banner).
// Both stdout and stderr are piped into the same buffer so a spurious
// JSON error record from jsonErrorOr would push the count to 4 and fail
// this assertion.
func TestPages_ListAliases_JSON_LineCount(t *testing.T) {
	store := aliasTestEnv(t)
	seeds := []struct{ name, id string }{
		{"alpha", "11111111111111111111111111111111"},
		{"beta", "22222222222222222222222222222222"},
		{"gamma", "33333333333333333333333333333333"},
	}
	for _, s := range seeds {
		if err := store.Set(s.name, s.id); err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "list-aliases", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Use the raw byte count of newlines rather than strings.Split after
	// TrimSpace: we want to count exactly N '\n' terminators. Any extra
	// line (even a blank one) would inflate the count and fail.
	raw := out.String()
	got := strings.Count(raw, "\n")
	if got != len(seeds) {
		t.Fatalf("newline count=%d want %d; raw=%q", got, len(seeds), raw)
	}
	// Every line must be a decodable JSON object with the expected shape.
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) != len(seeds) {
		t.Fatalf("split lines=%d want %d; raw=%q", len(lines), len(seeds), raw)
	}
	for i, line := range lines {
		var rec struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: decode %q: %v", i, line, err)
		}
		if rec.Name != seeds[i].name || rec.ID != seeds[i].id {
			t.Errorf("line %d: got {%q,%q} want {%q,%q}", i, rec.Name, rec.ID, seeds[i].name, seeds[i].id)
		}
	}
}

// TestPages_ListAliases_JSON asserts the --json NDJSON shape: one
// {"name":..,"id":..} object per line, in alphabetical order.
func TestPages_ListAliases_JSON(t *testing.T) {
	store := aliasTestEnv(t)
	if err := store.Set("z", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.Set("a", "22222222222222222222222222222222"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "list-aliases", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d want 2; got=%q", len(lines), out.String())
	}
	var first, second struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 0: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line 1: %v", err)
	}
	if first.Name != "a" || second.Name != "z" {
		t.Errorf("order wrong: %q, %q", first.Name, second.Name)
	}
}
