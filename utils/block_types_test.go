// Tests for the extended block types added by issue #26: image, file,
// video, embed, bookmark, equation, table, table_row, synced_block,
// column_list, column. Lives in a dedicated file so it stays out of #27's
// rich-text fidelity work.

package utils

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------- GetBlockContent coverage ----------

func TestBlockTypes_GetBlockContent(t *testing.T) {
	tests := []struct {
		name  string
		block Block
		want  string
	}{
		{
			name: "image external",
			block: Block{
				Type:  "image",
				Image: &MediaBlock{Type: "external", External: &ExternalFile{URL: "https://example.com/pic.png"}},
			},
			want: "https://example.com/pic.png",
		},
		{
			name: "image file_upload",
			block: Block{
				Type:  "image",
				Image: &MediaBlock{Type: "file_upload", FileUpload: &FileUploadRef{ID: "upload-1"}},
			},
			want: "[uploaded file upload-1]",
		},
		{
			name:  "image nil payload",
			block: Block{Type: "image"},
			want:  "(empty)",
		},
		{
			name: "file external",
			block: Block{
				Type: "file",
				File: &MediaBlock{Type: "external", External: &ExternalFile{URL: "https://example.com/x.pdf"}},
			},
			want: "https://example.com/x.pdf",
		},
		{
			name: "video external",
			block: Block{
				Type:  "video",
				Video: &MediaBlock{Type: "external", External: &ExternalFile{URL: "https://example.com/v.mp4"}},
			},
			want: "https://example.com/v.mp4",
		},
		{
			name: "embed",
			block: Block{
				Type:  "embed",
				Embed: &EmbedBlock{URL: "https://twitter.com/x"},
			},
			want: "https://twitter.com/x",
		},
		{
			name: "bookmark no caption",
			block: Block{
				Type:     "bookmark",
				Bookmark: &BookmarkBlock{URL: "https://example.com"},
			},
			want: "https://example.com",
		},
		{
			name: "bookmark with caption",
			block: Block{
				Type: "bookmark",
				Bookmark: &BookmarkBlock{
					URL:     "https://example.com",
					Caption: []RichText{{PlainText: "the site"}},
				},
			},
			want: "https://example.com — the site",
		},
		{
			name: "equation",
			block: Block{
				Type:     "equation",
				Equation: &EquationBlock{Expression: "E=mc^2"},
			},
			want: "$E=mc^2$",
		},
		{
			name: "table with column header only",
			block: Block{
				Type:  "table",
				Table: &TableBlock{TableWidth: 3, HasColumnHeader: true},
			},
			want: "table (3 cols, yes col header, no row header)",
		},
		{
			name: "table with no headers",
			block: Block{
				Type:  "table",
				Table: &TableBlock{TableWidth: 2, HasColumnHeader: false},
			},
			want: "table (2 cols, no col header, no row header)",
		},
		{
			name: "table with both headers",
			block: Block{
				Type:  "table",
				Table: &TableBlock{TableWidth: 4, HasColumnHeader: true, HasRowHeader: true},
			},
			want: "table (4 cols, yes col header, yes row header)",
		},
		{
			name: "table with row header only",
			block: Block{
				Type:  "table",
				Table: &TableBlock{TableWidth: 2, HasColumnHeader: false, HasRowHeader: true},
			},
			want: "table (2 cols, no col header, yes row header)",
		},
		{
			name: "table_row",
			block: Block{
				Type: "table_row",
				TableRow: &TableRowBlock{
					Cells: [][]RichText{
						{{PlainText: "a"}},
						{{PlainText: "b"}},
						{{PlainText: "c"}},
					},
				},
			},
			want: "[ a | b | c ]",
		},
		{
			name: "table_row empty cell",
			block: Block{
				Type: "table_row",
				TableRow: &TableRowBlock{
					Cells: [][]RichText{
						{{PlainText: "a"}},
						{},
						{{PlainText: "c"}},
					},
				},
			},
			want: "[ a |  | c ]",
		},
		{
			name: "synced_block original",
			block: Block{
				Type:        "synced_block",
				SyncedBlock: &SyncedBlock{SyncedFrom: nil},
			},
			want: "(synced original)",
		},
		{
			name: "synced_block reference",
			block: Block{
				Type: "synced_block",
				SyncedBlock: &SyncedBlock{
					SyncedFrom: &SyncedFromRef{BlockID: "orig-id"},
				},
			},
			want: "(synced from orig-id)",
		},
		{
			name:  "column_list",
			block: Block{Type: "column_list", ColumnList: &ColumnList{}},
			want:  "",
		},
		{
			name:  "column",
			block: Block{Type: "column", Column: &Column{}},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetBlockContent(tt.block)
			if got != tt.want {
				t.Errorf("GetBlockContent(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// ---------- GetBlockIcon coverage ----------

func TestBlockTypes_GetBlockIcon(t *testing.T) {
	tests := []struct {
		typ  string
		want string
	}{
		{"image", "🖼"},
		{"file", "📎"},
		{"video", "🎬"},
		{"embed", "🔗"},
		{"bookmark", "🔖"},
		{"equation", "∑"},
		{"table", "▦"},
		{"table_row", "│"},
		{"synced_block", "⟳"},
		{"column_list", "⫴"},
		{"column", "│"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			got := GetBlockIcon(Block{Type: tt.typ})
			if got != tt.want {
				t.Errorf("GetBlockIcon(%s) = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

// ---------- IsAddableBlockType ----------

func TestBlockTypes_IsAddableBlockType(t *testing.T) {
	addable := []string{
		"paragraph", "heading_1", "heading_2", "heading_3",
		"bulleted_list_item", "numbered_list_item", "to_do",
		"toggle", "quote", "callout", "divider", "code",
		"image", "file", "video", "embed", "bookmark", "equation",
	}
	notAddable := []string{"table", "table_row", "synced_block", "column_list", "column"}

	for _, typ := range addable {
		if !IsAddableBlockType(typ) {
			t.Errorf("IsAddableBlockType(%q) = false, want true", typ)
		}
	}
	for _, typ := range notAddable {
		if IsAddableBlockType(typ) {
			t.Errorf("IsAddableBlockType(%q) = true, want false", typ)
		}
	}
	if IsAddableBlockType("not_a_type") {
		t.Error("IsAddableBlockType(not_a_type) = true, want false")
	}
}

// ---------- buildAddBlockPayload unit tests (no HTTP) ----------

func TestBlockTypes_BuildPayload(t *testing.T) {
	tests := []struct {
		name      string
		blockType string
		text      string
		cfg       blockConfig
		wantErr   bool
		verify    func(t *testing.T, payload map[string]interface{})
	}{
		{
			name:      "image external via text",
			blockType: "image",
			text:      "https://example.com/p.png",
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["image"].(map[string]interface{})
				if inner["type"] != "external" {
					t.Errorf("type = %v, want external", inner["type"])
				}
				ext := inner["external"].(map[string]interface{})
				if ext["url"] != "https://example.com/p.png" {
					t.Errorf("url = %v", ext["url"])
				}
			},
		},
		{
			name:      "image external prefers --url over text",
			blockType: "image",
			text:      "fallback",
			cfg:       blockConfig{URL: "https://example.com/real.png"},
			verify: func(t *testing.T, p map[string]interface{}) {
				ext := p["image"].(map[string]interface{})["external"].(map[string]interface{})
				if ext["url"] != "https://example.com/real.png" {
					t.Errorf("url = %v want --url value", ext["url"])
				}
			},
		},
		{
			name:      "image file_upload",
			blockType: "image",
			text:      "",
			cfg:       blockConfig{FileID: "up-1"},
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["image"].(map[string]interface{})
				if inner["type"] != "file_upload" {
					t.Errorf("type = %v, want file_upload", inner["type"])
				}
				fu := inner["file_upload"].(map[string]interface{})
				if fu["id"] != "up-1" {
					t.Errorf("id = %v", fu["id"])
				}
				if _, has := inner["external"]; has {
					t.Error("external key must be absent for file_upload variant")
				}
			},
		},
		{
			name:      "image with caption",
			blockType: "image",
			text:      "https://example.com/p.png",
			cfg:       blockConfig{Caption: "pic"},
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["image"].(map[string]interface{})
				caps := inner["caption"].([]map[string]interface{})
				if len(caps) != 1 {
					t.Fatalf("want 1 caption run, got %d", len(caps))
				}
				if caps[0]["text"].(map[string]interface{})["content"] != "pic" {
					t.Error("caption text not propagated")
				}
			},
		},
		{
			name:      "image missing url and file id errors",
			blockType: "image",
			text:      "",
			wantErr:   true,
		},
		{
			name:      "file external",
			blockType: "file",
			text:      "https://example.com/x.pdf",
			verify: func(t *testing.T, p map[string]interface{}) {
				if p["type"] != "file" {
					t.Error("envelope type not file")
				}
			},
		},
		{
			name:      "video external",
			blockType: "video",
			text:      "https://example.com/v.mp4",
			verify: func(t *testing.T, p map[string]interface{}) {
				if p["type"] != "video" {
					t.Error("envelope type not video")
				}
			},
		},
		{
			name:      "embed",
			blockType: "embed",
			text:      "https://example.com",
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["embed"].(map[string]interface{})
				if inner["url"] != "https://example.com" {
					t.Errorf("embed url = %v", inner["url"])
				}
			},
		},
		{
			name:      "embed missing url errors",
			blockType: "embed",
			text:      "",
			wantErr:   true,
		},
		{
			name:      "bookmark with caption",
			blockType: "bookmark",
			text:      "https://example.com",
			cfg:       blockConfig{Caption: "home"},
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["bookmark"].(map[string]interface{})
				if inner["url"] != "https://example.com" {
					t.Errorf("bookmark url = %v", inner["url"])
				}
				caps := inner["caption"].([]map[string]interface{})
				if len(caps) != 1 {
					t.Fatalf("want 1 caption run, got %d", len(caps))
				}
			},
		},
		{
			name:      "bookmark missing url errors",
			blockType: "bookmark",
			text:      "",
			wantErr:   true,
		},
		{
			name:      "equation",
			blockType: "equation",
			text:      "a^2+b^2=c^2",
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["equation"].(map[string]interface{})
				if inner["expression"] != "a^2+b^2=c^2" {
					t.Errorf("expression = %v", inner["expression"])
				}
			},
		},
		{
			name:      "equation missing expression errors",
			blockType: "equation",
			text:      "",
			wantErr:   true,
		},
		{
			name:      "paragraph retains rich-text shape",
			blockType: "paragraph",
			text:      "hi",
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["paragraph"].(map[string]interface{})
				rt := inner["rich_text"].([]map[string]interface{})
				if rt[0]["text"].(map[string]interface{})["content"] != "hi" {
					t.Error("rich_text content not propagated")
				}
			},
		},
		{
			name:      "code with language option",
			blockType: "code",
			text:      "fmt.Println(1)",
			cfg:       blockConfig{Language: "go"},
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["code"].(map[string]interface{})
				if inner["language"] != "go" {
					t.Errorf("language = %v want go", inner["language"])
				}
			},
		},
		{
			name:      "code default language",
			blockType: "code",
			text:      "x",
			verify: func(t *testing.T, p map[string]interface{}) {
				inner := p["code"].(map[string]interface{})
				if inner["language"] != "plain text" {
					t.Errorf("language = %v want plain text", inner["language"])
				}
			},
		},
		{
			name:      "divider ignores text",
			blockType: "divider",
			text:      "ignored",
			verify: func(t *testing.T, p map[string]interface{}) {
				if _, ok := p["divider"].(map[string]interface{}); !ok {
					t.Error("divider payload shape wrong")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildAddBlockPayload(tt.blockType, tt.text, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got["object"] != "block" {
				t.Errorf("envelope object = %v, want block", got["object"])
			}
			if got["type"] != tt.blockType {
				t.Errorf("envelope type = %v, want %s", got["type"], tt.blockType)
			}
			if tt.verify != nil {
				tt.verify(t, got)
			}
		})
	}
}

// ---------- AddBlock end-to-end per type ----------

// addBlockMock returns a server that accepts any PATCH to
// /blocks/.../children and captures the JSON body, so tests can assert the
// wire shape for each extended type.
func addBlockMock(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/children") {
			body, _ := io.ReadAll(r.Body)
			*captured = body
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		http.Error(w, `{"object":"error"}`, http.StatusNotFound)
	}))
	prev := baseURL
	SetBaseURL(srv.URL)
	t.Cleanup(func() {
		SetBaseURL(prev)
		srv.Close()
	})
	return srv
}

// decodeFirstChild parses the captured AddBlock PATCH body and returns the
// single child envelope — every AddBlock call sends exactly one.
func decodeFirstChild(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var parsed struct {
		Children []map[string]interface{} `json:"children"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(parsed.Children) != 1 {
		t.Fatalf("want 1 child in payload, got %d", len(parsed.Children))
	}
	return parsed.Children[0]
}

func TestAddBlock_Image(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "image", "https://example.com/p.png"); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	if child["type"] != "image" {
		t.Errorf("type = %v, want image", child["type"])
	}
	inner := child["image"].(map[string]interface{})
	if inner["type"] != "external" {
		t.Errorf("inner type = %v, want external", inner["type"])
	}
}

func TestAddBlock_ImageFileUpload(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "image", "", WithFileUploadID("uploaded-id")); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	inner := child["image"].(map[string]interface{})
	if inner["type"] != "file_upload" {
		t.Fatalf("inner type = %v, want file_upload", inner["type"])
	}
	fu := inner["file_upload"].(map[string]interface{})
	if fu["id"] != "uploaded-id" {
		t.Errorf("file_upload id = %v", fu["id"])
	}
}

func TestAddBlock_File(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "file", "https://example.com/x.pdf"); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	if child["type"] != "file" {
		t.Errorf("type = %v", child["type"])
	}
}

func TestAddBlock_Video(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "video", "https://example.com/v.mp4"); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	if child["type"] != "video" {
		t.Errorf("type = %v", child["type"])
	}
}

func TestAddBlock_Embed(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "embed", "https://twitter.com/x"); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	inner := child["embed"].(map[string]interface{})
	if inner["url"] != "https://twitter.com/x" {
		t.Errorf("url = %v", inner["url"])
	}
}

func TestAddBlock_Bookmark(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "bookmark", "https://example.com", WithCaption("home")); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	inner := child["bookmark"].(map[string]interface{})
	if inner["url"] != "https://example.com" {
		t.Errorf("url = %v", inner["url"])
	}
	cap := inner["caption"].([]interface{})
	if len(cap) != 1 {
		t.Fatalf("want 1 caption run, got %d", len(cap))
	}
}

func TestAddBlock_Equation(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	if err := AddBlock("k", "pg", "equation", "E=mc^2"); err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	child := decodeFirstChild(t, body)
	inner := child["equation"].(map[string]interface{})
	if inner["expression"] != "E=mc^2" {
		t.Errorf("expression = %v", inner["expression"])
	}
}

func TestAddBlock_RejectsNonAddableType(t *testing.T) {
	var body []byte
	addBlockMock(t, &body)
	for _, typ := range []string{"table", "table_row", "synced_block", "column_list", "column"} {
		t.Run(typ, func(t *testing.T) {
			err := AddBlock("k", "pg", typ, "x")
			if err == nil {
				t.Fatalf("AddBlock(%s) unexpectedly succeeded", typ)
			}
			if !strings.Contains(err.Error(), "not addable") {
				t.Errorf("error %v does not mention not-addable", err)
			}
		})
	}
}

// ---------- JSON round-trip for the new Block struct fields ----------

func TestBlockTypes_JSONRoundTrip(t *testing.T) {
	raw := `{
		"object":"list","has_more":false,"results":[
			{"object":"block","id":"i1","type":"image","image":{"type":"external","external":{"url":"https://example.com/p.png"},"caption":[{"type":"text","plain_text":"pic","text":{"content":"pic"}}]}},
			{"object":"block","id":"f1","type":"file","file":{"type":"file_upload","file_upload":{"id":"fu-1"}}},
			{"object":"block","id":"v1","type":"video","video":{"type":"external","external":{"url":"https://example.com/v.mp4"}}},
			{"object":"block","id":"e1","type":"embed","embed":{"url":"https://example.com"}},
			{"object":"block","id":"bk","type":"bookmark","bookmark":{"url":"https://example.com"}},
			{"object":"block","id":"eq","type":"equation","equation":{"expression":"x^2"}},
			{"object":"block","id":"tb","type":"table","table":{"table_width":2,"has_column_header":true,"has_row_header":false}},
			{"object":"block","id":"tr","type":"table_row","table_row":{"cells":[[{"type":"text","plain_text":"a","text":{"content":"a"}}],[{"type":"text","plain_text":"b","text":{"content":"b"}}]]}},
			{"object":"block","id":"sb","type":"synced_block","synced_block":{"synced_from":null}},
			{"object":"block","id":"sb2","type":"synced_block","synced_block":{"synced_from":{"block_id":"orig-id"}}},
			{"object":"block","id":"cl","type":"column_list","column_list":{}},
			{"object":"block","id":"co","type":"column","column":{}}
		]
	}`

	var list BlockList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Results) != 12 {
		t.Fatalf("want 12 blocks, got %d", len(list.Results))
	}

	checks := map[string]func(Block) bool{
		"i1": func(b Block) bool { return b.Image != nil && b.Image.External.URL == "https://example.com/p.png" },
		"f1": func(b Block) bool { return b.File != nil && b.File.FileUpload.ID == "fu-1" },
		"v1": func(b Block) bool { return b.Video != nil && b.Video.External.URL == "https://example.com/v.mp4" },
		"e1": func(b Block) bool { return b.Embed != nil && b.Embed.URL == "https://example.com" },
		"bk": func(b Block) bool { return b.Bookmark != nil && b.Bookmark.URL == "https://example.com" },
		"eq": func(b Block) bool { return b.Equation != nil && b.Equation.Expression == "x^2" },
		"tb": func(b Block) bool { return b.Table != nil && b.Table.TableWidth == 2 && b.Table.HasColumnHeader },
		"tr": func(b Block) bool { return b.TableRow != nil && len(b.TableRow.Cells) == 2 },
		"sb": func(b Block) bool { return b.SyncedBlock != nil && b.SyncedBlock.SyncedFrom == nil },
		"sb2": func(b Block) bool {
			return b.SyncedBlock != nil && b.SyncedBlock.SyncedFrom != nil && b.SyncedBlock.SyncedFrom.BlockID == "orig-id"
		},
		"cl": func(b Block) bool { return b.ColumnList != nil },
		"co": func(b Block) bool { return b.Column != nil },
	}

	byID := map[string]Block{}
	for _, b := range list.Results {
		byID[b.ID] = b
	}
	for id, check := range checks {
		b, ok := byID[id]
		if !ok {
			t.Errorf("missing block id %s after decode", id)
			continue
		}
		if !check(b) {
			t.Errorf("block id %s did not decode with the expected payload", id)
		}
	}

	// Re-marshal + decode ensures round-trip stability for the new types.
	out, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var list2 BlockList
	if err := json.Unmarshal(out, &list2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(list2.Results) != 12 {
		t.Errorf("round-trip: want 12 blocks, got %d", len(list2.Results))
	}
}

// ---------- BlockOption helpers ----------

// Gap-gate-facing names. These mirror the exported identifiers so the
// repo's scripts/check-test-coverage.sh treats them as covered.

func TestWithURL(t *testing.T) {
	cfg := blockConfig{}
	WithURL("https://x")(&cfg)
	if cfg.URL != "https://x" {
		t.Errorf("cfg.URL = %q, want https://x", cfg.URL)
	}
}

func TestWithCaption(t *testing.T) {
	cfg := blockConfig{}
	WithCaption("hi")(&cfg)
	if cfg.Caption != "hi" {
		t.Errorf("cfg.Caption = %q, want hi", cfg.Caption)
	}
}

func TestWithFileUploadID(t *testing.T) {
	cfg := blockConfig{}
	WithFileUploadID("up-1")(&cfg)
	if cfg.FileID != "up-1" {
		t.Errorf("cfg.FileID = %q, want up-1", cfg.FileID)
	}
}

func TestWithLanguage(t *testing.T) {
	cfg := blockConfig{}
	WithLanguage("go")(&cfg)
	if cfg.Language != "go" {
		t.Errorf("cfg.Language = %q, want go", cfg.Language)
	}
}

// TestIsAddableBlockType is the gap-gate mirror for IsAddableBlockType. The
// coverage checker looks for a Test<FuncName> that matches the exported
// function exactly, so this minimal smoke test satisfies the gate even though
// TestBlockTypes_IsAddableBlockType above covers the same surface in depth.
// Do not merge the two — the singular-named test is the gap-gate hook.
func TestIsAddableBlockType(t *testing.T) {
	if !IsAddableBlockType("paragraph") {
		t.Error("paragraph should be addable")
	}
	if !IsAddableBlockType("image") {
		t.Error("image should be addable (issue #26)")
	}
	if IsAddableBlockType("table") {
		t.Error("table should not be addable (needs children)")
	}
	if IsAddableBlockType("not_a_type") {
		t.Error("unknown type should not be addable")
	}
}
