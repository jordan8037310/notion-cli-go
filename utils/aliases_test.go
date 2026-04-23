// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore returns an AliasStore rooted in t.TempDir() so each test
// gets an isolated filesystem path. No HOME mutation is required because
// the store is given an explicit Path.
func newTestStore(t *testing.T) *AliasStore {
	t.Helper()
	dir := t.TempDir()
	return &AliasStore{Path: filepath.Join(dir, "pages.yaml")}
}

// TestAliasStore_LoadMissingReturnsEmpty locks the documented contract:
// a missing file is not an error. Load returns an empty map so the list
// command can render an empty state without a specialised error branch.
func TestAliasStore_LoadMissingReturnsEmpty(t *testing.T) {
	s := newTestStore(t)
	m, err := s.Load()
	if err != nil {
		t.Fatalf("Load missing: unexpected err: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("Load missing: len=%d want 0", len(m))
	}
}

// TestAliasStore_LoadParses exercises the inline YAML reader against a
// file touching every documented parser branch: blank lines, comments,
// quoted values, and a variety of valid id shapes.
func TestAliasStore_LoadParses(t *testing.T) {
	s := newTestStore(t)
	body := "" +
		"# a comment\n" +
		"\n" +
		"work: 11111111111111111111111111111111\n" +
		"journal: \"22222222-2222-2222-2222-222222222222\"\n" +
		"  spaced  :   33333333333333333333333333333333  \n"
	if err := os.WriteFile(s.Path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{
		"work":    "11111111111111111111111111111111",
		"journal": "22222222-2222-2222-2222-222222222222",
		"spaced":  "33333333333333333333333333333333",
	}
	if len(m) != len(want) {
		t.Fatalf("len=%d want %d; got=%v", len(m), len(want), m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("m[%q]=%q want %q", k, m[k], v)
		}
	}
}

// TestAliasStore_LoadRejectsMalformed guards the error message that
// points the user at the offending line number. The "no colon" branch is
// the only structural error the flat-YAML reader can detect; malformed
// values (wrong-shape ids) are caller business.
func TestAliasStore_LoadRejectsMalformed(t *testing.T) {
	s := newTestStore(t)
	if err := os.WriteFile(s.Path, []byte("noseparator\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := s.Load()
	if err == nil {
		t.Fatal("Load: expected error for malformed line")
	}
	if !strings.Contains(err.Error(), "key: value") {
		t.Errorf("Load error does not cite expected shape: %v", err)
	}
}

// TestAliasStore_ResolvePassesThroughIDs asserts that raw Notion ids are
// returned verbatim without touching the store. This is the hottest path
// (every invocation with a literal id hits it) so it must be side-effect
// free.
func TestAliasStore_ResolvePassesThroughIDs(t *testing.T) {
	s := &AliasStore{Path: "/does/not/exist/pages.yaml"}
	cases := []string{
		"11111111111111111111111111111111",
		"22222222-2222-2222-2222-222222222222",
		"aaaaaaaaBBBBBBBBccccccccDDDDDDDD",
	}
	for _, in := range cases {
		got, err := s.Resolve(in)
		if err != nil {
			t.Errorf("Resolve(%q): err=%v", in, err)
			continue
		}
		if got != in {
			t.Errorf("Resolve(%q)=%q want passthrough", in, got)
		}
	}
}

// TestAliasStore_ResolveLookup covers the happy path: a named alias
// resolves to the stored id. The error branch for "alias not found" is
// covered in its own test so each assertion stays tight.
func TestAliasStore_ResolveLookup(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set("work", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Resolve("work")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "11111111111111111111111111111111" {
		t.Errorf("Resolve=%q", got)
	}
}

// TestAliasStore_ResolveMissingAlias locks the error contract so a typo'd
// alias surfaces a message that tells the user how to fix it.
func TestAliasStore_ResolveMissingAlias(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Resolve("nope")
	if err == nil {
		t.Fatal("expected error for missing alias")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error does not cite alias name: %v", err)
	}
	if !strings.Contains(err.Error(), "add-alias") {
		t.Errorf("error does not hint at add-alias command: %v", err)
	}
}

// TestAliasStore_ResolveEmpty asserts the up-front rejection of empty
// input so callers do not have to pre-validate.
func TestAliasStore_ResolveEmpty(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Resolve(""); err == nil {
		t.Fatal("expected error for empty input")
	}
}

// TestAliasStore_SetRoundTrip exercises the write/read cycle: Set
// creates the file, Load returns the written entry, and Set again
// overwrites the prior value rather than appending a duplicate.
func TestAliasStore_SetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set("work", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("journal", "22222222-2222-2222-2222-222222222222"); err != nil {
		t.Fatalf("Set journal: %v", err)
	}
	// Overwrite work with a new id.
	if err := s.Set("work", "33333333333333333333333333333333"); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	m, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("len=%d want 2; got=%v", len(m), m)
	}
	if m["work"] != "33333333333333333333333333333333" {
		t.Errorf("work=%q want overwritten id", m["work"])
	}
	if m["journal"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("journal=%q", m["journal"])
	}
}

// TestAliasStore_SetRejectsEmpty covers the two argument-shape errors
// Set rejects up front.
func TestAliasStore_SetRejectsEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set("", "id"); err == nil {
		t.Error("Set(empty name): expected error")
	}
	if err := s.Set("name", ""); err == nil {
		t.Error("Set(empty id): expected error")
	}
}

// TestAliasStore_SetCreatesParent verifies that Set creates any missing
// parent directory. Users running the CLI for the first time will not
// have ~/.config/notioncli pre-created.
func TestAliasStore_SetCreatesParent(t *testing.T) {
	dir := t.TempDir()
	// Deliberately nest two levels deep; neither exists yet.
	path := filepath.Join(dir, "nested", "more", "pages.yaml")
	s := &AliasStore{Path: path}
	if err := s.Set("x", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat after Set: %v", err)
	}
}

// TestDefaultAliasStore confirms the path assembly lands under the
// expected ~/.config/notioncli/pages.yaml location. We set HOME so the
// assertion is deterministic regardless of the developer's real home.
func TestDefaultAliasStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, err := DefaultAliasStore()
	if err != nil {
		t.Fatalf("DefaultAliasStore: %v", err)
	}
	want := filepath.Join(home, ".config", "notioncli", "pages.yaml")
	if s.Path != want {
		t.Errorf("Path=%q want %q", s.Path, want)
	}
}

// TestIsNotionID covers the uuid-shape detector. Both the dashed and
// undashed forms must match; anything else must not.
func TestIsNotionID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"11111111111111111111111111111111", true},
		{"22222222-2222-2222-2222-222222222222", true},
		{"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", true},
		{"", false},
		{"work-notes", false},
		{"too-short", false},
		{"111111111111111111111111111111111", false}, // 33 chars
		{"zz111111111111111111111111111111", false},  // non-hex
	}
	for _, tc := range cases {
		if got := IsNotionID(tc.in); got != tc.want {
			t.Errorf("IsNotionID(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

// TestAliasStore_NilReceiver guards Load against a nil receiver so a
// caller that forgot to construct a store gets an empty map rather than
// a panic. Matches the "missing file = empty map" ergonomics.
func TestAliasStore_NilReceiver(t *testing.T) {
	var s *AliasStore
	m, err := s.Load()
	if err != nil {
		t.Fatalf("nil Load: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("nil Load len=%d want 0", len(m))
	}
	if err := s.Set("x", "y"); err == nil {
		t.Error("nil Set: expected error")
	}
}
