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

// writeSpecFile drops a create-many input file into t.TempDir.
func writeSpecFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// runCreateMany drives the command end-to-end against the dispatch mock
// and returns combined output plus the execution error.
func runCreateMany(t *testing.T, args ...string) (*pagesDispatchServer, string, error) {
	t.Helper()
	d := withPagesEnv(t)
	resetPagesFlags()
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"pages", "create-many"}, args...))
	err := rootCmd.Execute()
	return d, out.String(), err
}

// TestPagesCreateMany_JSONArray covers the array form of --from: one POST
// per entry, and each entry's own parent honoured.
func TestPagesCreateMany_JSONArray(t *testing.T) {
	path := writeSpecFile(t, "pages.json", `[
	  {"parent": "p1", "title": "First"},
	  {"parent": "p2", "title": "Second"}
	]`)
	d, out, err := runCreateMany(t, "--from", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if got := d.count("POST /pages"); got != 2 {
		t.Errorf("POST /pages count = %d, want 2", got)
	}
	if !strings.Contains(out, "Created 2 of 2") {
		t.Errorf("output %q does not report the created count", out)
	}
}

// TestPagesCreateMany_JSONL covers the stream form. The two forms are
// distinguished by the first non-space byte, with no flag to get wrong.
func TestPagesCreateMany_JSONL(t *testing.T) {
	path := writeSpecFile(t, "pages.jsonl",
		"{\"parent\":\"p1\",\"title\":\"First\"}\n{\"parent\":\"p1\",\"title\":\"Second\"}\n{\"parent\":\"p1\",\"title\":\"Third\"}\n")
	d, out, err := runCreateMany(t, "--from", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if got := d.count("POST /pages"); got != 3 {
		t.Errorf("POST /pages count = %d, want 3 (JSONL not detected?)", got)
	}
}

// TestPagesCreateMany_FlagSuppliesDefaultParent is the common import
// shape: a file of bare titles, one --parent-database for all of them.
func TestPagesCreateMany_FlagSuppliesDefaultParent(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl",
		"{\"title\":\"A\"}\n{\"title\":\"B\"}\n")
	d, out, err := runCreateMany(t, "--from", path, "--parent-database", "db1")
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	if got := d.count("POST /pages"); got != 2 {
		t.Fatalf("POST /pages count = %d, want 2", got)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("POST /pages"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	parent, _ := body["parent"].(map[string]interface{})
	if parent["database_id"] != "db1" {
		t.Errorf("parent = %v, want the --parent-database default applied", parent)
	}
}

// TestPagesCreateMany_EntryParentBeatsFlag pins the precedence: the flag
// is a default for entries that name no parent, never an override. One
// file must be able to span several parents.
func TestPagesCreateMany_EntryParentBeatsFlag(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl", "{\"parent\":\"ownParent\",\"title\":\"A\"}\n")
	d, out, err := runCreateMany(t, "--from", path, "--parent", "flagParent")
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("POST /pages"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	parent, _ := body["parent"].(map[string]interface{})
	if parent["page_id"] != "ownParent" {
		t.Errorf("parent = %v, want the entry's own parent to win over --parent", parent)
	}
}

// TestPagesCreateMany_PropertiesAndChildrenPassThrough confirms the #40
// payload survives into the bulk body verbatim.
func TestPagesCreateMany_PropertiesAndChildrenPassThrough(t *testing.T) {
	path := writeSpecFile(t, "rows.json", `[{
	  "parent_database": "db1",
	  "title": "Row",
	  "properties": {"Stage": {"select": {"name": "Live"}}},
	  "children": [{"object":"block","type":"paragraph",
	                "paragraph":{"rich_text":[{"type":"text","text":{"content":"body"}}]}}]
	}]`)
	d, out, err := runCreateMany(t, "--from", path)
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(d.body("POST /pages"), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	props, _ := body["properties"].(map[string]interface{})
	stage, _ := props["Stage"].(map[string]interface{})
	if stage["select"] == nil {
		t.Errorf("properties = %v, want Stage passed through verbatim", props)
	}
	if children, _ := body["children"].([]interface{}); len(children) != 1 {
		t.Errorf("children = %v, want the one block", body["children"])
	}
}

// TestPagesCreateMany_RejectsBadOnError guards the enum. A typo silently
// falling back to abort (or to continue) would change how a failed import
// behaves without saying so.
func TestPagesCreateMany_RejectsBadOnError(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl", "{\"parent\":\"p1\",\"title\":\"A\"}\n")
	d, out, err := runCreateMany(t, "--from", path, "--on-error", "keep-going")
	if err == nil {
		t.Fatalf("Execute succeeded on an invalid --on-error (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "want abort|continue") {
		t.Errorf("error %q does not name the accepted values", err)
	}
	if got := d.count("POST /pages"); got != 0 {
		t.Errorf("posted %d times despite an invalid flag", got)
	}
}

// TestPagesCreateMany_RequiresFrom keeps the command from silently doing
// nothing when the input file is forgotten.
func TestPagesCreateMany_RequiresFrom(t *testing.T) {
	_, out, err := runCreateMany(t)
	if err == nil {
		t.Fatalf("Execute succeeded with no --from (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "--from is required") {
		t.Errorf("error = %q, want it to name --from", err)
	}
}

// TestPagesCreateMany_ParentlessEntryNamesTheEntry checks the parse error
// points at a line the operator can find, and that nothing is created —
// the file is rejected before the first POST.
func TestPagesCreateMany_ParentlessEntryNamesTheEntry(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl",
		"{\"parent\":\"p1\",\"title\":\"A\"}\n{\"title\":\"Orphan\"}\n")
	d, out, err := runCreateMany(t, "--from", path)
	if err == nil {
		t.Fatalf("Execute succeeded with a parentless entry (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "entry 2 (Orphan)") {
		t.Errorf("error = %q, want it to name entry 2", err)
	}
	if got := d.count("POST /pages"); got != 0 {
		t.Errorf("created %d pages before rejecting the file; a file that cannot be fully parsed must not half-import", got)
	}
}

// TestPagesCreateMany_MalformedJSONL names the offending entry rather
// than reporting a bare syntax error for the whole file.
func TestPagesCreateMany_MalformedJSONL(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl",
		"{\"parent\":\"p1\",\"title\":\"A\"}\n{not json}\n")
	_, out, err := runCreateMany(t, "--from", path)
	if err == nil {
		t.Fatalf("Execute succeeded on malformed JSONL (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "entry 2") {
		t.Errorf("error = %q, want it to name the bad entry", err)
	}
}

// TestPagesCreateMany_EmptyFile rejects rather than reporting a
// successful import of nothing.
func TestPagesCreateMany_EmptyFile(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl", "   \n")
	_, out, err := runCreateMany(t, "--from", path)
	if err == nil {
		t.Fatalf("Execute succeeded on an empty file (out=%s)", out)
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Errorf("error = %q, want it to say the file is empty", err)
	}
}

// TestPagesCreateMany_JSONStreamsNDJSON asserts --json emits one page
// object per line as each lands, and no human summary line — the stream
// has to stay pipeable into jq.
func TestPagesCreateMany_JSONStreamsNDJSON(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl",
		"{\"parent\":\"p1\",\"title\":\"A\"}\n{\"parent\":\"p1\",\"title\":\"B\"}\n")
	_, out, err := runCreateMany(t, "--from", path, "--json")
	if err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d output lines, want 2 NDJSON records:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var page map[string]interface{}
		if err := json.Unmarshal([]byte(line), &page); err != nil {
			t.Errorf("line %d is not JSON (%v): %s", i, err, line)
			continue
		}
		if page["id"] != "newPageID" {
			t.Errorf("line %d = %v, want a created page object", i, page)
		}
	}
}

// TestPagesCreateMany_JSONErrorsStayJSON pins the --json stderr contract
// from #64: a per-entry failure is an envelope, not a plain red line, so
// a consumer reading stderr as line-delimited JSON does not choke on a
// partial import.
func TestPagesCreateMany_JSONErrorsStayJSON(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl",
		"{\"parent\":\"p1\",\"title\":\"A\"}\n{\"parent\":\"p1\",\"title\":\"REJECT\"}\n")
	d := withPagesEnv(t)
	d.failCreateWith = "REJECT"
	resetPagesFlags()
	resetRootCmdArgs()

	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"pages", "create-many", "--from", path, "--on-error", "continue", "--json"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute succeeded on a partial import (out=%s err=%s)", out.String(), errBuf.String())
	}
	for i, line := range strings.Split(strings.TrimSpace(errBuf.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var env map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(line), &env); jsonErr != nil {
			t.Errorf("stderr line %d is not JSON (%v): %s", i, jsonErr, line)
		}
	}
}

// TestPagesCreateMany_AbortExitsNonZeroAndStops covers the default mode
// at the command layer: the run stops at the first failure and reports a
// failure, so a script does not read a partial import as a clean one.
func TestPagesCreateMany_AbortExitsNonZeroAndStops(t *testing.T) {
	path := writeSpecFile(t, "rows.jsonl",
		"{\"parent\":\"p1\",\"title\":\"A\"}\n{\"parent\":\"p1\",\"title\":\"REJECT\"}\n{\"parent\":\"p1\",\"title\":\"C\"}\n")
	d := withPagesEnv(t)
	d.failCreateWith = "REJECT"
	resetPagesFlags()
	resetRootCmdArgs()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"pages", "create-many", "--from", path})
	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("Execute succeeded despite a failed entry (out=%s)", out.String())
	}
	if got := d.count("POST /pages"); got != 2 {
		t.Errorf("POST /pages count = %d, want 2 — the third entry must not be attempted under --on-error abort", got)
	}
	if !strings.Contains(out.String(), "Created 1 of 3") {
		t.Errorf("output %q does not report what actually landed; the operator needs to know what not to re-run", out.String())
	}
}
