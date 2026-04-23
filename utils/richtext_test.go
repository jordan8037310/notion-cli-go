// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fatih/color"
)

// withNoColor flips fatih/color off for the duration of the test. This
// gives us a deterministic PlainRichText-style output from RenderRichText
// so we can string-equal the result without embedding ANSI escapes in
// every assertion.
func withNoColor(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })
}

// withColorOn flips fatih/color on even when the test binary is running
// without a TTY (which is the default in `go test`). Used by the one
// test that asserts ANSI escapes are actually produced when annotations
// are present.
func withColorOn(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })
}

// TestRenderRichText_Basic covers the plain-text + multi-segment
// happy path and the empty-slice edge case. color.NoColor=true so the
// output is plain and deterministic.
func TestRenderRichText_Basic(t *testing.T) {
	withNoColor(t)

	tests := []struct {
		name string
		in   []RichText
		want string
	}{
		{name: "empty", in: nil, want: ""},
		{
			name: "single plain segment",
			in:   []RichText{{PlainText: "hello"}},
			want: "hello",
		},
		{
			name: "multi segment concatenates",
			in: []RichText{
				{PlainText: "hello "},
				{PlainText: "world"},
			},
			want: "hello world",
		},
		{
			name: "falls back to text.content when plain_text empty",
			in:   []RichText{{Text: Text{Content: "from-text"}}},
			want: "from-text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderRichText(tt.in)
			if got != tt.want {
				t.Errorf("RenderRichText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderRichText_Annotations covers every annotation flag
// individually. With color.NoColor=true, ANSI color attrs collapse to
// identity but the backtick wrap on code runs must survive.
func TestRenderRichText_Annotations(t *testing.T) {
	withNoColor(t)

	tests := []struct {
		name string
		ann  Annotation
		want string
	}{
		{"bold", Annotation{Bold: true}, "word"},
		{"italic", Annotation{Italic: true}, "word"},
		{"strike", Annotation{Strikethrough: true}, "word"},
		{"underline", Annotation{Underline: true}, "word"},
		{"code wraps in backticks", Annotation{Code: true}, "`word`"},
		{"color only", Annotation{Color: "red"}, "word"},
		{"default color noop", Annotation{Color: "default"}, "word"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderRichText([]RichText{{PlainText: "word", Annotations: tt.ann}})
			if got != tt.want {
				t.Errorf("RenderRichText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderRichText_ANSI asserts that at least one ANSI escape
// is emitted when annotations are set and color is enabled. Keeps the
// coupling to fatih/color loose — we don't pin the exact escape sequence.
func TestRenderRichText_ANSI(t *testing.T) {
	withColorOn(t)

	got := RenderRichText([]RichText{{PlainText: "word", Annotations: Annotation{Bold: true}}})
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI escape in output, got %q", got)
	}
	if !strings.Contains(got, "word") {
		t.Errorf("output %q missing payload", got)
	}
}

// TestRenderRichText_Mentions covers all four mention shapes.
func TestRenderRichText_Mentions(t *testing.T) {
	withNoColor(t)

	tests := []struct {
		name string
		in   RichText
		want string
	}{
		{
			name: "user by name",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "user", User: &User{Name: "Jordan"}}},
			want: "@Jordan",
		},
		{
			name: "user by id fallback",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "user", User: &User{ID: "u-1"}}},
			want: "@u-1",
		},
		{
			name: "page",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "page", Page: &PageMention{ID: "p-1"}}},
			want: "[page:p-1]",
		},
		{
			name: "database",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "database", Database: &DatabaseMention{ID: "d-1"}}},
			want: "{db:d-1}",
		},
		{
			name: "date single",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "date", Date: &DateMention{Start: "2026-04-22"}}},
			want: "<2026-04-22>",
		},
		{
			name: "date range",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "date", Date: &DateMention{Start: "2026-04-22", End: "2026-04-25"}}},
			want: "<2026-04-22..2026-04-25>",
		},
		{
			name: "malformed mention falls back to plain_text",
			in:   RichText{Type: "mention", PlainText: "@someone", Mention: &Mention{Type: "user"}},
			want: "@someone",
		},
		{
			name: "unknown mention type falls back to plain_text",
			in:   RichText{Type: "mention", PlainText: "@x", Mention: &Mention{Type: "template_variable"}},
			want: "@x",
		},
		{
			// Locks the <mention> sentinel: when both the typed sub-object
			// is unknown AND Notion did not supply a PlainText fallback,
			// renderMention must still emit a non-empty marker so the
			// segment does not silently disappear. Covers the last
			// fallback arm in utils/richtext.go:renderMention.
			name: "unknown mention with empty plain_text yields sentinel",
			in:   RichText{Type: "mention", Mention: &Mention{Type: "template_variable"}},
			want: "<mention>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderRichText([]RichText{tt.in})
			if got != tt.want {
				t.Errorf("RenderRichText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderRichText_Equation asserts inline equations are
// wrapped in "$...$".
func TestRenderRichText_Equation(t *testing.T) {
	withNoColor(t)

	got := RenderRichText([]RichText{{Type: "equation", Equation: &TextEquation{Expression: "E=mc^2"}}})
	want := "$E=mc^2$"
	if got != want {
		t.Errorf("RenderRichText() = %q, want %q", got, want)
	}
}

// TestRenderRichText_Mixed covers the realistic case: multi-
// segment input mixing plain text, an annotated run, a mention, and an
// equation.
func TestRenderRichText_Mixed(t *testing.T) {
	withNoColor(t)

	in := []RichText{
		{PlainText: "Assigned to "},
		{Type: "mention", Mention: &Mention{Type: "user", User: &User{Name: "Jordan"}}},
		{PlainText: ". Formula: "},
		{Type: "equation", Equation: &TextEquation{Expression: "a^2+b^2=c^2"}},
		{PlainText: " — ", Annotations: Annotation{Italic: true}},
		{PlainText: "done", Annotations: Annotation{Bold: true, Color: "green"}},
	}
	got := RenderRichText(in)
	want := "Assigned to @Jordan. Formula: $a^2+b^2=c^2$ — done"
	if got != want {
		t.Errorf("RenderRichText() = %q, want %q", got, want)
	}
}

// TestPlainRichText matches RenderRichText on the no-color path
// but strips the backtick wrap for code runs — it is specifically the
// unannotated flavour for JSON paths.
func TestPlainRichText(t *testing.T) {
	in := []RichText{
		{PlainText: "hello "},
		{PlainText: "world", Annotations: Annotation{Bold: true, Code: true}},
		{Type: "mention", Mention: &Mention{Type: "user", User: &User{Name: "J"}}},
	}
	got := PlainRichText(in)
	want := "hello world@J"
	if got != want {
		t.Errorf("PlainRichText() = %q, want %q", got, want)
	}
}

// TestPlainRichText_Empty covers the empty case.
func TestPlainRichText_Empty(t *testing.T) {
	if got := PlainRichText(nil); got != "" {
		t.Errorf("PlainRichText(nil) = %q, want empty", got)
	}
	if got := PlainRichText([]RichText{}); got != "" {
		t.Errorf("PlainRichText([]) = %q, want empty", got)
	}
}

// TestParseRichTextJSON_Happy covers a full-fidelity payload:
// multi-segment with annotations, a mention, and an equation.
func TestParseRichTextJSON_Happy(t *testing.T) {
	raw := []byte(`[
		{"type":"text","text":{"content":"hello "},"annotations":{"bold":true}},
		{"type":"mention","mention":{"type":"user","user":{"id":"u1","name":"Jordan"}}},
		{"type":"equation","equation":{"expression":"E=mc^2"}}
	]`)

	got, err := ParseRichTextJSON(raw)
	if err != nil {
		t.Fatalf("ParseRichTextJSON() err = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Text.Content != "hello " {
		t.Errorf("segment 0 content = %q, want %q", got[0].Text.Content, "hello ")
	}
	if !got[0].Annotations.Bold {
		t.Error("segment 0 bold not set")
	}
	if got[1].Mention == nil || got[1].Mention.User == nil || got[1].Mention.User.Name != "Jordan" {
		t.Errorf("segment 1 mention = %+v", got[1].Mention)
	}
	if got[2].Equation == nil || got[2].Equation.Expression != "E=mc^2" {
		t.Errorf("segment 2 equation = %+v", got[2].Equation)
	}
}

// TestParseRichTextJSON_Errors covers every rejection path:
// empty input, non-array, malformed JSON, empty array, empty-payload run.
func TestParseRichTextJSON_Errors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // substring expected in err
	}{
		{"empty bytes", "", "empty input"},
		{"whitespace only", "   \n  ", "empty input"},
		{"object at top level", `{"type":"text"}`, "must be an array"},
		{"malformed json", `[{`, "decode"},
		{"empty array", `[]`, "array is empty"},
		{
			"segment with no payload",
			`[{"type":"text","text":{"content":""}}]`,
			"no text / mention / equation payload",
		},
		{
			"unknown top-level field",
			`[{"type":"text","text":{"content":"x"},"bogus":1}]`,
			"decode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRichTextJSON([]byte(tt.raw))
			if err == nil {
				t.Fatalf("ParseRichTextJSON(%q) err = nil, want error containing %q", tt.raw, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want substring %q", err, tt.want)
			}
		})
	}
}

// TestRichText_RichTextToAPI verifies the internal write-path converter
// emits the Notion payload shape: default type is "text", mentions and
// equations swap the inner payload accordingly, and empty-default
// annotations are elided so we do not clobber Notion's defaults.
func TestRichText_RichTextToAPI(t *testing.T) {
	in := []RichText{
		{Text: Text{Content: "plain"}},
		{Text: Text{Content: "bold"}, Annotations: Annotation{Bold: true}},
		{Mention: &Mention{Type: "user", User: &User{ID: "u1"}}},
		{Equation: &TextEquation{Expression: "a+b"}},
	}
	got := richTextToAPI(in)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}

	// Segment 0: plain text, no annotations emitted.
	if got[0]["type"] != "text" {
		t.Errorf("segment 0 type = %v, want text", got[0]["type"])
	}
	if _, has := got[0]["annotations"]; has {
		t.Errorf("segment 0 should not emit empty annotations: %v", got[0]["annotations"])
	}
	// Segment 1: bold annotation must round-trip.
	if _, has := got[1]["annotations"]; !has {
		t.Error("segment 1 missing annotations")
	}
	// Segment 2: mention type auto-filled.
	if got[2]["type"] != "mention" {
		t.Errorf("segment 2 type = %v, want mention", got[2]["type"])
	}
	if _, has := got[2]["mention"]; !has {
		t.Error("segment 2 missing mention payload")
	}
	// Segment 3: equation type auto-filled.
	if got[3]["type"] != "equation" {
		t.Errorf("segment 3 type = %v, want equation", got[3]["type"])
	}

	// Round-trip the converted slice through JSON just to assert the
	// shape encodes cleanly.
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal(api shape): %v", err)
	}
}

// TestRichText_HasAnnotations covers the helper directly.
func TestRichText_HasAnnotations(t *testing.T) {
	tests := []struct {
		name string
		ann  Annotation
		want bool
	}{
		{"zero value", Annotation{}, false},
		{"default color only", Annotation{Color: "default"}, false},
		{"bold", Annotation{Bold: true}, true},
		{"italic", Annotation{Italic: true}, true},
		{"strike", Annotation{Strikethrough: true}, true},
		{"underline", Annotation{Underline: true}, true},
		{"code", Annotation{Code: true}, true},
		{"non-default color", Annotation{Color: "red"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAnnotations(tt.ann); got != tt.want {
				t.Errorf("hasAnnotations(%+v) = %v, want %v", tt.ann, got, tt.want)
			}
		})
	}
}
