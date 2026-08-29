// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// baseURL is the legacy package-level base URL. It is kept as the source of
// truth for the default Client so that SetBaseURL (used by tests) continues
// to redirect every legacy top-level call. New code should build its own
// *Client via NewClient and not reach for this variable.
var baseURL = "https://api.notion.com/v1"

// SetBaseURL redirects the Notion API client to an arbitrary URL. Intended
// for tests (especially the cmd/ layer, which cannot reach test-only symbols
// defined in utils/*_test.go). Production callers should not use this.
//
// Deprecated: use WithBaseURL on Client — this exists only for legacy
// cmd-layer tests and will be removed in a follow-up once cmd/ migrates
// to NewClient(WithBaseURL(...)).
func SetBaseURL(url string) {
	baseURL = url
}

// GetBaseURL returns the current Notion API base URL. Exposed only so tests
// can snapshot and restore the value around SetBaseURL calls.
//
// Deprecated: use WithBaseURL on Client — this exists only for legacy
// cmd-layer tests and will be removed in a follow-up once cmd/ migrates
// to NewClient(WithBaseURL(...)).
func GetBaseURL() string {
	return baseURL
}

// BlockTypeInfo contains display information for a block type.
type BlockTypeInfo struct {
	Icon  string
	Color string
}

// SupportedBlockTypes defines all supported block types with their display info.
var SupportedBlockTypes = map[string]BlockTypeInfo{
	"paragraph":          {Icon: "¶", Color: "white"},
	"heading_1":          {Icon: "H1", Color: "cyan"},
	"heading_2":          {Icon: "H2", Color: "cyan"},
	"heading_3":          {Icon: "H3", Color: "cyan"},
	"bulleted_list_item": {Icon: "•", Color: "white"},
	"numbered_list_item": {Icon: "#", Color: "white"},
	"to_do":              {Icon: "☐", Color: "green"},
	"toggle":             {Icon: "▸", Color: "magenta"},
	"quote":              {Icon: "❝", Color: "yellow"},
	"callout":            {Icon: "💡", Color: "yellow"},
	"divider":            {Icon: "—", Color: "white"},
	"code":               {Icon: "<>", Color: "blue"},
	// Extended block types (issue #26).
	"image":        {Icon: "🖼", Color: "magenta"},
	"file":         {Icon: "📎", Color: "magenta"},
	"video":        {Icon: "🎬", Color: "magenta"},
	"embed":        {Icon: "🔗", Color: "blue"},
	"bookmark":     {Icon: "🔖", Color: "blue"},
	"equation":     {Icon: "∑", Color: "cyan"},
	"table":        {Icon: "▦", Color: "white"},
	"table_row":    {Icon: "│", Color: "white"},
	"synced_block": {Icon: "⟳", Color: "magenta"},
	"column_list":  {Icon: "⫴", Color: "white"},
	"column":       {Icon: "│", Color: "white"},
}

// AddableBlockTypes lists block types that can be appended via the simple
// `blocks add` CLI path. Complex types (table, synced_block, column_list,
// column) require children or references and are intentionally excluded —
// CLI callers should use a JSON-payload path when that lands.
var AddableBlockTypes = map[string]bool{
	"paragraph":          true,
	"heading_1":          true,
	"heading_2":          true,
	"heading_3":          true,
	"bulleted_list_item": true,
	"numbered_list_item": true,
	"to_do":              true,
	"toggle":             true,
	"quote":              true,
	"callout":            true,
	"divider":            true,
	"code":               true,
	"image":              true,
	"file":               true,
	"video":              true,
	"embed":              true,
	"bookmark":           true,
	"equation":           true,
}

// IsAddableBlockType reports whether a block type can be appended via the
// simple AddBlock path. Types that require children (table, column_list,
// column) or cross-block references (synced_block) return false.
func IsAddableBlockType(blockType string) bool {
	return AddableBlockTypes[blockType]
}

// richTextBearingBlockTypes lists the addable block types whose body
// schema includes a `rich_text` array. AddRichTextBlock builds a payload
// of the form `<type>: {"rich_text": [...]}`, so only these types yield
// a valid Notion block. Types like image/file/video/embed/bookmark take
// a `<media>: {"url": "..."}` shape instead, and equation takes
// `equation: {"expression": "..."}` — passing rich text to those
// produces a 400 from Notion.
var richTextBearingBlockTypes = map[string]bool{
	"paragraph":          true,
	"heading_1":          true,
	"heading_2":          true,
	"heading_3":          true,
	"bulleted_list_item": true,
	"numbered_list_item": true,
	"to_do":              true,
	"toggle":             true,
	"quote":              true,
	"callout":            true,
	"code":               true,
}

// BlockTypeAcceptsRichText reports whether a block type's Notion schema
// includes a `rich_text` array, and is therefore a valid target for
// AddRichTextBlock / `blocks add --rich-text-json`. Returns false for
// divider (no body), media blocks (image/file/video/embed/bookmark —
// these take a url-bearing object instead), and equation (which takes
// an `expression` string rather than rich text).
func BlockTypeAcceptsRichText(blockType string) bool {
	return richTextBearingBlockTypes[blockType]
}

// GetSupportedBlockTypeNames returns a sorted list of supported block type names.
func GetSupportedBlockTypeNames() []string {
	names := make([]string, 0, len(SupportedBlockTypes))
	for name := range SupportedBlockTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsValidBlockType checks if a block type is supported.
func IsValidBlockType(blockType string) bool {
	_, ok := SupportedBlockTypes[blockType]
	return ok
}

// ToDo is the Notion to-do block body.
type ToDo struct {
	Checked  bool       `json:"checked"`
	Color    string     `json:"color"`
	RichText []RichText `json:"rich_text"`
}

// RichTextBlock represents a block with rich_text content.
type RichTextBlock struct {
	RichText []RichText `json:"rich_text"`
	Color    string     `json:"color,omitempty"`
	Language string     `json:"language,omitempty"` // for code blocks
	Checked  bool       `json:"checked,omitempty"`  // for to_do blocks
}

// RichText is a single rich-text run returned by the Notion API.
//
// Notion rich-text arrays carry ordered "segments": each segment has its
// own annotations (bold/italic/color/...) plus one of a handful of payload
// shapes — plain text, a mention, or an inline equation. The struct below
// models all three payload shapes as optional pointers so a given run
// round-trips whichever one was set.
type RichText struct {
	Annotations Annotation    `json:"annotations"`
	Href        interface{}   `json:"href"`
	PlainText   string        `json:"plain_text"`
	Text        Text          `json:"text"`
	Type        string        `json:"type"`
	Mention     *Mention      `json:"mention,omitempty"`
	Equation    *TextEquation `json:"equation,omitempty"`
}

// Annotation captures the Notion text-run annotation flags.
//
// Color carries the `,omitempty` tag because Notion-Version 2026-03-11
// rejects `"color": ""` outright — the field must be one of the named
// colors (default/gray/brown/...) or absent. The Go zero value is the
// empty string, so without omitempty every payload that doesn't
// explicitly set a color (including the comments-create path that
// builds rich_text from a plain --text flag) 400s with
// `body.rich_text[0].annotations.color should be "default", ... or undefined`.
// The bool fields remain present-with-false because Notion accepts
// false on each of them. See issue #49.
type Annotation struct {
	Bold          bool   `json:"bold"`
	Code          bool   `json:"code"`
	Color         string `json:"color,omitempty"`
	Italic        bool   `json:"italic"`
	Strikethrough bool   `json:"strikethrough"`
	Underline     bool   `json:"underline"`
}

// Text is the text payload inside a RichText run.
type Text struct {
	Content string      `json:"content"`
	Link    interface{} `json:"link"`
}

// TextEquation is an inline LaTeX equation payload carried by a rich-text
// run whose Type is "equation". Mirrors the Notion payload shape
// `{"expression":"E=mc^2"}`.
type TextEquation struct {
	Expression string `json:"expression"`
}

// Mention is the Notion mention payload carried by a rich-text run whose
// Type is "mention". Exactly one of User / Page / Date / Database is
// populated to match the mention's Type discriminator.
type Mention struct {
	Type     string           `json:"type"`
	User     *User            `json:"user,omitempty"`
	Page     *PageMention     `json:"page,omitempty"`
	Date     *DateMention     `json:"date,omitempty"`
	Database *DatabaseMention `json:"database,omitempty"`
}

// PageMention carries the referenced Notion page id for a page mention.
// Notion does not include the page title inline; callers that want a
// readable title must fetch the page separately.
type PageMention struct {
	ID string `json:"id"`
}

// DatabaseMention carries the referenced Notion database id for a
// database mention.
type DatabaseMention struct {
	ID string `json:"id"`
}

// DateMention is the payload for a date mention. End is optional and
// populated only when the user picked a range.
type DateMention struct {
	Start string `json:"start"`
	End   string `json:"end,omitempty"`
}

// Block is the Notion block envelope covering every supported block type.
type Block struct {
	Object           string         `json:"object"`
	ID               string         `json:"id"`
	CreatedTime      string         `json:"created_time"`
	LastEditedTime   string         `json:"last_edited_time"`
	Type             string         `json:"type"`
	HasChildren      bool           `json:"has_children"`
	ToDo             *ToDo          `json:"to_do,omitempty"`
	Paragraph        *RichTextBlock `json:"paragraph,omitempty"`
	Heading1         *RichTextBlock `json:"heading_1,omitempty"`
	Heading2         *RichTextBlock `json:"heading_2,omitempty"`
	Heading3         *RichTextBlock `json:"heading_3,omitempty"`
	BulletedListItem *RichTextBlock `json:"bulleted_list_item,omitempty"`
	NumberedListItem *RichTextBlock `json:"numbered_list_item,omitempty"`
	Toggle           *RichTextBlock `json:"toggle,omitempty"`
	Quote            *RichTextBlock `json:"quote,omitempty"`
	Callout          *RichTextBlock `json:"callout,omitempty"`
	Code             *RichTextBlock `json:"code,omitempty"`
	Divider          *struct{}      `json:"divider,omitempty"`

	// Extended block types (issue #26).
	Image       *MediaBlock    `json:"image,omitempty"`
	File        *MediaBlock    `json:"file,omitempty"`
	Video       *MediaBlock    `json:"video,omitempty"`
	Embed       *EmbedBlock    `json:"embed,omitempty"`
	Bookmark    *BookmarkBlock `json:"bookmark,omitempty"`
	Equation    *EquationBlock `json:"equation,omitempty"`
	Table       *TableBlock    `json:"table,omitempty"`
	TableRow    *TableRowBlock `json:"table_row,omitempty"`
	SyncedBlock *SyncedBlock   `json:"synced_block,omitempty"`
	ColumnList  *ColumnList    `json:"column_list,omitempty"`
	Column      *Column        `json:"column,omitempty"`
}

// ExternalFile is the external-URL variant of a Notion media reference. Used
// by image/file/video blocks whose Type is "external".
type ExternalFile struct {
	URL string `json:"url"`
}

// FileUploadRef is the file_upload-id variant of a Notion media reference.
// Used by image/file/video blocks whose Type is "file_upload".
type FileUploadRef struct {
	ID string `json:"id"`
}

// MediaBlock is the shared shape for image, file, and video blocks. Exactly
// one of External or FileUpload is set depending on Type ("external" or
// "file_upload"). Caption is optional.
type MediaBlock struct {
	Type       string         `json:"type"`
	External   *ExternalFile  `json:"external,omitempty"`
	FileUpload *FileUploadRef `json:"file_upload,omitempty"`
	Caption    []RichText     `json:"caption,omitempty"`
}

// EmbedBlock is a generic embed (Miro, Twitter, etc.) addressed by URL.
type EmbedBlock struct {
	URL     string     `json:"url"`
	Caption []RichText `json:"caption,omitempty"`
}

// BookmarkBlock is a URL bookmark rendered inline on a page.
type BookmarkBlock struct {
	URL     string     `json:"url"`
	Caption []RichText `json:"caption,omitempty"`
}

// EquationBlock carries a LaTeX-style math expression. The expression is
// always present; there is no caption.
type EquationBlock struct {
	Expression string `json:"expression"`
}

// TableBlock describes the table envelope; the actual cell content lives in
// the table's child blocks (each a TableRowBlock).
type TableBlock struct {
	TableWidth      int  `json:"table_width"`
	HasColumnHeader bool `json:"has_column_header"`
	HasRowHeader    bool `json:"has_row_header"`
}

// TableRowBlock is a single row of a Notion table. Cells is a slice of
// cells; each cell is a slice of rich-text runs.
type TableRowBlock struct {
	Cells [][]RichText `json:"cells"`
}

// SyncedFromRef identifies the original block that a synced copy mirrors.
// When nil on a SyncedBlock, the block is itself the original.
type SyncedFromRef struct {
	BlockID string `json:"block_id"`
}

// SyncedBlock is a shared-content block. If SyncedFrom is nil this is the
// original; otherwise this block mirrors the referenced block id.
type SyncedBlock struct {
	SyncedFrom *SyncedFromRef `json:"synced_from"`
}

// ColumnList is the container for a multi-column layout. Columns live as
// child blocks.
type ColumnList struct{}

// Column is a single column within a ColumnList. Its own children are the
// blocks rendered in that column.
type Column struct{}

// BlockList is a single page of block results, as returned by
// /blocks/{id}/children.
type BlockList struct {
	Object          string   `json:"object"`
	Results         []Block  `json:"results"`
	NextCursor      string   `json:"next_cursor"`
	HasMore         bool     `json:"has_more"`
	Type            string   `json:"type"`
	Block           struct{} `json:"block"`
	DeveloperSurvey string   `json:"developer_survey"`
}

// defaultBlockClient returns a BlockClient configured with the caller's
// apiKey and the current package-level baseURL.
//
// A fresh *Client is allocated on every call rather than cached as a
// package-level singleton, and that is intentional for now: the legacy
// top-level entry points accept apiKey per call, and baseURL is mutable
// via SetBaseURL (tests today, prod after PR #16). Caching without
// reconciling both of those would either break httptest wiring or silently
// mix credentials across calls. Once #16 lands and SetBaseURL is promoted
// to a proper Client option, this can collapse to a sync.Once-guarded
// singleton. Tracked alongside the #1 follow-ups.
func defaultBlockClient(apiKey string) *BlockClient {
	return NewBlockClient(NewClient(apiKey, WithBaseURL(baseURL)))
}

// GetBlocks delegates to BlockClient.GetBlocks on a default client.
//
// Deprecated: new callers should build a *Client and *BlockClient via
// NewClient/NewBlockClient and pass context.Context explicitly.
func GetBlocks(notionAPIKey, pageID string) ([]Block, error) {
	return defaultBlockClient(notionAPIKey).GetBlocks(defaultCtx(), pageID)
}

// GetToDoBlocks delegates to BlockClient.GetToDoBlocks on a default client.
//
// Deprecated: prefer BlockClient.GetToDoBlocks.
func GetToDoBlocks(notionAPIKey, blockID string, localTimezone *time.Location) ([]string, error) {
	return defaultBlockClient(notionAPIKey).GetToDoBlocks(defaultCtx(), blockID, localTimezone)
}

// GetVisibleToDoBlocks delegates to BlockClient.GetVisibleToDoBlocks on a
// default client. Returns the to-do blocks that the human `list` and
// `list --json` commands surface — type-filtered AND non-empty rich_text.
//
// Deprecated: prefer BlockClient.GetVisibleToDoBlocks.
func GetVisibleToDoBlocks(notionAPIKey, pageID string) ([]Block, error) {
	return defaultBlockClient(notionAPIKey).GetVisibleToDoBlocks(defaultCtx(), pageID)
}

// AddNewToDoItem delegates to BlockClient.AddNewToDoItem on a default client.
//
// Deprecated: prefer BlockClient.AddNewToDoItem.
func AddNewToDoItem(notionAPIKey, pageID, text string) error {
	return defaultBlockClient(notionAPIKey).AddNewToDoItem(defaultCtx(), pageID, text)
}

// GetBlockID delegates to BlockClient.GetBlockID on a default client.
//
// Deprecated: prefer BlockClient.GetBlockID.
func GetBlockID(notionAPIKey, pageID string, order int) (string, error) {
	return defaultBlockClient(notionAPIKey).GetBlockID(defaultCtx(), pageID, order)
}

// MarkToDoBlockChecked delegates to BlockClient.MarkToDoBlockChecked on a default client.
//
// Deprecated: prefer BlockClient.MarkToDoBlockChecked.
func MarkToDoBlockChecked(notionAPIKey, pageID string, order int) error {
	return defaultBlockClient(notionAPIKey).MarkToDoBlockChecked(defaultCtx(), pageID, order)
}

// MarkToDoBlockUnChecked delegates to BlockClient.MarkToDoBlockUnChecked on a default client.
//
// Deprecated: prefer BlockClient.MarkToDoBlockUnChecked.
func MarkToDoBlockUnChecked(notionAPIKey, pageID string, order int) error {
	return defaultBlockClient(notionAPIKey).MarkToDoBlockUnChecked(defaultCtx(), pageID, order)
}

// DeleteToDoBlock delegates to BlockClient.DeleteToDoBlock on a default client.
//
// Deprecated: prefer BlockClient.DeleteToDoBlock.
func DeleteToDoBlock(notionAPIKey, pageID string, order int) error {
	return defaultBlockClient(notionAPIKey).DeleteToDoBlock(defaultCtx(), pageID, order)
}

// GetAllBlocks delegates to BlockClient.GetAllBlocks on a default client.
//
// Deprecated: prefer BlockClient.GetAllBlocks.
func GetAllBlocks(notionAPIKey, pageID string, filterType string) ([]Block, error) {
	return defaultBlockClient(notionAPIKey).GetAllBlocks(defaultCtx(), pageID, filterType)
}

// FormatAllBlocks delegates to BlockClient.FormatAllBlocks on a default client.
//
// Deprecated: prefer BlockClient.FormatAllBlocks.
func FormatAllBlocks(notionAPIKey, pageID string, localTimezone *time.Location, filterType string) ([]string, map[string]int, error) {
	return defaultBlockClient(notionAPIKey).FormatAllBlocks(defaultCtx(), pageID, localTimezone, filterType)
}

// FormatAllBlocksWithResolver is FormatAllBlocks but threads the given
// PageTitleResolver into the snippet renderer so page mentions expand
// from the legacy "[page:<id>]" marker into "[<title>]". Callers that
// do not need resolution should continue to use FormatAllBlocks — the
// legacy helper passes a NoPageResolver internally and preserves the
// pre-resolver output byte-for-byte.
//
// This is a human-output affordance only. JSON paths must NOT go
// through this helper: emitting resolved titles there would be lossy
// round-tripping (the original rich_text mention shape is replaced by a
// bracketed string). Keep JSON on raw rich_text arrays.
//
// Deprecated: prefer BlockClient.FormatAllBlocksWithResolver.
func FormatAllBlocksWithResolver(notionAPIKey, pageID string, localTimezone *time.Location, filterType string, resolver PageTitleResolver) ([]string, map[string]int, error) {
	return defaultBlockClient(notionAPIKey).FormatAllBlocksWithResolver(defaultCtx(), pageID, localTimezone, filterType, resolver)
}

// AddBlock delegates to BlockClient.AddBlock on a default client. The
// variadic opts argument passes media URLs, captions, and other per-type
// metadata through to the block payload.
//
// Deprecated: prefer BlockClient.AddBlock.
func AddBlock(notionAPIKey, pageID, blockType, text string, opts ...BlockOption) error {
	return defaultBlockClient(notionAPIKey).AddBlock(defaultCtx(), pageID, blockType, text, opts...)
}

// AddRichTextBlock delegates to BlockClient.AddRichTextBlock on a default
// client. Peer to AddBlock: same (apiKey, pageID, blockType, payload)
// parameter ordering; the payload is a []RichText slice so annotations,
// mentions, inline equations, and multi-segment runs round-trip — the cmd
// layer uses it for --rich-text-json.
//
// Deprecated: introduced deprecated for symmetry with the other package-
// level delegates in this file (AddBlock, GetBlocks, ...). All of them are
// on a migration path toward the *BlockClient method form; prefer
// BlockClient.AddRichTextBlock in new code.
func AddRichTextBlock(notionAPIKey, pageID, blockType string, rt []RichText) error {
	return defaultBlockClient(notionAPIKey).AddRichTextBlock(defaultCtx(), pageID, blockType, rt)
}

// DeleteBlock delegates to BlockClient.DeleteBlock on a default client.
//
// Deprecated: prefer BlockClient.DeleteBlock.
func DeleteBlock(notionAPIKey, pageID string, order int) error {
	return defaultBlockClient(notionAPIKey).DeleteBlock(defaultCtx(), pageID, order)
}

// blockRichText returns the rich-text slice for any block that carries
// one, or nil for block types that do not (divider, etc.). Centralising
// this switch keeps GetBlockContent and GetBlockContentPlain aligned —
// any new rich-text-bearing block type added in one place lights up in
// both renderers automatically.
func blockRichText(block Block) []RichText {
	switch block.Type {
	case "to_do":
		if block.ToDo != nil {
			return block.ToDo.RichText
		}
	case "paragraph":
		if block.Paragraph != nil {
			return block.Paragraph.RichText
		}
	case "heading_1":
		if block.Heading1 != nil {
			return block.Heading1.RichText
		}
	case "heading_2":
		if block.Heading2 != nil {
			return block.Heading2.RichText
		}
	case "heading_3":
		if block.Heading3 != nil {
			return block.Heading3.RichText
		}
	case "bulleted_list_item":
		if block.BulletedListItem != nil {
			return block.BulletedListItem.RichText
		}
	case "numbered_list_item":
		if block.NumberedListItem != nil {
			return block.NumberedListItem.RichText
		}
	case "toggle":
		if block.Toggle != nil {
			return block.Toggle.RichText
		}
	case "quote":
		if block.Quote != nil {
			return block.Quote.RichText
		}
	case "callout":
		if block.Callout != nil {
			return block.Callout.RichText
		}
	case "code":
		if block.Code != nil {
			return block.Code.RichText
		}
	}
	return nil
}

// extendedBlockContent returns the human-readable one-liner for block types
// that are not rich-text-bearing (image/file/video/embed/bookmark/equation/
// table/table_row/synced_block/column_list/column). Returns the empty string
// "" to signal "not one of these types; use blockRichText instead" — callers
// must distinguish this from the genuine empty-string cases (column_list,
// column) by checking block.Type first.
//
// renderCell renders a table_row's cells. It MUST be supplied by the caller
// rather than hardcoded, because this function sits on both the ANSI path
// (GetBlockContentWithResolver) and the deliberately ANSI-free one
// (GetBlockContentPlainWithResolver, whose output FormatAllBlocks
// byte-slices at 47 bytes). Rendering cells with the annotating renderer on
// the plain path would emit an escape sequence that the truncation can cut
// mid-code, leaving the terminal stuck in the cell's formatting. Passing the
// resolver through the closure also keeps --resolve-mentions working inside
// table cells.
func extendedBlockContent(block Block, renderCell func([]RichText) string) (string, bool) {
	switch block.Type {
	case "image":
		return mediaBlockContent(block.Image), true
	case "file":
		return mediaBlockContent(block.File), true
	case "video":
		return mediaBlockContent(block.Video), true
	case "embed":
		if block.Embed != nil {
			return block.Embed.URL, true
		}
		return "(empty)", true
	case "bookmark":
		if block.Bookmark != nil {
			s := block.Bookmark.URL
			if len(block.Bookmark.Caption) > 0 {
				s += " — " + block.Bookmark.Caption[0].PlainText
			}
			return s, true
		}
		return "(empty)", true
	case "equation":
		if block.Equation != nil {
			return "$" + block.Equation.Expression + "$", true
		}
		return "(empty)", true
	case "table":
		if block.Table != nil {
			col := "no"
			if block.Table.HasColumnHeader {
				col = "yes"
			}
			row := "no"
			if block.Table.HasRowHeader {
				row = "yes"
			}
			return fmt.Sprintf("table (%d cols, %s col header, %s row header)", block.Table.TableWidth, col, row), true
		}
		return "(empty)", true
	case "table_row":
		if block.TableRow != nil {
			return formatTableRow(block.TableRow, renderCell), true
		}
		return "(empty)", true
	case "synced_block":
		if block.SyncedBlock != nil {
			if block.SyncedBlock.SyncedFrom != nil {
				return fmt.Sprintf("(synced from %s)", block.SyncedBlock.SyncedFrom.BlockID), true
			}
			return "(synced original)", true
		}
		return "(empty)", true
	case "column_list", "column":
		return "", true
	}
	return "", false
}

// GetBlockContent extracts displayable text from any block type. For
// rich-text-bearing blocks the full segment array is rendered via
// RenderRichText (annotations → ANSI, mentions → markers, equations →
// "$…$") so callers see the whole block, not just the first segment.
// fatih/color respects color.NoColor — when --json toggles that off,
// RenderRichText emits plain text without ANSI escapes.
//
// This overload keeps legacy call sites intact — it delegates to
// GetBlockContentWithResolver with a NoPageResolver which errors on every
// lookup and therefore preserves the "[page:<id>]" marker.
func GetBlockContent(block Block) string {
	return GetBlockContentWithResolver(context.Background(), block, NoPageResolver{})
}

// GetBlockContentWithResolver is GetBlockContent but routes page mentions
// through the supplied PageTitleResolver so "[page:<id>]" can be
// expanded to "[<title>]". Semantics match RenderRichTextWithResolver
// for page mentions; non-rich-text block types (divider, media, table,
// etc.) are unaffected by the resolver.
func GetBlockContentWithResolver(ctx context.Context, block Block, resolver PageTitleResolver) string {
	if block.Type == "divider" {
		return "───────────"
	}
	// Table cells render with the same annotating renderer this function
	// uses for every other block type, and with the caller's resolver.
	renderCell := func(cell []RichText) string {
		return RenderRichTextWithResolver(ctx, cell, resolver)
	}
	if s, ok := extendedBlockContent(block, renderCell); ok {
		return s
	}
	rt := blockRichText(block)
	if len(rt) == 0 {
		return "(empty)"
	}
	return RenderRichTextWithResolver(ctx, rt, resolver)
}

// GetBlockContentPlain returns the block's text with no annotations,
// mention markers, or equation delimiters applied — just the concatenated
// PlainText of every segment. Use this from tests and JSON paths that
// want a stable string and don't care about the visual rendering.
//
// This overload keeps legacy call sites intact — it delegates to
// GetBlockContentPlainWithResolver with a NoPageResolver which errors on
// every lookup and therefore preserves the "[page:<id>]" marker.
func GetBlockContentPlain(block Block) string {
	return GetBlockContentPlainWithResolver(context.Background(), block, NoPageResolver{})
}

// GetBlockContentPlainWithResolver is GetBlockContentPlain but routes
// page mentions through the supplied PageTitleResolver so "[page:<id>]"
// can be expanded to "[<title>]". Still emits an ANSI-free string so the
// snippet truncation in FormatAllBlocks stays byte-slice safe.
func GetBlockContentPlainWithResolver(ctx context.Context, block Block, resolver PageTitleResolver) string {
	if block.Type == "divider" {
		return "───────────"
	}
	// Table cells must use the ANSI-free renderer here. FormatAllBlocks
	// truncates this function's output with a 47-byte slice, which would
	// cut an escape sequence in half — see extendedBlockContent's godoc.
	renderCell := func(cell []RichText) string {
		return PlainRichTextWithResolver(ctx, cell, resolver)
	}
	if s, ok := extendedBlockContent(block, renderCell); ok {
		return s
	}
	rt := blockRichText(block)
	if len(rt) == 0 {
		return "(empty)"
	}
	return PlainRichTextWithResolver(ctx, rt, resolver)
}

// mediaBlockContent returns a human-readable one-liner for a MediaBlock.
// External URLs are emitted verbatim; file_upload references render as
// "[uploaded file <id>]". A nil pointer yields the generic "(empty)" used
// elsewhere in GetBlockContent.
func mediaBlockContent(m *MediaBlock) string {
	if m == nil {
		return "(empty)"
	}
	switch m.Type {
	case "external":
		if m.External != nil {
			return m.External.URL
		}
	case "file_upload":
		if m.FileUpload != nil {
			return "[uploaded file " + m.FileUpload.ID + "]"
		}
	}
	return "(empty)"
}

// formatTableRow joins a row's cells with " | ". Each cell is rendered
// with the caller-supplied renderCell so every run survives intact. Empty
// cells render as an empty string, preserving column positions.
//
// renderCell is a parameter rather than a hardcoded RenderRichText so the
// ANSI and ANSI-free callers each get their own renderer; see
// extendedBlockContent's godoc for why that distinction is load-bearing.
//
// Previously this took only cell[0].PlainText, so a cell like
// "Project: **Q2 Plan**" (three runs) silently truncated to "Project: "
// — issue #69.
func formatTableRow(row *TableRowBlock, renderCell func([]RichText) string) string {
	parts := make([]string, 0, len(row.Cells))
	for _, cell := range row.Cells {
		parts = append(parts, renderCell(cell))
	}
	return "[ " + strings.Join(parts, " | ") + " ]"
}

// GetBlockIcon returns the display icon for a block.
func GetBlockIcon(block Block) string {
	// Special case for to_do: show checked/unchecked.
	if block.Type == "to_do" && block.ToDo != nil {
		if block.ToDo.Checked {
			return "☑"
		}
		return "☐"
	}
	if info, ok := SupportedBlockTypes[block.Type]; ok {
		return info.Icon
	}
	return "?"
}
