// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
)

// PageNode is one page in an exported tree.
//
// Err is per-node rather than fatal. A workspace of any size contains
// pages the integration cannot read, and an export that aborts on the
// first of them is useless for its main purpose — taking a backup. The
// node is kept with its error recorded so the caller can report exactly
// which subtree is missing and why, and the walk continues.
type PageNode struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Depth is 0 for the root page.
	Depth int `json:"depth"`
	// Page is the page object as fetched. Nil when the fetch failed.
	Page *Page `json:"page,omitempty"`
	// Blocks is the page's own content, nested blocks included, flattened
	// with a Depth on each. Populated only when ExportOptions.WithBlocks
	// is set. It never contains another page's content: the walker treats
	// child_page as a new node, not as nested content.
	Blocks []NestedBlock `json:"blocks,omitempty"`
	// Markdown is the page rendered by Notion, populated only when
	// ExportOptions.WithMarkdown is set. See ExportTree for why this is
	// preferred over rendering Blocks locally.
	Markdown string `json:"markdown,omitempty"`
	// Truncated reports that Notion cut the rendered markdown short.
	// Surfaced rather than dropped — a silently shortened backup is worse
	// than a loud one.
	Truncated bool        `json:"truncated,omitempty"`
	Children  []*PageNode `json:"children,omitempty"`
	// Err is the reason this node is incomplete, if it is.
	Err string `json:"error,omitempty"`
}

// ExportOptions controls what ExportTree fetches per page.
type ExportOptions struct {
	// MaxDepth limits recursion. 0 exports only the root page, 1 adds its
	// immediate sub-pages, and a negative value means unlimited.
	//
	// Note the zero value is therefore "root only", not "unlimited".
	// Callers that want everything must say so; defaulting an unset
	// option to an unbounded crawl of someone's workspace is not a
	// default worth having.
	MaxDepth int
	// WithBlocks fetches each page's block tree.
	WithBlocks bool
	// WithMarkdown renders each page via Notion.
	WithMarkdown bool
}

// ExportTree walks a page and its sub-pages breadth-first and returns the
// tree.
//
// Sub-pages are discovered from child_page blocks, which is the only
// signal the API gives: a child_page block's id IS the page id of the
// child. That means discovery always costs a block listing per page, even
// in markdown mode where the block content itself is not wanted —
// Notion's rendered markdown names sub-pages but does not give their ids.
//
// Markdown comes from GET /v1/pages/{id}/markdown rather than from
// rendering Blocks locally. The rendered form includes content inside
// toggles, columns and synced blocks that a block listing cannot reach at
// all, so a locally rendered export would silently omit parts of the page
// it claims to have backed up. See utils/markdown.go.
//
// A visited set guards against revisiting a page. Notion's page hierarchy
// is a tree, so a child_page cycle should be impossible — but "should be
// impossible" is a poor reason to let a backup command loop forever
// against a live API, and the set costs one map.
func (p *PageClient) ExportTree(ctx context.Context, rootID string, opts ExportOptions) (*PageNode, error) {
	if err := p.checkAuth(); err != nil {
		return nil, fmt.Errorf("export page: %w", err)
	}
	if rootID == "" {
		return nil, fmt.Errorf("export page: id is required")
	}
	bc := NewBlockClient(p.c)
	visited := map[string]bool{}
	root := p.exportNode(ctx, bc, rootID, 0, opts, visited)
	// Only a failure on the ROOT is fatal: the caller asked for that page
	// specifically, so an empty tree is not a useful answer. Failures
	// deeper down are recorded on their node and reported alongside the
	// pages that did export.
	if root.Err != "" && root.Page == nil {
		return nil, fmt.Errorf("export page %s: %s", rootID, root.Err)
	}
	return root, nil
}

// exportNode fetches one page and recurses into its sub-pages.
func (p *PageClient) exportNode(ctx context.Context, bc *BlockClient, id string, depth int, opts ExportOptions, visited map[string]bool) *PageNode {
	node := &PageNode{ID: id, Depth: depth}
	if visited[id] {
		node.Err = "already exported elsewhere in this tree (cycle)"
		return node
	}
	visited[id] = true

	if err := ctx.Err(); err != nil {
		node.Err = err.Error()
		return node
	}

	page, err := p.Get(ctx, id)
	if err != nil {
		node.Err = err.Error()
		return node
	}
	node.Page = page
	node.Title = extractTitle(page)

	if opts.WithMarkdown {
		md, err := p.GetMarkdown(ctx, id)
		switch {
		case err != nil:
			// Not fatal for the node: the page object is already useful,
			// and the caller is told which page has no body.
			node.Err = "markdown: " + err.Error()
		default:
			node.Markdown = md.Markdown
			node.Truncated = md.Truncated
		}
	}

	// The block listing is needed for sub-page discovery whether or not
	// the caller wants the blocks themselves, so it always runs — but the
	// result is only kept when asked for.
	blocks, err := bc.GetAllBlocks(ctx, id, "")
	if err != nil {
		if node.Err == "" {
			node.Err = "blocks: " + err.Error()
		}
		return node
	}
	if opts.WithBlocks {
		tree, err := bc.GetBlockTree(ctx, id, 0)
		if err != nil {
			node.Err = "block tree: " + err.Error()
		} else {
			node.Blocks = tree
		}
	}

	if opts.MaxDepth >= 0 && depth >= opts.MaxDepth {
		return node
	}
	for _, block := range blocks {
		if block.Type != "child_page" {
			continue
		}
		node.Children = append(node.Children, p.exportNode(ctx, bc, block.ID, depth+1, opts, visited))
	}
	return node
}

// Walk calls fn for every node in the tree, parents before children.
func (n *PageNode) Walk(fn func(*PageNode)) {
	if n == nil {
		return
	}
	fn(n)
	for _, child := range n.Children {
		child.Walk(fn)
	}
}

// Errors collects every node that failed, so a caller can report the
// incomplete parts of an export in one place instead of hunting the tree.
func (n *PageNode) Errors() []*PageNode {
	var out []*PageNode
	n.Walk(func(node *PageNode) {
		if node.Err != "" {
			out = append(out, node)
		}
	})
	return out
}

// Count returns how many pages the tree holds.
func (n *PageNode) Count() int {
	total := 0
	n.Walk(func(*PageNode) { total++ })
	return total
}
