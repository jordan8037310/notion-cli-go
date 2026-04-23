// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
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
type Annotation struct {
	Bold          bool   `json:"bold"`
	Code          bool   `json:"code"`
	Color         string `json:"color"`
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

// AddBlock delegates to BlockClient.AddBlock on a default client. The
// variadic opts argument passes media URLs, captions, and other per-type
// metadata through to the block payload.
//
// Deprecated: prefer BlockClient.AddBlock.
func AddBlock(notionAPIKey, pageID, blockType, text string, opts ...BlockOption) error {
	return defaultBlockClient(notionAPIKey).AddBlock(defaultCtx(), pageID, blockType, text, opts...)
}

// DeleteBlock delegates to BlockClient.DeleteBlock on a default client.
//
// Deprecated: prefer BlockClient.DeleteBlock.
func DeleteBlock(notionAPIKey, pageID string, order int) error {
	return defaultBlockClient(notionAPIKey).DeleteBlock(defaultCtx(), pageID, order)
}

// GetBlockContent extracts text content from any block type.
func GetBlockContent(block Block) string {
	switch block.Type {
	case "divider":
		return "───────────"
	case "to_do":
		if block.ToDo != nil && len(block.ToDo.RichText) > 0 {
			return block.ToDo.RichText[0].PlainText
		}
	case "paragraph":
		if block.Paragraph != nil && len(block.Paragraph.RichText) > 0 {
			return block.Paragraph.RichText[0].PlainText
		}
	case "heading_1":
		if block.Heading1 != nil && len(block.Heading1.RichText) > 0 {
			return block.Heading1.RichText[0].PlainText
		}
	case "heading_2":
		if block.Heading2 != nil && len(block.Heading2.RichText) > 0 {
			return block.Heading2.RichText[0].PlainText
		}
	case "heading_3":
		if block.Heading3 != nil && len(block.Heading3.RichText) > 0 {
			return block.Heading3.RichText[0].PlainText
		}
	case "bulleted_list_item":
		if block.BulletedListItem != nil && len(block.BulletedListItem.RichText) > 0 {
			return block.BulletedListItem.RichText[0].PlainText
		}
	case "numbered_list_item":
		if block.NumberedListItem != nil && len(block.NumberedListItem.RichText) > 0 {
			return block.NumberedListItem.RichText[0].PlainText
		}
	case "toggle":
		if block.Toggle != nil && len(block.Toggle.RichText) > 0 {
			return block.Toggle.RichText[0].PlainText
		}
	case "quote":
		if block.Quote != nil && len(block.Quote.RichText) > 0 {
			return block.Quote.RichText[0].PlainText
		}
	case "callout":
		if block.Callout != nil && len(block.Callout.RichText) > 0 {
			return block.Callout.RichText[0].PlainText
		}
	case "code":
		if block.Code != nil && len(block.Code.RichText) > 0 {
			return block.Code.RichText[0].PlainText
		}
	case "image":
		return mediaBlockContent(block.Image)
	case "file":
		return mediaBlockContent(block.File)
	case "video":
		return mediaBlockContent(block.Video)
	case "embed":
		if block.Embed != nil {
			return block.Embed.URL
		}
	case "bookmark":
		if block.Bookmark != nil {
			s := block.Bookmark.URL
			if len(block.Bookmark.Caption) > 0 {
				s += " — " + block.Bookmark.Caption[0].PlainText
			}
			return s
		}
	case "equation":
		if block.Equation != nil {
			return "$" + block.Equation.Expression + "$"
		}
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
			return fmt.Sprintf("table (%d cols, %s col header, %s row header)", block.Table.TableWidth, col, row)
		}
	case "table_row":
		if block.TableRow != nil {
			return formatTableRow(block.TableRow)
		}
	case "synced_block":
		if block.SyncedBlock != nil {
			if block.SyncedBlock.SyncedFrom != nil {
				return fmt.Sprintf("(synced from %s)", block.SyncedBlock.SyncedFrom.BlockID)
			}
			return "(synced original)"
		}
	case "column_list", "column":
		return ""
	}
	return "(empty)"
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

// formatTableRow joins a row's cells with " | ", taking the first plain-text
// run of each cell. Empty cells render as an empty string, preserving column
// positions in the output.
func formatTableRow(row *TableRowBlock) string {
	parts := make([]string, 0, len(row.Cells))
	for _, cell := range row.Cells {
		text := ""
		if len(cell) > 0 {
			text = cell[0].PlainText
		}
		parts = append(parts, text)
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
