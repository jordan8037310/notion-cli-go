// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore returns an AliasStore rooted in t.TempDir() so each test
// gets an isolated filesystem path. No HOME mutation is required because
// the store is given an explicit Path. Accepts testing.TB so benchmarks
// and subtests can share the same scaffold.
func newTestStore(tb testing.TB) *AliasStore {
	tb.Helper()
	dir := tb.TempDir()
	return &AliasStore{Path: filepath.Join(dir, "pages.yaml")}
}

// seedLoadMissingStore is the shared setup for the "missing file = empty
// map" contract. Kept distinct from newTestStore only to document intent
// at call sites — both the behavioural test and the gap-gate alias test
// use this helper instead of reaching into each other's frames.
func seedLoadMissingStore(tb testing.TB) *AliasStore {
	tb.Helper()
	return newTestStore(tb)
}

// seedResolveLookupStore is the shared setup for the "alias resolves to
// stored id" contract. It builds a fresh store and writes a single known
// entry. Used by both the behavioural test and the gap-gate alias test
// so neither reaches into the other's frame.
func seedResolveLookupStore(tb testing.TB) (*AliasStore, string, string) {
	tb.Helper()
	const name = "work"
	const id = "11111111111111111111111111111111"
	s := newTestStore(tb)
	if err := s.Set(name, id); err != nil {
		tb.Fatalf("seed Set: %v", err)
	}
	return s, name, id
}

// seedSetRoundTripStore performs the round-trip mutations used by both
// TestAliasStore_SetRoundTrip and TestSet. Returning the store lets each
// caller assert the observable state without duplicating the mutation
// sequence.
func seedSetRoundTripStore(tb testing.TB) *AliasStore {
	tb.Helper()
	s := newTestStore(tb)
	if err := s.Set("work", "11111111111111111111111111111111"); err != nil {
		tb.Fatalf("Set work: %v", err)
	}
	if err := s.Set("journal", "22222222-2222-2222-2222-222222222222"); err != nil {
		tb.Fatalf("Set journal: %v", err)
	}
	// Overwrite work with a new id to prove Set upserts rather than
	// appending a duplicate entry.
	if err := s.Set("work", "33333333333333333333333333333333"); err != nil {
		tb.Fatalf("Set overwrite: %v", err)
	}
	return s
}

// assertSetRoundTripState is the shared post-condition check for the
// seedSetRoundTripStore scaffold. Keeps both TestAliasStore_SetRoundTrip
// and the gap-gate alias TestSet asserting the same invariant without a
// test-to-test call.
func assertSetRoundTripState(tb testing.TB, s *AliasStore) {
	tb.Helper()
	m, err := s.All()
	if err != nil {
		tb.Fatalf("All: %v", err)
	}
	if len(m) != 2 {
		tb.Fatalf("len=%d want 2; got=%v", len(m), m)
	}
	if m["work"] != "33333333333333333333333333333333" {
		tb.Errorf("work=%q want overwritten id", m["work"])
	}
	if m["journal"] != "22222222-2222-2222-2222-222222222222" {
		tb.Errorf("journal=%q", m["journal"])
	}
}

// TestAliasStore_LoadMissingReturnsEmpty locks the documented contract:
// a missing file is not an error. Load returns an empty map so the list
// command can render an empty state without a specialised error branch.
func TestAliasStore_LoadMissingReturnsEmpty(t *testing.T) {
	s := seedLoadMissingStore(t)
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

// TestAliasStore_LoadRejectsEmptyValue guards the parser contract for a
// line shaped `key:` (no value after the colon). Silently storing "" as
// the id would surface later as an opaque Notion 400 during Resolve;
// rejecting at parse time with the line number keeps the failure local
// and actionable. Symmetric with the empty-key rejection on the line
// immediately above it in the parser.
func TestAliasStore_LoadRejectsEmptyValue(t *testing.T) {
	cases := []struct {
		name string
		body string
		line int
		key  string
	}{
		{name: "bare colon", body: "work:\n", line: 1, key: "work"},
		{name: "trailing spaces", body: "journal:    \n", line: 1, key: "journal"},
		{name: "empty after valid line", body: "work: 11111111111111111111111111111111\njournal:\n", line: 2, key: "journal"},
		{name: "quoted empty", body: "work: \"\"\n", line: 1, key: "work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if err := os.WriteFile(s.Path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}
			_, err := s.Load()
			if err == nil {
				t.Fatal("Load: expected error for empty value")
			}
			msg := err.Error()
			if !strings.Contains(msg, "empty value") {
				t.Errorf("error does not mention empty value: %v", err)
			}
			if !strings.Contains(msg, fmt.Sprintf(":%d:", tc.line)) {
				t.Errorf("error does not cite line %d: %v", tc.line, err)
			}
			if !strings.Contains(msg, fmt.Sprintf("%q", tc.key)) {
				t.Errorf("error does not cite key %q: %v", tc.key, err)
			}
		})
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
	s, name, id := seedResolveLookupStore(t)
	got, err := s.Resolve(name)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != id {
		t.Errorf("Resolve=%q want %q", got, id)
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
// overwrites the prior value rather than appending a duplicate. The
// seed + assertion live in helpers so the gap-gate alias TestSet can
// replay the same scenario without calling this test directly.
func TestAliasStore_SetRoundTrip(t *testing.T) {
	s := seedSetRoundTripStore(t)
	assertSetRoundTripState(t, s)
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

// TestLoad is the method-name-matching alias for the gap-gate checker.
// The full parser coverage lives in TestAliasStore_LoadParses and its
// siblings; this thin wrapper replays the missing-file contract through
// the shared scaffold so there is no test-to-test call. See
// scripts/check-test-coverage.sh for the name-match rule.
func TestLoad(t *testing.T) {
	s := seedLoadMissingStore(t)
	m, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("Load missing: len=%d want 0", len(m))
	}
}

// TestAll — gap-gate alias for the AliasStore.All method. Behavioural
// coverage lives in TestAliasStore_SetRoundTrip; this version round-trips
// a single entry through All() directly to satisfy the method-name rule.
func TestAll(t *testing.T) {
	s := newTestStore(t)
	if err := s.Set("k", "11111111111111111111111111111111"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	m, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if m["k"] != "11111111111111111111111111111111" {
		t.Errorf("All missing entry: %v", m)
	}
}

// TestResolve — gap-gate alias. Replays the shared lookup scaffold so
// Resolve has its own named test without calling another Test function.
// Detailed behaviour still lives in TestAliasStore_ResolvePassesThroughIDs,
// TestAliasStore_ResolveLookup, TestAliasStore_ResolveMissingAlias, and
// TestAliasStore_ResolveEmpty.
func TestResolve(t *testing.T) {
	s, name, id := seedResolveLookupStore(t)
	got, err := s.Resolve(name)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != id {
		t.Errorf("Resolve=%q want %q", got, id)
	}
}

// TestSet — gap-gate alias for AliasStore.Set. Replays the round-trip
// scaffold so the gap-gate has a same-named test without delegating to
// TestAliasStore_SetRoundTrip. Detailed mutation ordering is documented
// on seedSetRoundTripStore.
func TestSet(t *testing.T) {
	s := seedSetRoundTripStore(t)
	assertSetRoundTripState(t, s)
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
