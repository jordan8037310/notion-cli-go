// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build integration

// Package integration runs notion-cli-go against a real Notion workspace.
//
// Why this exists: every defect found in the 2026-08-29/30 audit passed the
// unit suite and failed on first contact with the API. httptest mocks only
// prove the serialiser agrees with the assumption that produced it — when
// Notion's 2025-09-03 release moved a database's schema onto its data
// source, the mocks kept asserting the old shape and three commands shipped
// broken. See contracts_test.go for the shapes that drift, recorded from
// live responses rather than from documentation.
//
// Everything here is opt-in. Without both env vars the package skips.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// EnvAPIKey and EnvFixtureParent gate the whole package. They are
	// deliberately NOT the names the CLI itself reads (NOTION_API_KEY),
	// so a developer's everyday credential cannot accidentally point a
	// mutating test suite at their production workspace.
	EnvAPIKey        = "NOTION_INTEGRATION_API_KEY"
	EnvFixtureParent = "NOTION_INTEGRATION_FIXTURE_PARENT"

	// APIVersion must track utils.NotionAPIVersion. The contracts in this
	// package are version-specific claims; pinning it here means a version
	// bump surfaces as a contract failure naming the shape that moved,
	// rather than as a mystery 400 in production.
	APIVersion = "2026-03-11"

	notionBase = "https://api.notion.com/v1"
)

// Harness is one integration run: a credential, a scratch page created for
// this run alone, an artefact directory, and a cleanup stack.
type Harness struct {
	t         *testing.T
	apiKey    string
	parentID  string
	RunID     string
	ArtifactD string
	PageID    string

	mu      sync.Mutex
	cleanup []func()
	binPath string
}

// New builds a Harness or skips the test. It creates a per-run page under
// the fixture parent so no test ever mutates a page a human cares about,
// and registers that page for archival via t.Cleanup.
func New(t *testing.T) *Harness {
	t.Helper()

	key := os.Getenv(EnvAPIKey)
	parent := os.Getenv(EnvFixtureParent)
	if key == "" || parent == "" {
		t.Skipf("integration tests need %s and %s; set both to run them (see README → Running integration tests)",
			EnvAPIKey, EnvFixtureParent)
	}

	// A run id from the clock is enough: runs are serialised per workspace
	// today (see issue #52, "Notion-side concurrency" is out of scope).
	runID := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(".testdata", "integration", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create artefact dir: %v", err)
	}

	h := &Harness{t: t, apiKey: key, parentID: parent, RunID: runID, ArtifactD: dir}
	t.Cleanup(h.runCleanup)

	h.PageID = h.createRunPage()
	t.Logf("integration run %s → page %s, artefacts in %s", runID, h.PageID, dir)
	return h
}

// Defer registers a cleanup action, run last-in-first-out at test end.
// Cleanup failures are logged, never fatal: a leaked scratch page is worth
// less than the test result it would otherwise mask.
func (h *Harness) Defer(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanup = append(h.cleanup, fn)
}

func (h *Harness) runCleanup() {
	h.mu.Lock()
	fns := append([]func(){}, h.cleanup...)
	h.mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

// API issues a raw request against Notion and returns the status and decoded
// body. Contract tests use this rather than the CLI: the point is to observe
// what the API does, independent of what our client believes.
func (h *Harness) API(method, path string, body interface{}) (int, map[string]interface{}) {
	h.t.Helper()

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, notionBase+path, rdr)
	if err != nil {
		h.t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("Notion-Version", APIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	h.record(fmt.Sprintf("api_%s_%s", method, strings.NewReplacer("/", "_", "{", "", "}", "").Replace(path)), raw)

	var out map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// CLI runs the built notioncli binary with the harness credential and
// returns stdout+stderr combined plus the exit code. Every invocation is
// captured to the artefact directory.
func (h *Harness) CLI(args ...string) (string, int) {
	h.t.Helper()

	cmd := exec.Command(h.binary(), args...)
	cmd.Env = append(os.Environ(),
		"NOTION_API_KEY="+h.apiKey,
		"LOCAL_TIMEZONE=UTC",
	)
	// Run from the artefact dir so a stray .env in the repo root cannot
	// shadow the credential we mean to use (issue #99).
	cmd.Dir = h.ArtifactD

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatalf("run notioncli %s: %v", strings.Join(args, " "), err)
	}

	h.record("cli_"+strings.Join(nonFlagArgs(args), "_"), []byte(
		fmt.Sprintf("$ notioncli %s\nexit=%d\n\n%s", strings.Join(args, " "), code, buf.String())))
	return buf.String(), code
}

// binary builds the CLI once per harness and caches the path, so the tests
// exercise the same artefact a user runs rather than the library directly.
func (h *Harness) binary() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.binPath != "" {
		return h.binPath
	}
	out := filepath.Join(h.t.TempDir(), "notioncli")
	build := exec.Command("go", "build", "-o", out, ".")
	build.Dir = ".."
	if b, err := build.CombinedOutput(); err != nil {
		h.t.Fatalf("build notioncli: %v\n%s", err, b)
	}
	h.binPath = out
	return out
}

// record writes an artefact. Names are sanitised and collisions suffixed so
// a run that repeats a call keeps both captures.
func (h *Harness) record(name string, body []byte) {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, name)
	path := filepath.Join(h.ArtifactD, safe+".json")
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(h.ArtifactD, fmt.Sprintf("%s_%d.json", safe, i))
	}
	_ = os.WriteFile(path, body, 0o644)
}

// createRunPage makes the scratch page this run owns and schedules its
// archival. Provisioning through the API rather than the CLI is deliberate:
// if `pages create` is the thing that is broken, the harness must still be
// able to set up and tear down.
func (h *Harness) createRunPage() string {
	h.t.Helper()
	status, body := h.API(http.MethodPost, "/pages", map[string]interface{}{
		"parent": map[string]interface{}{"type": "page_id", "page_id": h.parentID},
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"title": []map[string]interface{}{
					{"type": "text", "text": map[string]interface{}{"content": "ci-" + h.RunID}},
				},
			},
		},
	})
	if status != http.StatusOK {
		h.t.Fatalf("provision run page: HTTP %d: %v\n(is %s shared with the integration?)", status, body, EnvFixtureParent)
	}
	id, _ := body["id"].(string)
	if id == "" {
		h.t.Fatalf("provision run page: no id in response: %v", body)
	}
	h.Defer(func() { h.Archive("/pages/", id) })
	return id
}

// Archive trashes an object so the fixture workspace does not accumulate
// detritus. Notion has no hard delete on this surface, so in_trash is the
// strongest cleanup available.
func (h *Harness) Archive(prefix, id string) {
	status, _ := h.API(http.MethodPatch, prefix+id, map[string]interface{}{"in_trash": true})
	if status != http.StatusOK {
		h.t.Logf("cleanup: could not trash %s%s (HTTP %d) — remove it by hand", prefix, id, status)
	}
}

// CaptureFinalState writes the run page and its block list to the artefact
// directory. Acceptance criterion of issue #52, and the thing that makes a
// failed run diffable after the fact.
func (h *Harness) CaptureFinalState() {
	h.API(http.MethodGet, "/pages/"+h.PageID, nil)
	h.API(http.MethodGet, "/blocks/"+h.PageID+"/children", nil)
}

func nonFlagArgs(args []string) []string {
	out := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if len(out) >= 3 {
			break
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return []string{"cmd"}
	}
	return out
}

// writeFileHelper is a thin os.WriteFile wrapper so the e2e file lives next
// to the run's other artefacts.
func writeFileHelper(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
