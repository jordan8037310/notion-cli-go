// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"notioncli/utils"
)

// TestSlugify_CannotEscapeTheOutputDirectory is the security case. Page
// titles are remote data used to build filesystem paths, so a title that
// looks like a path must not become one. The allowlist means no output can
// contain a separator or be a dot segment, whatever the title says.
func TestSlugify_CannotEscapeTheOutputDirectory(t *testing.T) {
	hostile := []string{
		"../../.ssh/authorized_keys",
		"..",
		".",
		"/etc/passwd",
		`..\..\windows\system32`,
		"....//....//etc",
		"a/b/c",
		"~/.bashrc",
		"page\x00name",
	}
	for _, title := range hostile {
		t.Run(title, func(t *testing.T) {
			got := slugify(title, "abcdef1234567890")
			if got == "" {
				t.Fatal("slugify returned an empty segment; a file would be written with no name")
			}
			if strings.ContainsAny(got, `/\`) {
				t.Errorf("slugify(%q) = %q, which contains a path separator", title, got)
			}
			if strings.Contains(got, "..") {
				t.Errorf("slugify(%q) = %q, which contains a dot segment", title, got)
			}
			if got != filepath.Base(got) {
				t.Errorf("slugify(%q) = %q, which is not a single path component", title, got)
			}
			// The decisive check: joining the result must stay inside the
			// directory it was joined to.
			joined := filepath.Clean(filepath.Join("/export", got))
			if !strings.HasPrefix(joined, "/export/") {
				t.Errorf("slugify(%q) = %q escapes to %q", title, got, joined)
			}
		})
	}
}

// TestSlugify_ReadableNames confirms the allowlist has not made ordinary
// titles unusable.
func TestSlugify_ReadableNames(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"Quarterly Plan", "quarterly-plan"},
		{"Q3 2026 — Roadmap!", "q3-2026-roadmap"},
		{"  spaced  out  ", "spaced-out"},
		{"UPPER Case", "upper-case"},
	} {
		if got := slugify(tc.title, "id0000000000"); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
	// A title with nothing to keep falls back to the id, so the file can
	// still be traced to its page.
	if got := slugify("!!! ???", "abcdef1234567890"); got != "page-abcdef12" {
		t.Errorf("punctuation-only title = %q, want an id-derived name", got)
	}
	if got := slugify("", "abcdef1234567890"); got != "page-abcdef12" {
		t.Errorf("empty title = %q, want an id-derived name", got)
	}
}

// TestSlugify_TruncatesLongTitles keeps a single path component within
// what filesystems accept.
func TestSlugify_TruncatesLongTitles(t *testing.T) {
	got := slugify(strings.Repeat("word ", 60), "id0000000000")
	if len(got) > 80 {
		t.Errorf("slugify produced a %d-byte component, want <= 80", len(got))
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("slugify(%q) ends in a dash after truncation", got)
	}
}

// TestUniqueName_DisambiguatesSiblings covers what Notion allows and a
// filesystem does not: two sub-pages with the same title.
func TestUniqueName_DisambiguatesSiblings(t *testing.T) {
	used := map[string]bool{}
	got := []string{
		uniqueName("notes", used),
		uniqueName("notes", used),
		uniqueName("notes", used),
	}
	want := []string{"notes", "notes-2", "notes-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("uniqueName call %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

// TestWriteMarkdownTree_MirrorsTheHierarchy checks the directory layout: a
// page with children becomes a directory holding its own index.md.
func TestWriteMarkdownTree_MirrorsTheHierarchy(t *testing.T) {
	page := &utils.Page{ID: "x"}
	root := &utils.PageNode{
		ID: "root", Title: "Root Page", Page: page, Markdown: "root body\n",
		Children: []*utils.PageNode{
			{ID: "a", Title: "Alpha", Page: page, Markdown: "alpha body\n",
				Children: []*utils.PageNode{
					{ID: "d", Title: "Deep", Page: page, Markdown: "deep body\n"},
				}},
			{ID: "b", Title: "Beta", Page: page, Markdown: "beta body\n"},
		},
	}
	dir := t.TempDir()
	written, err := writeMarkdownTree(root, dir)
	if err != nil {
		t.Fatalf("writeMarkdownTree: %v", err)
	}
	if written != 4 {
		t.Errorf("wrote %d files, want 4", written)
	}
	for path, want := range map[string]string{
		"index.md":       "root body\n",
		"alpha/index.md": "alpha body\n",
		"alpha/deep.md":  "deep body\n",
		"beta.md":        "beta body\n",
	} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

// TestWriteMarkdownTree_ChildTitledIndexDoesNotClobberParent covers the
// collision the reserved name exists for: a sub-page titled "Index" would
// otherwise overwrite its parent's own body.
func TestWriteMarkdownTree_ChildTitledIndexDoesNotClobberParent(t *testing.T) {
	page := &utils.Page{ID: "x"}
	root := &utils.PageNode{
		ID: "root", Title: "Root", Page: page, Markdown: "PARENT BODY\n",
		Children: []*utils.PageNode{
			{ID: "c", Title: "Index", Page: page, Markdown: "CHILD BODY\n"},
		},
	}
	dir := t.TempDir()
	if _, err := writeMarkdownTree(root, dir); err != nil {
		t.Fatalf("writeMarkdownTree: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("read index.md: %v", err)
	}
	if string(got) != "PARENT BODY\n" {
		t.Errorf("index.md = %q, want the parent's body — a child titled \"Index\" overwrote it", got)
	}
	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "index-2.md,index.md" {
		t.Errorf("directory = %v, want the child written alongside as index-2.md", names)
	}
}

// TestWriteMarkdownTree_SkipsUnfetchedPages keeps an unreadable page from
// leaving a plausible-looking empty file in a backup.
func TestWriteMarkdownTree_SkipsUnfetchedPages(t *testing.T) {
	root := &utils.PageNode{
		ID: "root", Title: "Root", Page: &utils.Page{ID: "x"}, Markdown: "body\n",
		Children: []*utils.PageNode{
			{ID: "gone", Title: "Forbidden", Err: "restricted_resource"},
		},
	}
	dir := t.TempDir()
	written, err := writeMarkdownTree(root, dir)
	if err != nil {
		t.Fatalf("writeMarkdownTree: %v", err)
	}
	if written != 1 {
		t.Errorf("wrote %d files, want 1 — the unreadable page must not produce one", written)
	}
	if _, err := os.Stat(filepath.Join(dir, "forbidden.md")); !os.IsNotExist(err) {
		t.Error("an empty file was written for a page that could not be read")
	}
}

// --- command dispatch -------------------------------------------------------

// TestPagesExport_RejectsBadFormat guards the enum rather than silently
// falling back to json.
func TestPagesExport_RejectsBadFormat(t *testing.T) {
	_, out, _, err := runPages(t, "export", "pg1", "--format", "yaml")
	if err == nil {
		t.Fatalf("Execute accepted an invalid --format (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "want json|md|tree") {
		t.Errorf("error = %q, want it to name the accepted formats", err)
	}
}

// TestPagesExport_MarkdownRequiresOut refuses to guess a destination for
// files it is about to write.
func TestPagesExport_MarkdownRequiresOut(t *testing.T) {
	_, out, _, err := runPages(t, "export", "pg1", "--format", "md")
	if err == nil {
		t.Fatalf("Execute accepted --format md with no --out (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "--out") {
		t.Errorf("error = %q, want it to name --out", err)
	}
}

// TestPagesExport_OutRejectedForStdoutFormats catches a flag that would
// otherwise be silently ignored, leaving the user waiting for files that
// never appear.
func TestPagesExport_OutRejectedForStdoutFormats(t *testing.T) {
	for _, format := range []string{"json", "tree"} {
		t.Run(format, func(t *testing.T) {
			_, out, _, err := runPages(t, "export", "pg1", "--format", format, "--out", t.TempDir())
			if err == nil {
				t.Fatalf("Execute accepted --out with --format %s (out=%s)", format, out)
			}
			if !strings.Contains(err.Error(), "--out only applies") {
				t.Errorf("error = %q, want it to explain --out does not apply", err)
			}
		})
	}
}

// TestPagesExport_TreeFormat prints the outline and writes nothing.
func TestPagesExport_TreeFormat(t *testing.T) {
	d, out, _, err := runPages(t, "export", "pg1", "--format", "tree", "--depth", "0")
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if !strings.Contains(out, "pg1") {
		t.Errorf("output = %q, want the page id in the outline", out)
	}
	if got := d.count("GET /pages/pg1/markdown"); got != 0 {
		t.Errorf("tree format rendered markdown %d times; it should not need any", got)
	}
}

// TestPagesExport_JSONFormat emits one document carrying the page.
func TestPagesExport_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"pages", "export", "pg1", "--depth", "0"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, buf.String())
	}
	var node map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &node); err != nil {
		t.Fatalf("stdout is not one JSON document (%v): %s", err, buf.String())
	}
	if node["id"] != "pg1" {
		t.Errorf("id = %v, want pg1", node["id"])
	}
	if _, ok := node["page"]; !ok {
		t.Errorf("document = %v, want the page object included", node)
	}
	_ = d
}
