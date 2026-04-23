// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBlockRichText_GetBlockContent_MultiSegment asserts GetBlockContent
// now returns every segment (with annotations applied), not just the
// first PlainText — which was the pre-#27 behaviour.
func TestBlockRichText_GetBlockContent_MultiSegment(t *testing.T) {
	withNoColor(t)

	block := Block{
		Type: "paragraph",
		Paragraph: &RichTextBlock{
			RichText: []RichText{
				{PlainText: "hello "},
				{PlainText: "world", Annotations: Annotation{Bold: true}},
				{PlainText: " "},
				{PlainText: "code", Annotations: Annotation{Code: true}},
			},
		},
	}
	got := GetBlockContent(block)
	want := "hello world `code`"
	if got != want {
		t.Errorf("GetBlockContent() = %q, want %q", got, want)
	}
}

// TestBlockRichText_GetBlockContentPlain asserts the plain variant
// strips the backtick wrap for code runs and applies no annotations —
// it is the stable JSON-path string.
func TestGetBlockContentPlain(t *testing.T) {
	block := Block{
		Type: "to_do",
		ToDo: &ToDo{
			RichText: []RichText{
				{PlainText: "buy "},
				{PlainText: "milk", Annotations: Annotation{Bold: true, Code: true}},
			},
		},
	}
	if got := GetBlockContentPlain(block); got != "buy milk" {
		t.Errorf("GetBlockContentPlain() = %q, want %q", got, "buy milk")
	}
}

// TestBlockRichText_GetBlockContent_Mention asserts a mention segment
// renders as its marker in GetBlockContent.
func TestBlockRichText_GetBlockContent_Mention(t *testing.T) {
	withNoColor(t)

	block := Block{
		Type: "paragraph",
		Paragraph: &RichTextBlock{
			RichText: []RichText{
				{PlainText: "ping "},
				{Type: "mention", Mention: &Mention{Type: "user", User: &User{Name: "Jordan"}}},
			},
		},
	}
	if got := GetBlockContent(block); got != "ping @Jordan" {
		t.Errorf("GetBlockContent() = %q, want %q", got, "ping @Jordan")
	}
}

// TestBlockRichText_GetBlockContent_Equation asserts an inline equation
// segment renders with "$...$" delimiters.
func TestBlockRichText_GetBlockContent_Equation(t *testing.T) {
	withNoColor(t)

	block := Block{
		Type: "paragraph",
		Paragraph: &RichTextBlock{
			RichText: []RichText{
				{PlainText: "Euler: "},
				{Type: "equation", Equation: &TextEquation{Expression: "e^{i\\pi}+1=0"}},
			},
		},
	}
	want := "Euler: $e^{i\\pi}+1=0$"
	if got := GetBlockContent(block); got != want {
		t.Errorf("GetBlockContent() = %q, want %q", got, want)
	}
}

// TestBlockRichText_GetBlockContent_Empty asserts the empty-runs path
// still returns "(empty)" and divider still returns its ruler.
func TestBlockRichText_GetBlockContent_Empty(t *testing.T) {
	tests := []struct {
		name  string
		block Block
		want  string
	}{
		{"empty paragraph", Block{Type: "paragraph", Paragraph: &RichTextBlock{}}, "(empty)"},
		{"nil paragraph ptr", Block{Type: "paragraph"}, "(empty)"},
		{"divider", Block{Type: "divider", Divider: &struct{}{}}, "───────────"},
		{"unknown type", Block{Type: "image"}, "(empty)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetBlockContent(tt.block); got != tt.want {
				t.Errorf("GetBlockContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBlockRichText_AnnotationRoundTrip verifies that a block fetched
// over the HTTP mock preserves every annotation field through JSON
// marshal/unmarshal — this is the invariant that `blocks list --json`
// relies on.
func TestBlockRichText_AnnotationRoundTrip(t *testing.T) {
	want := RichText{
		Type:      "text",
		PlainText: "hello",
		Text:      Text{Content: "hello"},
		Annotations: Annotation{
			Bold:          true,
			Italic:        true,
			Strikethrough: true,
			Underline:     true,
			Code:          true,
			Color:         "red",
		},
	}
	wantBlock := Block{
		Object:    "block",
		ID:        "annotated-1",
		Type:      "paragraph",
		Paragraph: &RichTextBlock{RichText: []RichText{want}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/blocks/annotatedPage/children" {
			_ = json.NewEncoder(w).Encode(BlockList{Results: []Block{wantBlock}})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	got, err := GetAllBlocks("fakeKey", "annotatedPage", "")
	if err != nil {
		t.Fatalf("GetAllBlocks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	seg := got[0].Paragraph.RichText[0]
	if seg.Annotations != want.Annotations {
		t.Errorf("annotations = %+v, want %+v", seg.Annotations, want.Annotations)
	}
	// And re-encode through json.Encoder — the round-trip must retain
	// every flag so `blocks list --json` emits them.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(got[0]); err != nil {
		t.Fatalf("json encode: %v", err)
	}
	wire := buf.String()
	for _, needle := range []string{
		`"bold":true`, `"italic":true`, `"strikethrough":true`,
		`"underline":true`, `"code":true`, `"color":"red"`,
	} {
		if !strings.Contains(wire, needle) {
			t.Errorf("JSON wire %q missing %q", wire, needle)
		}
	}
}

// TestBlockRichText_MentionRoundTrip verifies a mention-bearing block
// survives JSON decode with every nested field intact.
func TestBlockRichText_MentionRoundTrip(t *testing.T) {
	raw := `{
		"object":"block","id":"m-1","type":"paragraph",
		"paragraph":{"rich_text":[
			{"type":"text","text":{"content":"Assigned to "},"plain_text":"Assigned to ","annotations":{"bold":false,"code":false,"color":"default","italic":false,"strikethrough":false,"underline":false}},
			{"type":"mention","mention":{"type":"user","user":{"object":"user","id":"u-1","name":"Jordan"}},"plain_text":"@Jordan","annotations":{"bold":false,"code":false,"color":"default","italic":false,"strikethrough":false,"underline":false}},
			{"type":"equation","equation":{"expression":"E=mc^2"},"plain_text":"E=mc^2","annotations":{"bold":false,"code":false,"color":"default","italic":false,"strikethrough":false,"underline":false}}
		]}
	}`
	var block Block
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rt := block.Paragraph.RichText
	if len(rt) != 3 {
		t.Fatalf("len = %d, want 3", len(rt))
	}
	if rt[1].Mention == nil || rt[1].Mention.User == nil || rt[1].Mention.User.Name != "Jordan" {
		t.Errorf("mention user lost: %+v", rt[1])
	}
	if rt[2].Equation == nil || rt[2].Equation.Expression != "E=mc^2" {
		t.Errorf("equation lost: %+v", rt[2])
	}
}

// TestBlockRichText_AddRichTextBlock exercises the write path: the
// payload we PATCH must preserve annotations and multi-segment shape.
func TestAddRichTextBlock(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/blocks/writePage/children" {
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	rt := []RichText{
		{Type: "text", Text: Text{Content: "hello "}, Annotations: Annotation{Bold: true}},
		{Type: "mention", Mention: &Mention{Type: "user", User: &User{ID: "u-1", Name: "J"}}},
	}
	if err := NewBlockClient(NewClient("k", WithBaseURL(srv.URL))).
		AddRichTextBlock(context.Background(), "writePage", "paragraph", rt); err != nil {
		t.Fatalf("AddRichTextBlock: %v", err)
	}

	// Parse the outbound body back and assert both segments are present.
	var sent struct {
		Children []struct {
			Type      string                 `json:"type"`
			Paragraph map[string]interface{} `json:"paragraph"`
		} `json:"children"`
	}
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("unmarshal outbound body: %v (body=%s)", err, gotBody)
	}
	if len(sent.Children) != 1 || sent.Children[0].Type != "paragraph" {
		t.Fatalf("unexpected outbound children: %+v", sent.Children)
	}
	rtOut, ok := sent.Children[0].Paragraph["rich_text"].([]interface{})
	if !ok || len(rtOut) != 2 {
		t.Fatalf("outbound rich_text = %v", sent.Children[0].Paragraph["rich_text"])
	}
	// Segment 0: text with bold annotation.
	seg0 := rtOut[0].(map[string]interface{})
	if seg0["type"] != "text" {
		t.Errorf("seg0 type = %v, want text", seg0["type"])
	}
	ann0, ok := seg0["annotations"].(map[string]interface{})
	if !ok || ann0["bold"] != true {
		t.Errorf("seg0 annotations = %v, want bold=true", seg0["annotations"])
	}
	// Segment 1: mention.
	seg1 := rtOut[1].(map[string]interface{})
	if seg1["type"] != "mention" {
		t.Errorf("seg1 type = %v, want mention", seg1["type"])
	}
}

// TestBlockRichText_AddRichTextBlock_Errors covers the validation paths.
func TestAddRichTextBlock_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	bc := NewBlockClient(NewClient("k", WithBaseURL(srv.URL)))
	tests := []struct {
		name      string
		blockType string
		rt        []RichText
		wantSub   string
	}{
		{"unsupported type", "bogus", []RichText{{Text: Text{Content: "x"}}}, "unsupported block type"},
		{"divider rejected", "divider", []RichText{{Text: Text{Content: "x"}}}, "divider blocks do not accept"},
		{"empty rt", "paragraph", nil, "at least one segment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bc.AddRichTextBlock(context.Background(), "page", tt.blockType, tt.rt)
			if err == nil {
				t.Fatalf("AddRichTextBlock err = nil, want error containing %q", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

// TestBlockRichText_AddRichTextBlockDelegate exercises the top-level
// (deprecated) AddRichTextBlock wrapper so the coverage tool sees it.
func TestAddRichTextBlock_Delegate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	prev := baseURL
	SetBaseURL(srv.URL)
	defer SetBaseURL(prev)

	rt := []RichText{{Type: "text", Text: Text{Content: "hi"}}}
	if err := AddRichTextBlock("k", "pageID", "paragraph", rt); err != nil {
		t.Fatalf("AddRichTextBlock(top-level) err = %v", err)
	}
}
