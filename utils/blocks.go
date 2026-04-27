// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// BlockClient provides typed access to the Notion blocks API. Construct one
// with NewBlockClient and pass it into callers that need block operations.
// Every method takes a context.Context so requests can be cancelled or
// deadlined from the caller.
type BlockClient struct {
	c *Client
}

// NewBlockClient wraps a *Client with block-resource methods.
func NewBlockClient(c *Client) *BlockClient {
	return &BlockClient{c: c}
}

// GetBlocks returns to-do blocks (and only to-dos with non-empty rich text)
// for the given page. Preserves the pre-refactor filtering behavior so the
// legacy top-level GetBlocks continues to match.
func (b *BlockClient) GetBlocks(ctx context.Context, pageID string) ([]Block, error) {
	req, err := b.c.newRequest(ctx, http.MethodGet, "/blocks/"+pageID+"/children", nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return nil, err
	}
	var blockList BlockList
	if err := decodeInto(resp, &blockList); err != nil {
		return nil, err
	}

	var blocks []Block
	for _, result := range blockList.Results {
		if result.Object == "block" && result.ToDo != nil && len(result.ToDo.RichText) > 0 {
			blocks = append(blocks, result)
		}
	}
	return blocks, nil
}

// GetToDoBlocks returns formatted to-do strings (index, check state, text,
// last-edited time) for the given page, in the supplied timezone.
func (b *BlockClient) GetToDoBlocks(ctx context.Context, pageID string, localTimezone *time.Location) ([]string, error) {
	blocks, err := b.GetBlocks(ctx, pageID)
	if err != nil {
		return nil, err
	}
	var todoBlocks []string
	for _, block := range blocks {
		if block.ToDo == nil {
			continue
		}
		checked := " "
		if block.ToDo.Checked {
			checked = "X"
		}
		lastEditedTime, err := time.Parse(time.RFC3339, block.LastEditedTime)
		if err != nil {
			return nil, err
		}
		truncatedTime := lastEditedTime.In(localTimezone).Truncate(time.Minute)
		element := fmt.Sprintf("%d [%s] %s (%s)", len(todoBlocks)+1, checked, block.ToDo.RichText[0].PlainText, truncatedTime.Format("2006-01-02 15:04"))
		todoBlocks = append(todoBlocks, element)
	}
	return todoBlocks, nil
}

// AddNewToDoItem appends a to-do block with the given text to the given
// page.
func (b *BlockClient) AddNewToDoItem(ctx context.Context, pageID, text string) error {
	body := map[string]interface{}{
		"children": []map[string]interface{}{
			{
				"object": "block",
				"type":   "to_do",
				"to_do": map[string]interface{}{
					"rich_text": []map[string]interface{}{
						{
							"type": "text",
							"text": map[string]interface{}{
								"content": text,
							},
						},
					},
				},
			},
		},
	}
	req, err := b.c.newRequest(ctx, http.MethodPatch, "/blocks/"+pageID+"/children", body)
	if err != nil {
		return err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return err
	}
	return expectStatus(resp, http.StatusOK)
}

// GetBlockID resolves a 1-based block index on the given page into the
// block's Notion ID.
func (b *BlockClient) GetBlockID(ctx context.Context, pageID string, order int) (string, error) {
	if order < 1 {
		return "", fmt.Errorf("order must be greater than 0")
	}
	req, err := b.c.newRequest(ctx, http.MethodGet, "/blocks/"+pageID+"/children", nil)
	if err != nil {
		return "", err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return "", err
	}
	var blockList BlockList
	if err := decodeInto(resp, &blockList); err != nil {
		return "", err
	}
	if order > len(blockList.Results) {
		return "", fmt.Errorf("order number exceeds the number of blocks")
	}
	return blockList.Results[order-1].ID, nil
}

// MarkToDoBlockChecked flips the to-do at the given index to checked=true.
func (b *BlockClient) MarkToDoBlockChecked(ctx context.Context, pageID string, order int) error {
	return b.setToDoChecked(ctx, pageID, order, true)
}

// MarkToDoBlockUnChecked flips the to-do at the given index to checked=false.
func (b *BlockClient) MarkToDoBlockUnChecked(ctx context.Context, pageID string, order int) error {
	return b.setToDoChecked(ctx, pageID, order, false)
}

func (b *BlockClient) setToDoChecked(ctx context.Context, pageID string, order int, checked bool) error {
	blockID, err := b.GetBlockID(ctx, pageID, order)
	if err != nil {
		return err
	}
	body := map[string]interface{}{
		"to_do": map[string]interface{}{
			"checked": checked,
		},
	}
	req, err := b.c.newRequest(ctx, http.MethodPatch, "/blocks/"+blockID, body)
	if err != nil {
		return err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return err
	}
	return expectStatus(resp, http.StatusOK)
}

// DeleteToDoBlock removes the to-do at the given 1-based index.
func (b *BlockClient) DeleteToDoBlock(ctx context.Context, pageID string, order int) error {
	blockID, err := b.GetBlockID(ctx, pageID, order)
	if err != nil {
		return err
	}
	req, err := b.c.newRequest(ctx, http.MethodDelete, "/blocks/"+blockID, nil)
	if err != nil {
		return err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return err
	}
	return expectStatus(resp, http.StatusOK)
}

// GetAllBlocks retrieves every block under a page, following pagination.
// When filterType is non-empty, only blocks whose Type matches are
// returned.
func (b *BlockClient) GetAllBlocks(ctx context.Context, pageID, filterType string) ([]Block, error) {
	var result []Block
	var cursor string

	for {
		path := "/blocks/" + pageID + "/children"
		if cursor != "" {
			path += "?start_cursor=" + cursor
		}
		req, err := b.c.newRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := b.c.do(req)
		if err != nil {
			return nil, err
		}
		var blockList BlockList
		if err := decodeInto(resp, &blockList); err != nil {
			return nil, err
		}
		for _, block := range blockList.Results {
			if block.Object != "block" {
				continue
			}
			if filterType == "" || block.Type == filterType {
				result = append(result, block)
			}
		}
		if !blockList.HasMore || blockList.NextCursor == "" {
			break
		}
		cursor = blockList.NextCursor
	}
	return result, nil
}

// FormatAllBlocks returns human-readable lines plus a by-type count for
// every block under pageID, optionally filtered by filterType.
//
// This overload keeps legacy call sites intact — it delegates to
// FormatAllBlocksWithResolver with a NoPageResolver which errors on every
// lookup and therefore preserves the "[page:<id>]" marker byte-for-byte.
func (b *BlockClient) FormatAllBlocks(ctx context.Context, pageID string, localTimezone *time.Location, filterType string) ([]string, map[string]int, error) {
	return b.FormatAllBlocksWithResolver(ctx, pageID, localTimezone, filterType, NoPageResolver{})
}

// FormatAllBlocksWithResolver is FormatAllBlocks with a caller-supplied
// PageTitleResolver threaded through the snippet renderer so page
// mentions in "[page:<id>]" positions expand to "[<title>]" when the
// resolver succeeds. Any resolver error or empty title falls back to
// the legacy marker (per RenderRichTextWithResolver semantics), so a
// 404 on one mention can never panic or drop content.
//
// Human-output path only. JSON paths must continue to emit raw
// rich_text arrays (see cmd/blocks.go blocks list --json) so caller
// tooling sees the original mention shape rather than a bracketed
// lossy string.
func (b *BlockClient) FormatAllBlocksWithResolver(ctx context.Context, pageID string, localTimezone *time.Location, filterType string, resolver PageTitleResolver) ([]string, map[string]int, error) {
	blocks, err := b.GetAllBlocks(ctx, pageID, filterType)
	if err != nil {
		return nil, nil, err
	}

	var formatted []string
	typeCounts := make(map[string]int)
	for index, block := range blocks {
		lastEditedTime, err := time.Parse(time.RFC3339, block.LastEditedTime)
		if err != nil {
			return nil, nil, err
		}
		truncatedTime := lastEditedTime.In(localTimezone).Truncate(time.Minute)
		icon := GetBlockIcon(block)
		// Use the plain (no-ANSI) renderer for the table snippet: the 50-char
		// truncation below is a byte-slice, which is unsafe on a string that
		// might contain ANSI escape sequences (chopping mid-escape leaves
		// the terminal in a stuck formatting state). color.NoColor is
		// already flipped off under --json via rootCmd's PersistentPreRunE,
		// but relying on that for correctness here would make a future
		// caller of FormatAllBlocks from a JSON path silently leak escapes
		// into the stream. GetBlockContentPlainWithResolver closes the gap
		// while still expanding page mentions when a resolver is wired.
		content := GetBlockContentPlainWithResolver(ctx, block, resolver)
		if len(content) > 50 {
			content = content[:47] + "..."
		}
		element := fmt.Sprintf("%4d %s  [%-20s] %s (%s)",
			index+1,
			icon,
			block.Type,
			content,
			truncatedTime.Format("2006-01-02 15:04"))
		formatted = append(formatted, element)
		typeCounts[block.Type]++
	}
	return formatted, typeCounts, nil
}

// blockConfig holds the optional parameters used by AddBlock when building
// block payloads. Fields only matter for the types that reference them —
// e.g. Caption is ignored for paragraph/heading blocks.
type blockConfig struct {
	URL      string
	Caption  string
	FileID   string // Notion file_upload id; when set, media blocks use "file_upload" instead of "external".
	Language string
}

// BlockOption mutates a blockConfig. Exposed as functional options so callers
// can pass arbitrary combinations of URL, caption, file-upload id, and
// language without growing AddBlock's positional parameter list.
type BlockOption func(*blockConfig)

// WithURL sets the external URL used by image/file/video/embed/bookmark
// blocks. Overrides the positional text argument when non-empty.
func WithURL(u string) BlockOption {
	return func(c *blockConfig) { c.URL = u }
}

// WithCaption attaches a plain-text caption to media and bookmark blocks.
func WithCaption(s string) BlockOption {
	return func(c *blockConfig) { c.Caption = s }
}

// WithFileUploadID turns a media block into the "file_upload" variant,
// referencing a previously-uploaded file by id (see utils.FileClient.Upload).
// When set, any WithURL value is ignored.
func WithFileUploadID(id string) BlockOption {
	return func(c *blockConfig) { c.FileID = id }
}

// WithLanguage sets the language on a code block. Defaults to "plain text"
// when not supplied.
func WithLanguage(lang string) BlockOption {
	return func(c *blockConfig) { c.Language = lang }
}

// AddBlock appends a block of the given type to the given page. Pass the
// block's text for rich-text types; text is the URL for media/embed/bookmark
// blocks and the LaTeX expression for equation blocks. Optional behaviour
// (file_upload id, captions, language) is supplied via BlockOption values.
// Text is ignored for the divider block.
//
// This is the single-segment write path — callers that need annotations,
// mentions, inline equations, or multi-segment rich text must use
// AddRichTextBlock instead ([]RichText payload).
func (b *BlockClient) AddBlock(ctx context.Context, pageID, blockType, text string, opts ...BlockOption) error {
	if !IsValidBlockType(blockType) {
		return fmt.Errorf("unsupported block type: %s", blockType)
	}
	if !IsAddableBlockType(blockType) {
		return fmt.Errorf("block type %q is not addable via `blocks add`; use a JSON-payload path once --json-input lands", blockType)
	}

	cfg := blockConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	blockContent, err := buildAddBlockPayload(blockType, text, cfg)
	if err != nil {
		return err
	}

	body := map[string]interface{}{
		"children": []map[string]interface{}{blockContent},
	}
	req, err := b.c.newRequest(ctx, http.MethodPatch, "/blocks/"+pageID+"/children", body)
	if err != nil {
		return err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return err
	}
	return expectStatus(resp, http.StatusOK)
}

// buildAddBlockPayload returns the per-type JSON envelope for the Notion
// `PATCH /blocks/{id}/children` endpoint. Split out of AddBlock so it can be
// unit-tested without an HTTP round-trip and so each new type maps to a
// single self-contained case block.
func buildAddBlockPayload(blockType, text string, cfg blockConfig) (map[string]interface{}, error) {
	switch blockType {
	case "divider":
		return map[string]interface{}{
			"object":  "block",
			"type":    "divider",
			"divider": map[string]interface{}{},
		}, nil

	case "image", "file", "video":
		inner, err := buildMediaBlock(blockType, text, cfg)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"object":  "block",
			"type":    blockType,
			blockType: inner,
		}, nil

	case "embed":
		url := mediaURL(text, cfg)
		if url == "" {
			return nil, fmt.Errorf("embed block requires a URL")
		}
		inner := map[string]interface{}{"url": url}
		if captionRT := captionRichText(cfg.Caption); captionRT != nil {
			inner["caption"] = captionRT
		}
		return map[string]interface{}{
			"object": "block",
			"type":   "embed",
			"embed":  inner,
		}, nil

	case "bookmark":
		url := mediaURL(text, cfg)
		if url == "" {
			return nil, fmt.Errorf("bookmark block requires a URL")
		}
		inner := map[string]interface{}{"url": url}
		if captionRT := captionRichText(cfg.Caption); captionRT != nil {
			inner["caption"] = captionRT
		}
		return map[string]interface{}{
			"object":   "block",
			"type":     "bookmark",
			"bookmark": inner,
		}, nil

	case "equation":
		if text == "" {
			return nil, fmt.Errorf("equation block requires an expression")
		}
		return map[string]interface{}{
			"object":   "block",
			"type":     "equation",
			"equation": map[string]interface{}{"expression": text},
		}, nil

	default:
		// Rich-text family: paragraph, heading_1..3, bulleted/numbered,
		// to_do, toggle, quote, callout, code.
		richText := []map[string]interface{}{
			{
				"type": "text",
				"text": map[string]interface{}{"content": text},
			},
		}
		innerContent := map[string]interface{}{"rich_text": richText}
		if blockType == "code" {
			lang := cfg.Language
			if lang == "" {
				lang = "plain text"
			}
			innerContent["language"] = lang
		}
		return map[string]interface{}{
			"object":  "block",
			"type":    blockType,
			blockType: innerContent,
		}, nil
	}
}

// buildMediaBlock returns the inner envelope for an image/file/video block.
// When the caller supplied a file-upload id, the block uses the "file_upload"
// variant; otherwise it falls back to "external" with the URL taken from
// WithURL (preferred) or the positional text argument.
func buildMediaBlock(blockType, text string, cfg blockConfig) (map[string]interface{}, error) {
	inner := map[string]interface{}{}
	if cfg.FileID != "" {
		inner["type"] = "file_upload"
		inner["file_upload"] = map[string]interface{}{"id": cfg.FileID}
	} else {
		url := mediaURL(text, cfg)
		if url == "" {
			return nil, fmt.Errorf("%s block requires a URL (positional arg or --url) or a --file-upload-id", blockType)
		}
		inner["type"] = "external"
		inner["external"] = map[string]interface{}{"url": url}
	}
	if captionRT := captionRichText(cfg.Caption); captionRT != nil {
		inner["caption"] = captionRT
	}
	return inner, nil
}

// mediaURL picks the URL from WithURL when set, otherwise from the positional
// text argument. Keeps `blocks add https://pic.png -t image` working while
// still honouring `--url` on the cmd layer.
func mediaURL(text string, cfg blockConfig) string {
	if cfg.URL != "" {
		return cfg.URL
	}
	return text
}

// captionRichText wraps a plain-text caption in the rich-text array shape the
// Notion API expects on media/embed/bookmark blocks. An empty string yields
// nil so the caption key stays absent from the payload.
func captionRichText(s string) []map[string]interface{} {
	if s == "" {
		return nil
	}
	return []map[string]interface{}{
		{
			"type": "text",
			"text": map[string]interface{}{"content": s},
		},
	}
}

// AddRichTextBlock appends a block of the given type to the given page,
// using a caller-supplied rich-text array verbatim. Peer to AddBlock —
// same (ctx, pageID, blockType, payload) parameter ordering; the payload
// is []RichText instead of a plain string so annotations, mentions,
// inline equations, and multi-segment runs round-trip. Divider blocks
// carry no rich text; use AddBlock for those.
func (b *BlockClient) AddRichTextBlock(ctx context.Context, pageID, blockType string, rt []RichText) error {
	if !IsValidBlockType(blockType) {
		return fmt.Errorf("unsupported block type: %s", blockType)
	}
	if !BlockTypeAcceptsRichText(blockType) {
		return fmt.Errorf("block type %q does not accept rich text; rich-text-json is only valid for paragraph, heading_1/2/3, bulleted_list_item, numbered_list_item, to_do, toggle, quote, callout, and code", blockType)
	}
	if len(rt) == 0 {
		return fmt.Errorf("rich text must have at least one segment")
	}

	innerContent := map[string]interface{}{"rich_text": richTextToAPI(rt)}
	if blockType == "code" {
		innerContent["language"] = "plain text"
	}
	body := map[string]interface{}{
		"children": []map[string]interface{}{
			{
				"object":  "block",
				"type":    blockType,
				blockType: innerContent,
			},
		},
	}
	req, err := b.c.newRequest(ctx, http.MethodPatch, "/blocks/"+pageID+"/children", body)
	if err != nil {
		return err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return err
	}
	return expectStatus(resp, http.StatusOK)
}

// DeleteBlock removes the block at the given 1-based index across the full
// paginated block list.
func (b *BlockClient) DeleteBlock(ctx context.Context, pageID string, order int) error {
	blocks, err := b.GetAllBlocks(ctx, pageID, "")
	if err != nil {
		return err
	}
	if order < 1 || order > len(blocks) {
		return fmt.Errorf("block number %d out of range (1-%d)", order, len(blocks))
	}
	blockID := blocks[order-1].ID
	req, err := b.c.newRequest(ctx, http.MethodDelete, "/blocks/"+blockID, nil)
	if err != nil {
		return err
	}
	resp, err := b.c.do(req)
	if err != nil {
		return err
	}
	return expectStatus(resp, http.StatusOK)
}
