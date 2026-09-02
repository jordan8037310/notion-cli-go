// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// These drive the built binary the way a user does. The contract tests above
// prove what the API accepts; these prove the CLI actually produces it.
// Both halves are needed: a client can send a well-formed request to the
// wrong endpoint and every unit test will still pass.

// TestE2E_DatabaseSchemaLifecycle is the regression guard for the defects
// fixed in PRs #95 and #111. Each step failed silently in production while
// the unit suite was green.
func TestE2E_DatabaseSchemaLifecycle(t *testing.T) {
	h := New(t)

	schema := h.writeFile(t, "schema.json", `{
		"Name":     {"title": {}},
		"Priority": {"select": {}},
		"Notes":    {"rich_text": {}}
	}`)

	// 1. create — the schema must actually land (pre-#111 it was dropped
	//    while the CLI printed a green success and exited 0).
	out, code := h.CLI("databases", "create", "--parent", h.PageID,
		"--title", "e2e schema lifecycle", "--properties-json", schema, "--json")
	if code != 0 {
		t.Fatalf("databases create exited %d: %s", code, out)
	}
	dbID := jsonField(t, out, "id")
	h.Defer(func() { h.Archive("/databases/", dbID) })

	dsID := firstDataSource(t, h.getJSON(t, "/databases/"+dbID))
	assertColumns(t, h.schemaOf(t, dsID), "create", "Name", "Priority", "Notes")

	// 2. data-sources — the discovery command from #94. Without it the id
	//    the next step needs is unobtainable from the CLI.
	out, code = h.CLI("databases", "data-sources", dbID, "--json")
	if code != 0 {
		t.Fatalf("databases data-sources exited %d: %s", code, out)
	}
	if !strings.Contains(out, dsID) {
		t.Errorf("data-sources did not list %s; got: %s", dsID, out)
	}

	// 3. update with a DATABASE id — must resolve to the data source rather
	//    than PATCHing a container that ignores `properties`.
	addOwner := h.writeFile(t, "owner.json", `{"Owner": {"people": {}}}`)
	if out, code = h.CLI("databases", "update", dbID, "--properties-json", addOwner); code != 0 {
		t.Fatalf("databases update <db-id> --properties-json exited %d: %s", code, out)
	}
	assertColumns(t, h.schemaOf(t, dsID), "update via database id", "Owner")

	// 4. title + schema together. This is the invocation the PR #111 review
	//    proved could never succeed while Update split one id across two
	//    endpoints: the rename committed, then the schema call failed.
	addStage := h.writeFile(t, "stage.json", `{"Stage": {"select": {}}}`)
	if out, code = h.CLI("databases", "update", dbID, "--title", "e2e renamed", "--properties-json", addStage); code != 0 {
		t.Fatalf("combined title+schema update exited %d: %s", code, out)
	}
	assertColumns(t, h.schemaOf(t, dsID), "combined update", "Stage")
	if got := plain(h.getJSON(t, "/databases/"+dbID)["title"]); got != "e2e renamed" {
		t.Errorf("combined update did not apply the title: got %q", got)
	}
}

// TestE2E_FetchAndBlocksAreLossless guards the JSON output contract from
// PR #91: --json must emit what Notion returned, not a re-marshalled struct
// that drops every field the CLI does not model.
func TestE2E_FetchAndBlocksAreLossless(t *testing.T) {
	h := New(t)

	if out, code := h.CLI("blocks", "add", "e2e paragraph", "--page", h.PageID); code != 0 {
		t.Fatalf("blocks add exited %d: %s", code, out)
	}

	out, code := h.CLI("fetch", h.PageID, "--json")
	if code != 0 {
		t.Fatalf("fetch --json exited %d: %s", code, out)
	}
	var page map[string]interface{}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("fetch --json is not valid JSON: %v\n%s", err, out)
	}
	// created_by and last_edited_by are returned by the API and are not
	// modelled on utils.Page — they survive only via the raw passthrough.
	for _, key := range []string{"created_by", "last_edited_by", "request_id"} {
		if _, ok := page[key]; !ok {
			t.Errorf("fetch --json dropped %q — the loss-free passthrough (#80) has regressed. Keys: %v",
				key, keysOf(page))
		}
	}

	out, code = h.CLI("blocks", "list", "--page", h.PageID, "--json")
	if code != 0 {
		t.Fatalf("blocks list --json exited %d: %s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("blocks list --json returned nothing; the unfiltered listing (#88) may have regressed")
	}
	var blk map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &blk); err != nil {
		t.Fatalf("blocks list --json line is not JSON: %v\n%s", err, lines[0])
	}
	if _, ok := blk["created_by"]; !ok {
		t.Errorf("blocks list --json dropped created_by — the raw passthrough (#86) has regressed. Keys: %v", keysOf(blk))
	}
}

// TestE2E_ErrorsAreDiagnosable pins the failure contract: non-zero exit, and
// in --json mode exactly one envelope on stderr. Issue #64 produced two
// outputs; its first fix produced zero.
func TestE2E_ErrorsAreDiagnosable(t *testing.T) {
	h := New(t)
	const ghost = "00000000-0000-0000-0000-000000000000"

	out, code := h.CLI("fetch", ghost, "--json")
	if code == 0 {
		t.Errorf("fetch on a nonexistent id exited 0; want non-zero. Output: %s", out)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(l) != "" && !strings.Contains(l, "NotionCLI") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("json-mode failure emitted %d lines, want exactly 1 envelope:\n%s", len(lines), out)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("json-mode failure is not an envelope: %v\n%s", err, lines[0])
	}
	if msg, _ := env["error"].(string); msg == "" {
		t.Error("error envelope carries no message")
	}
	// Since issue #101 the envelope carries the structured fields too, so a
	// consumer can branch on `code` and quote `request_id` to Notion support
	// instead of parsing an English sentence. request_id in particular is the
	// first thing support asks for.
	for _, key := range []string{"status", "code", "request_id"} {
		if _, ok := env[key]; !ok {
			t.Errorf("error envelope dropped %q — the structured error contract (#101) has regressed. Got keys %v",
				key, keysOf(env))
		}
	}
	if code, _ := env["code"].(string); code != "object_not_found" {
		t.Errorf("code = %q, want object_not_found", code)
	}
}

// TestE2E_CaptureFinalState is the acceptance criterion from issue #52: the
// run's end state lands in the artefact directory so a failure is diffable
// afterwards rather than only reproducible.
func TestE2E_CaptureFinalState(t *testing.T) {
	h := New(t)
	if out, code := h.CLI("blocks", "add", "final state marker", "--page", h.PageID); code != 0 {
		t.Fatalf("blocks add exited %d: %s", code, out)
	}
	h.CaptureFinalState()
	t.Logf("artefacts written to %s", h.ArtifactD)
}

// --- helpers ---------------------------------------------------------------

func (h *Harness) writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := h.ArtifactD + "/" + name
	if err := writeFileHelper(path, body); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	// The CLI runs with cwd == ArtifactD, so a bare name resolves.
	return name
}

func (h *Harness) getJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	status, body := h.API(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %v", path, status, body)
	}
	return body
}

func jsonField(t *testing.T, out, key string) string {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	v, _ := m[key].(string)
	if v == "" {
		t.Fatalf("no %q in output: %s", key, out)
	}
	return v
}

func assertColumns(t *testing.T, schema map[string]interface{}, stage string, want ...string) {
	t.Helper()
	for _, w := range want {
		if _, ok := schema[w]; !ok {
			t.Errorf("%s: column %q missing. Notion accepts the obsolete shape with HTTP 200 and\n"+
				"silently drops the schema, so this is what a regression looks like — not an error.\nHave: %v",
				stage, w, keysOf(schema))
		}
	}
}

// TestE2E_FileUploadRoundTrip guards the defect that motivated adding this
// case: Notion derives a content type from the filename at create time and
// rejects a send whose multipart part disagrees. multipart.CreateFormFile
// hardcodes application/octet-stream, so EVERY upload of a recognisably
// typed file failed in production — while the unit suite passed, because a
// mock has no opinion about part headers.
//
// This is the shape of bug the whole package exists for, so it is covered
// against the real API rather than only against a fixture.
func TestE2E_FileUploadRoundTrip(t *testing.T) {
	h := New(t)

	for _, tt := range []struct{ name, body string }{
		{"notes.txt", "hello from the integration harness\n"},
		{"data.json", `{"ok":true}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h.writeFile(t, tt.name, tt.body)

			out, code := h.CLI("blocks", "add-file", tt.name, "--page", h.PageID, "--json")
			if code != 0 {
				t.Fatalf("blocks add-file %s exited %d — a typed file was rejected: %s", tt.name, code, out)
			}
			if !strings.Contains(out, `"ok":true`) && !strings.Contains(out, `"id"`) {
				t.Errorf("upload of %s produced no file reference: %s", tt.name, out)
			}
		})
	}

	// The same multipart path backs page icons, so a regression there
	// would otherwise only surface for a user setting one. Notion requires
	// an actual image here — a .txt is rejected with "has a type of
	// productivity, but only image files are expected" — so this writes a
	// real 1x1 PNG rather than a placeholder.
	if err := writeFileHelperBytes(h.ArtifactD+"/icon.png", onePixelPNG()); err != nil {
		t.Fatalf("write icon: %v", err)
	}
	if out, code := h.CLI("pages", "set-icon", h.PageID, "icon.png", "--json"); code != 0 {
		t.Fatalf("pages set-icon exited %d: %s", code, out)
	}
}

// onePixelPNG returns the smallest valid PNG, so the icon path can be
// exercised without committing a binary fixture.
func onePixelPNG() []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x0F, G: 0x6E, B: 0x68, A: 0xFF})
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
