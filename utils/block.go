// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"sort"
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
type RichText struct {
	Annotations Annotation  `json:"annotations"`
	Href        interface{} `json:"href"`
	PlainText   string      `json:"plain_text"`
	Text        Text        `json:"text"`
	Type        string      `json:"type"`
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
}

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

// defaultBlockClient returns a BlockClient bound to the default client, but
// reconfigured to the latest baseURL / apiKey so existing top-level calls
// that take apiKey arguments keep working.
func defaultBlockClient(apiKey string) *BlockClient {
	// Clone the default client with a per-call apiKey; baseURL follows the
	// package-level variable so SetBaseURL continues to redirect tests.
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

// AddBlock delegates to BlockClient.AddBlock on a default client.
//
// Deprecated: prefer BlockClient.AddBlock.
func AddBlock(notionAPIKey, pageID, blockType, text string) error {
	return defaultBlockClient(notionAPIKey).AddBlock(defaultCtx(), pageID, blockType, text)
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
	}
	return "(empty)"
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
