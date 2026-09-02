// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMD(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write doc.md: %v", err)
	}
	return path
}

// runPages drives a pages subcommand against the dispatch mock, keeping
// stdout and stderr separate so warnings can be asserted on the right
// stream.
func runPages(t *testing.T, args ...string) (*pagesDispatchServer, string, string, error) {
	t.Helper()
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(append([]string{"pages"}, args...))
	err := rootCmd.Execute()
	return d, out.String(), errBuf.String(), err
}

// TestCreateFromMarkdown_PromotesLeadingH1 is the whole point of the
// leading-heading rule. Notion silently drops a leading H1 on create and
// does not use it as the title either, so a plain markdown file loses its
// first line. The CLI promotes it instead.
func TestCreateFromMarkdown_PromotesLeadingH1(t *testing.T) {
	path := writeMD(t, "# Quarterly Plan\n\nSome body text.\n")
	d, out, _, err := runPages(t, "create", "--parent", "p1", "--from-markdown", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("POST /pages"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["markdown"] != "Some body text.\n" {
		t.Errorf("markdown = %q, want the leading H1 removed from the body", body["markdown"])
	}
	props, _ := body["properties"].(map[string]interface{})
	title, _ := props["title"].(map[string]interface{})
	rich, _ := title["title"].([]interface{})
	if len(rich) == 0 {
		t.Fatalf("no title property sent; the H1 was dropped, which is the bug this guards: %v", props)
	}
	first, _ := rich[0].(map[string]interface{})
	text, _ := first["text"].(map[string]interface{})
	if text["content"] != "Quarterly Plan" {
		t.Errorf("title = %v, want the promoted heading", text["content"])
	}
}

// TestCreateFromMarkdown_ExplicitTitleWinsAndWarns covers the ambiguous
// case. Two titles were supplied; the flag wins because it is the more
// explicit of the two, and the user is told the heading will not survive
// rather than left to discover it in the page.
func TestCreateFromMarkdown_ExplicitTitleWinsAndWarns(t *testing.T) {
	path := writeMD(t, "# From The File\n\nbody\n")
	d, out, errOut, err := runPages(t, "create", "--parent", "p1", "--title", "From The Flag", "--from-markdown", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if !strings.Contains(errOut, "warning") || !strings.Contains(errOut, "From The File") {
		t.Errorf("stderr = %q, want a warning naming the dropped heading", errOut)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("POST /pages"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	props, _ := body["properties"].(map[string]interface{})
	title, _ := props["title"].(map[string]interface{})
	rich, _ := title["title"].([]interface{})
	first, _ := rich[0].(map[string]interface{})
	text, _ := first["text"].(map[string]interface{})
	if text["content"] != "From The Flag" {
		t.Errorf("title = %v, want the explicit --title to win", text["content"])
	}
}

// TestCreateFromMarkdown_NoLeadingH1IsSentVerbatim confirms the rule only
// fires when it should — a document that does not open with an H1 is not
// rewritten at all.
func TestCreateFromMarkdown_NoLeadingH1IsSentVerbatim(t *testing.T) {
	const doc = "intro paragraph\n\n# A Later Heading\n\nmore\n"
	path := writeMD(t, doc)
	d, out, errOut, err := runPages(t, "create", "--parent", "p1", "--title", "T", "--from-markdown", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if strings.Contains(errOut, "warning") {
		t.Errorf("stderr = %q, want no warning when nothing is dropped", errOut)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("POST /pages"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["markdown"] != doc {
		t.Errorf("markdown = %q, want the document untouched", body["markdown"])
	}
}

// TestCreateRejectsCompetingBodySources keeps one body source from
// silently discarding another's file.
func TestCreateRejectsCompetingBodySources(t *testing.T) {
	md := writeMD(t, "# T\n\nbody\n")
	txt := writeMD(t, "plain line\n")
	_, out, _, err := runPages(t, "create", "--parent", "p1", "--from-markdown", md, "--from-text", txt)
	if err == nil {
		t.Fatalf("Execute accepted two body sources (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "pass only one") {
		t.Errorf("error = %q, want it to say only one body source is allowed", err)
	}
}

// TestCreateFromMarkdown_RejectsEmptyFile prefers an error to creating a
// blank page the caller did not intend.
func TestCreateFromMarkdown_RejectsEmptyFile(t *testing.T) {
	path := writeMD(t, "   \n\n")
	_, out, _, err := runPages(t, "create", "--parent", "p1", "--from-markdown", path)
	if err == nil {
		t.Fatalf("Execute accepted an empty markdown file (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Errorf("error = %q, want it to say the file is empty", err)
	}
}

// TestAppendMarkdown_Dispatch drives the append command end to end.
func TestAppendMarkdown_Dispatch(t *testing.T) {
	path := writeMD(t, "appended text\n")
	d, out, _, err := runPages(t, "append-markdown", "pg1", "--from", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if got := d.count("PATCH /pages/pg1/markdown"); got != 1 {
		t.Fatalf("PATCH /pages/pg1/markdown count = %d, want 1 (calls=%v)", got, d.calls)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("PATCH /pages/pg1/markdown"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["type"] != "insert_content" {
		t.Errorf("type = %v, want insert_content", body["type"])
	}
	ic, _ := body["insert_content"].(map[string]interface{})
	pos, _ := ic["position"].(map[string]interface{})
	if pos["type"] != "end" {
		t.Errorf("position = %v, want end by default", ic["position"])
	}
}

// TestAppendMarkdown_Prepend covers the one flag that changes where the
// content lands.
func TestAppendMarkdown_Prepend(t *testing.T) {
	path := writeMD(t, "top text\n")
	d, out, _, err := runPages(t, "append-markdown", "pg1", "--from", path, "--prepend")
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("PATCH /pages/pg1/markdown"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	ic, _ := body["insert_content"].(map[string]interface{})
	pos, _ := ic["position"].(map[string]interface{})
	if pos["type"] != "start" {
		t.Errorf("position = %v, want start under --prepend", ic["position"])
	}
}

// TestReplaceMarkdown_Dispatch drives the destructive command and checks
// it never sends allow_deleting_content — a body replace must not be able
// to take a page's sub-pages with it.
func TestReplaceMarkdown_Dispatch(t *testing.T) {
	path := writeMD(t, "# Fresh\n\nnew body\n")
	d, out, _, err := runPages(t, "replace-markdown", "pg1", "--from", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("PATCH /pages/pg1/markdown"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["type"] != "replace_content" {
		t.Errorf("type = %v, want replace_content", body["type"])
	}
	rc, _ := body["replace_content"].(map[string]interface{})
	if rc["new_str"] != "# Fresh\n\nnew body\n" {
		t.Errorf("new_str = %q, want the file verbatim — replace keeps its leading H1", rc["new_str"])
	}
	if _, present := body["allow_deleting_content"]; present {
		t.Error("allow_deleting_content was sent; child pages must survive a body replace")
	}
}

// TestMarkdownWriteCommands_RequireFrom keeps a forgotten --from from
// looking like a successful no-op.
func TestMarkdownWriteCommands_RequireFrom(t *testing.T) {
	for _, sub := range []string{"append-markdown", "replace-markdown"} {
		t.Run(sub, func(t *testing.T) {
			d, out, _, err := runPages(t, sub, "pg1")
			if err == nil {
				t.Fatalf("%s succeeded with no --from (out=%s)", sub, out)
			}
			if !strings.Contains(err.Error(), "--from is required") {
				t.Errorf("error = %q, want it to name --from", err)
			}
			if got := d.count("PATCH /pages/pg1/markdown"); got != 0 {
				t.Errorf("made %d requests despite the missing flag", got)
			}
		})
	}
}

// TestReplaceMarkdown_JSONMode emits the page_markdown object rather than
// the human summary, so the result stays pipeable.
func TestReplaceMarkdown_JSONMode(t *testing.T) {
	path := writeMD(t, "body\n")
	_, out, _, err := runPages(t, "replace-markdown", "pg1", "--from", path, "--json")
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("stdout is not a single JSON object (%v): %s", err, out)
	}
	if got["markdown"] != "# Rendered\n\nby notion\n" {
		t.Errorf("markdown = %v, want the re-rendered page echoed back", got["markdown"])
	}
	if got["object"] != "page_markdown" {
		t.Errorf("object = %v, want page_markdown", got["object"])
	}
}
