// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Markdown write support.
//
// Issue #45 asked for a hand-rolled markdown ↔ blocks converter in this
// file: BlocksToMarkdown and MarkdownToBlocks, "roll own parser/renderer,
// keep simple".
//
// Neither is written here, because Notion does both server-side and does
// them better:
//
//   - READ  — GET /v1/pages/{id}/markdown renders a whole page, including
//     content inside toggles, columns and synced blocks that `blocks list`
//     cannot even see. A local renderer fed from `blocks list` would emit
//     strictly less than the page contains, and would do it silently.
//   - WRITE — POST /v1/pages takes a `markdown` string, and
//     PATCH /v1/pages/{id}/markdown replaces or appends markdown on an
//     existing page. Both are verified live below.
//
// A local parser would be worse on the day it shipped and would drift
// every time Notion extends its own markdown dialect. What this file
// provides instead is the thin plumbing to those endpoints, plus the one
// piece of behaviour Notion gets wrong for a CLI's purposes: the leading
// H1 (see SplitLeadingHeading).
//
// WIRE SHAPES, ESTABLISHED LIVE, NOT FROM THE DOCS.
// developers.notion.com currently documents the PATCH body as flat keys
// (`replace_content` a string, `update_content` an array). Live, the
// endpoint rejects all of that. The real shape is a discriminated command:
//
//	{"type":"replace_content","replace_content":{"new_str":"<md>"}}
//	{"type":"insert_content", "insert_content":{"content":"<md>",
//	                                            "position":{"type":"start"|"end"}}}
//	{"type":"update_content", "update_content":{"content_updates":[
//	                                            {"old_str":..,"new_str":..}]}}
//
// Building on the documented shapes would have produced a 400 on every
// call. Only replace_content and insert_content are wired up here;
// update_content (surgical search-and-replace) is wired up too — see
// UpdateMarkdown, and the anti-contract it has to compensate for.

// markdownCommand is the discriminated body PATCH /pages/{id}/markdown
// expects. The type field and the payload key must agree — the endpoint
// dispatches on `type` and then validates only that key.
type markdownCommand struct {
	Type           string                 `json:"type"`
	ReplaceContent map[string]interface{} `json:"replace_content,omitempty"`
	InsertContent  map[string]interface{} `json:"insert_content,omitempty"`
	UpdateContent  map[string]interface{} `json:"update_content,omitempty"`
}

// MarkdownEdit is one search-and-replace against a page's markdown.
//
// ReplaceAll maps to the per-update `replace_all_matches` key. Note the
// spelling: when Notion rejects an ambiguous edit its own error says "Set
// replaceAllMatches to true" — and that camelCase spelling is a 400. The
// key the API actually accepts is snake_case, confirmed live. Following
// the error message's advice does not work.
type MarkdownEdit struct {
	Old        string `json:"old_str"`
	New        string `json:"new_str"`
	ReplaceAll bool   `json:"replace_all_matches,omitempty"`
}

// ReplaceMarkdown replaces a page's entire body with the given markdown
// and returns the page as Notion re-renders it.
//
// This is destructive: every existing block on the page is removed. The
// caller is expected to have made that explicit to the user — the CLI
// spells the flag --replace-markdown for exactly this reason.
//
// Child pages and child databases are NOT deleted; Notion gates those
// behind its own allow_deleting_content flag, which this deliberately
// does not send. A page's sub-pages surviving a body replace is the safe
// default, and losing them should never be a side effect of editing text.
func (p *PageClient) ReplaceMarkdown(ctx context.Context, id, markdown string) (*PageMarkdown, error) {
	return p.patchMarkdown(ctx, id, "replace page markdown", markdownCommand{
		Type:           "replace_content",
		ReplaceContent: map[string]interface{}{"new_str": markdown},
	})
}

// AppendMarkdown adds markdown to an existing page without disturbing
// what is already there. atStart prepends instead of appending.
func (p *PageClient) AppendMarkdown(ctx context.Context, id, markdown string, atStart bool) (*PageMarkdown, error) {
	position := "end"
	if atStart {
		position = "start"
	}
	return p.patchMarkdown(ctx, id, "append page markdown", markdownCommand{
		Type: "insert_content",
		InsertContent: map[string]interface{}{
			"content":  markdown,
			"position": map[string]interface{}{"type": position},
		},
	})
}

// patchMarkdown is the shared transport for the markdown commands.
//
// Large markdown bodies can exceed the request timeout; Notion offers an
// allow_async option that returns 202 plus a task to poll. That is not
// sent here — a synchronous result is what a CLI command wants, and the
// client's own timeout is the honest failure. If real files start timing
// out, allow_async plus a poll loop is the upgrade path.
func (p *PageClient) patchMarkdown(ctx context.Context, id, op string, cmd markdownCommand) (*PageMarkdown, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if id == "" {
		return nil, fmt.Errorf("%s: id is required", op)
	}
	req, err := p.c.newRequest(ctx, http.MethodPatch, "/pages/"+id+"/markdown", cmd)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	var out PageMarkdown
	if err := decodeInto(resp, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// SplitLeadingHeading pulls a leading `# Heading` off a markdown document
// and returns it separately from the rest.
//
// This exists because of a silent data loss in Notion's own create path,
// confirmed live against 2026-03-11:
//
//	POST /v1/pages with markdown starting "# Title\n\nbody"
//	  → the H1 block is NOT created, and it is NOT used as the page title
//	    either. The page comes back titled "" with a single paragraph.
//
// An H1 that is not first survives fine, and replace_content keeps a
// leading H1 too — the loss is specific to create. Since nearly every
// markdown file in existence opens with its title as an H1, a CLI that
// forwarded the file verbatim would drop the most important line of the
// document and report success.
//
// Callers promote the heading to the page title instead, which is what
// the author meant by writing it. found is false when the document does
// not open with an H1, in which case md is returned unchanged.
//
// Only a level-1 ATX heading on the first non-blank line counts. Setext
// underlining and deeper levels are left alone: the narrower the rule,
// the less it can surprise someone.
func SplitLeadingHeading(md string) (heading, rest string, found bool) {
	trimmed := strings.TrimLeft(md, "\r\n")
	line, remainder, _ := strings.Cut(trimmed, "\n")
	text := strings.TrimSpace(strings.TrimSuffix(line, "\r"))

	// "#" followed by a space. "#Heading" is not a heading in CommonMark,
	// and "## Sub" is a level 2 that Notion keeps.
	if !strings.HasPrefix(text, "# ") {
		return "", md, false
	}
	heading = strings.TrimSpace(strings.TrimPrefix(text, "# "))
	if heading == "" {
		return "", md, false
	}
	return heading, strings.TrimLeft(remainder, "\r\n"), true
}

// UpdateMarkdown applies search-and-replace edits to a page's markdown.
//
// It reads the page before writing, to check every edit will match. That
// pre-flight exists because of an anti-contract confirmed live against
// 2026-03-11:
//
//	ONE edit whose old_str matches nothing  → HTTP 400, "No matches found"
//	the SAME edit batched with a valid one  → HTTP 200, silently dropped
//
// Order makes no difference; the unmatched edit simply does not happen and
// nothing says so. A script applying five edits where three no longer
// match gets a 200 and believes all five landed. That is the failure mode
// this CLI exists to prevent, so the check is not optional.
//
// The pre-flight also improves the ambiguous case. Notion rejects an
// old_str with several matches and tells the caller to "Set
// replaceAllMatches to true" — a camelCase spelling the API rejects.
// Checking locally means the error names the edit, the number of matches
// and the flag that actually works.
//
// There is a window between the read and the write in which the page can
// change, so this narrows the failure rather than closing it. A batch
// whose edits stop matching in that window is still silently dropped by
// Notion; nothing short of an API fix can catch that.
func (p *PageClient) UpdateMarkdown(ctx context.Context, id string, edits []MarkdownEdit) (*PageMarkdown, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("update page markdown: %w", err)
	}
	if id == "" {
		return nil, fmt.Errorf("update page markdown: id is required")
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("update page markdown: no edits given")
	}
	current, err := p.GetMarkdown(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("update page markdown: read current content: %w", err)
	}
	if err := VerifyEdits(current.Markdown, edits); err != nil {
		return nil, fmt.Errorf("update page markdown: %w", err)
	}
	// The edits go on the wire as-is; the struct tags carry the snake_case
	// names the endpoint wants.
	return p.patchMarkdown(ctx, id, "update page markdown", markdownCommand{
		Type:          "update_content",
		UpdateContent: map[string]interface{}{"content_updates": edits},
	})
}

// VerifyEdits reports the first edit that would not apply cleanly to md.
//
// Split out as a pure function so the rules are testable without a network
// round-trip, and so a caller can check a batch before spending a request.
func VerifyEdits(md string, edits []MarkdownEdit) error {
	for i, edit := range edits {
		if edit.Old == "" {
			return fmt.Errorf("edit %d has an empty search string", i+1)
		}
		switch n := strings.Count(md, edit.Old); {
		case n == 0:
			return fmt.Errorf("edit %d: %q is not in the page, so it would be skipped without an error",
				i+1, truncateForError(edit.Old))
		case n > 1 && !edit.ReplaceAll:
			return fmt.Errorf("edit %d: %q appears %d times; pass --replace-all to change every occurrence, or make the search text unique",
				i+1, truncateForError(edit.Old), n)
		}
	}
	return nil
}

// truncateForError keeps a long search string from burying the rest of an
// error message.
func truncateForError(s string) string {
	const max = 60
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
