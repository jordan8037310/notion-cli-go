// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSplitLeadingHeading covers the rule that keeps a markdown file's
// first line from vanishing on create. The rule is deliberately narrow:
// only a level-1 ATX heading on the first non-blank line, because a
// broader rule would start rewriting documents nobody asked it to.
func TestSplitLeadingHeading(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantHeading string
		wantRest    string
		wantFound   bool
	}{
		{
			name:        "leading h1 is split off",
			in:          "# My Page\n\nbody text\n",
			wantHeading: "My Page",
			wantRest:    "body text\n",
			wantFound:   true,
		},
		{
			name:        "blank lines before the heading are tolerated",
			in:          "\n\n# My Page\nbody\n",
			wantHeading: "My Page",
			wantRest:    "body\n",
			wantFound:   true,
		},
		{
			name:        "CRLF line endings",
			in:          "# My Page\r\n\r\nbody\r\n",
			wantHeading: "My Page",
			wantRest:    "body\r\n",
			wantFound:   true,
		},
		{
			name:      "h2 is left alone",
			in:        "## Sub\n\nbody\n",
			wantRest:  "## Sub\n\nbody\n",
			wantFound: false,
		},
		{
			name: "heading that is not first is left alone — Notion keeps those",
			in:   "intro\n\n# Later\n",
			// The whole document comes back untouched, including the H1.
			wantRest:  "intro\n\n# Later\n",
			wantFound: false,
		},
		{
			name:      "hash without a space is not a heading",
			in:        "#NotAHeading\n\nbody\n",
			wantRest:  "#NotAHeading\n\nbody\n",
			wantFound: false,
		},
		{
			name:      "empty heading text is not promoted to an empty title",
			in:        "# \n\nbody\n",
			wantRest:  "# \n\nbody\n",
			wantFound: false,
		},
		{
			name:        "heading only, no body",
			in:          "# Just A Title\n",
			wantHeading: "Just A Title",
			wantRest:    "",
			wantFound:   true,
		},
		{
			name:      "empty document",
			in:        "",
			wantRest:  "",
			wantFound: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			heading, rest, found := SplitLeadingHeading(tc.in)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if heading != tc.wantHeading {
				t.Errorf("heading = %q, want %q", heading, tc.wantHeading)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// markdownMock records the PATCH bodies sent to /pages/{id}/markdown.
type markdownMock struct {
	srv    *httptest.Server
	paths  []string
	bodies []map[string]interface{}
	status int
}

func newMarkdownMock(t *testing.T) *markdownMock {
	t.Helper()
	m := &markdownMock{status: http.StatusOK}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(buf, &body)
		m.paths = append(m.paths, r.Method+" "+r.URL.Path)
		m.bodies = append(m.bodies, body)
		if m.status != http.StatusOK {
			w.WriteHeader(m.status)
			_, _ = io.WriteString(w, `{"object":"error","status":400,"code":"validation_error","message":"nope"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"page_markdown","id":"pg1","markdown":"# Rendered\n","truncated":false,"unknown_block_ids":[]}`)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *markdownMock) client() *PageClient {
	return NewPageClient(NewClient("sk_test", WithBaseURL(m.srv.URL), WithMaxRetries(0)))
}

// TestReplaceMarkdown_SendsTheDiscriminatedCommand pins the wire shape.
// developers.notion.com documents replace_content as a plain string; live,
// the endpoint requires a discriminated command whose payload is an object
// keyed new_str. Building on the documented shape 400s on every call, so
// this test exists to stop a well-meaning "simplification" back to it.
func TestReplaceMarkdown_SendsTheDiscriminatedCommand(t *testing.T) {
	m := newMarkdownMock(t)
	out, err := m.client().ReplaceMarkdown(context.Background(), "pg1", "# New\n\nbody\n")
	if err != nil {
		t.Fatalf("ReplaceMarkdown: %v", err)
	}
	if out.Markdown != "# Rendered\n" {
		t.Errorf("Markdown = %q, want the re-rendered page echoed back", out.Markdown)
	}
	if got := m.paths[0]; got != "PATCH /pages/pg1/markdown" {
		t.Fatalf("called %q, want PATCH /pages/pg1/markdown", got)
	}
	body := m.bodies[0]
	if body["type"] != "replace_content" {
		t.Errorf("type = %v, want replace_content", body["type"])
	}
	rc, ok := body["replace_content"].(map[string]interface{})
	if !ok {
		t.Fatalf("replace_content = %#v, want an object (a bare string is the documented-but-wrong shape)", body["replace_content"])
	}
	if rc["new_str"] != "# New\n\nbody\n" {
		t.Errorf("new_str = %q, want the markdown verbatim", rc["new_str"])
	}
	// allow_deleting_content is deliberately never sent: a body replace
	// must not be able to take a page's sub-pages with it.
	if _, present := body["allow_deleting_content"]; present {
		t.Error("allow_deleting_content was sent; replacing a body must never delete child pages")
	}
}

// TestAppendMarkdown_PositionEndAndStart covers both insert positions and
// the nested payload key, which differs from replace_content's.
func TestAppendMarkdown_PositionEndAndStart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		atStart bool
		want    string
	}{
		{name: "append goes to the end", atStart: false, want: "end"},
		{name: "prepend goes to the start", atStart: true, want: "start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMarkdownMock(t)
			if _, err := m.client().AppendMarkdown(context.Background(), "pg1", "more\n", tc.atStart); err != nil {
				t.Fatalf("AppendMarkdown: %v", err)
			}
			body := m.bodies[0]
			if body["type"] != "insert_content" {
				t.Fatalf("type = %v, want insert_content", body["type"])
			}
			ic, ok := body["insert_content"].(map[string]interface{})
			if !ok {
				t.Fatalf("insert_content = %#v, want an object", body["insert_content"])
			}
			if ic["content"] != "more\n" {
				t.Errorf("content = %q, want the markdown verbatim", ic["content"])
			}
			pos, _ := ic["position"].(map[string]interface{})
			if pos["type"] != tc.want {
				t.Errorf("position = %v, want type %q", ic["position"], tc.want)
			}
		})
	}
}

// TestMarkdownWrites_RequireAnID keeps a missing argument from becoming a
// PATCH against /pages//markdown.
func TestMarkdownWrites_RequireAnID(t *testing.T) {
	m := newMarkdownMock(t)
	if _, err := m.client().ReplaceMarkdown(context.Background(), "", "x"); err == nil {
		t.Error("ReplaceMarkdown accepted an empty id")
	}
	if _, err := m.client().AppendMarkdown(context.Background(), "", "x", false); err == nil {
		t.Error("AppendMarkdown accepted an empty id")
	}
	if len(m.paths) != 0 {
		t.Errorf("made %v requests for an empty id", m.paths)
	}
}

// TestMarkdownWrites_SurfaceAPIErrors confirms a 400 comes back as a
// structured APIError rather than a bare status line.
func TestMarkdownWrites_SurfaceAPIErrors(t *testing.T) {
	m := newMarkdownMock(t)
	m.status = http.StatusBadRequest
	_, err := m.client().ReplaceMarkdown(context.Background(), "pg1", "x")
	if err == nil {
		t.Fatal("ReplaceMarkdown succeeded against a 400")
	}
	if !strings.Contains(err.Error(), "validation_error") {
		t.Errorf("error = %q, want Notion's own code surfaced", err)
	}
}

// TestCreateSendsMarkdown checks the create path forwards the markdown
// body param rather than trying to convert it locally.
func TestCreateSendsMarkdown(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"page","id":"newpg"}`)
	}))
	defer srv.Close()

	pc := NewPageClient(NewClient("sk_test", WithBaseURL(srv.URL), WithMaxRetries(0)))
	_, err := pc.Create(context.Background(), CreatePageRequest{
		Parent:   PageParent{PageID: "parent"},
		Title:    "T",
		Markdown: "## Section\n\ntext\n",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if body["markdown"] != "## Section\n\ntext\n" {
		t.Errorf("markdown = %q, want it forwarded verbatim for Notion to parse", body["markdown"])
	}
}
