// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	pagesExportDepth  int
	pagesExportFormat string
	pagesExportOut    string
)

// pagesExportCmd dumps a page and its sub-pages.
var pagesExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export a page and its sub-pages as JSON, markdown files, or a tree",
	Long: `Export a page tree.

Formats:
  json   one JSON document holding the page, its blocks (nested blocks
         included) and its sub-pages, recursively. Written to stdout.
  md     one markdown file per page, written under --out. Sub-pages become
         subdirectories, mirroring the hierarchy in Notion.
  tree   a human-readable outline on stdout. Nothing is written to disk,
         so it is the cheap way to see how large an export will be.

--depth limits recursion: 0 is the page on its own, 1 adds its immediate
sub-pages, and -1 (the default) means the whole tree.

Markdown comes from Notion's own renderer, so it includes content inside
toggles, columns and synced blocks that 'blocks list' cannot reach.

Pages the integration cannot read do not stop the export. They are listed
on stderr when it finishes and the exit code is non-zero, so a partial
backup is never mistaken for a complete one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format := pagesExportFormat
		if format == "" {
			format = "json"
		}
		switch format {
		case "json", "md", "tree":
		default:
			return jsonErrorOr(cmd, fmt.Errorf("export page: invalid --format %q (want json|md|tree)", format))
		}
		if format == "md" && pagesExportOut == "" {
			return jsonErrorOr(cmd, fmt.Errorf(
				"export page: --format md writes a file per page; pass --out DIR to say where"))
		}
		if format != "md" && pagesExportOut != "" {
			return jsonErrorOr(cmd, fmt.Errorf(
				"export page: --out only applies to --format md; %s goes to stdout", format))
		}

		pc, err := newPageClient()
		if err != nil {
			return jsonErrorOr(cmd, err)
		}
		root, err := pc.ExportTree(cmd.Context(), args[0], utils.ExportOptions{
			MaxDepth:     pagesExportDepth,
			WithBlocks:   format == "json",
			WithMarkdown: format == "md",
		})
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("export page: %w", err))
		}

		switch format {
		case "json":
			if err := emitJSON(cmd.OutOrStdout(), root); err != nil {
				return jsonErrorOr(cmd, err)
			}
		case "tree":
			writeTree(cmd.OutOrStdout(), root, "")
		case "md":
			written, err := writeMarkdownTree(root, pagesExportOut)
			if err != nil {
				return jsonErrorOr(cmd, fmt.Errorf("export page: %w", err))
			}
			if !globalJSON {
				color.Green("Wrote %d page(s) under %s", written, pagesExportOut)
			}
		}
		return jsonErrorOr(cmd, reportExportErrors(cmd, root))
	},
}

// reportExportErrors lists the pages that did not export and returns a
// non-zero-exit error when there were any.
//
// A backup command that reports success while quietly missing a subtree
// is the worst possible failure mode: the gap is discovered when the
// backup is needed. Naming each page on stderr keeps stdout usable.
func reportExportErrors(cmd *cobra.Command, root *utils.PageNode) error {
	failed := root.Errors()
	if len(failed) == 0 {
		return nil
	}
	for _, node := range failed {
		label := node.Title
		if label == "" {
			label = node.ID
		}
		errorLine(cmd, "incomplete: %s (%s): %s", label, node.ID, node.Err)
	}
	return fmt.Errorf("export page: %d of %d page(s) incomplete", len(failed), root.Count())
}

// writeTree prints the outline form.
func writeTree(w io.Writer, node *utils.PageNode, indent string) {
	label := node.Title
	if label == "" {
		label = "(untitled)"
	}
	suffix := ""
	if node.Err != "" {
		suffix = "  [" + node.Err + "]"
	}
	fmt.Fprintf(w, "%s%s  %s%s\n", indent, label, node.ID, suffix)
	for _, child := range node.Children {
		writeTree(w, child, indent+"  ")
	}
}

// writeMarkdownTree writes one file per page under dir, mirroring the
// page hierarchy as directories, and returns how many files it wrote.
//
// A page with sub-pages becomes a directory containing its own
// "index.md" plus a file or directory per child. That keeps a parent's
// content and its children in one place, which is what makes the output
// browsable and re-importable.
func writeMarkdownTree(node *utils.PageNode, dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("create %s: %w", dir, err)
	}
	// Reserve "index" before naming any child: this directory's own body
	// is written to index.md, and a sub-page titled "Index" would slugify
	// to the same name and overwrite the parent's content.
	used := map[string]bool{"index": true}
	written := 0
	for _, child := range node.Children {
		n, err := writeMarkdownNode(child, dir, used)
		if err != nil {
			return written, err
		}
		written += n
	}
	// The root's own body goes in the directory it was pointed at, so
	// `pages export X --format md -o ./backup` yields ./backup/index.md
	// rather than an extra level of nesting named after the page.
	if node.Page != nil {
		if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(node.Markdown), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", filepath.Join(dir, "index.md"), err)
		}
		written++
	}
	return written, nil
}

func writeMarkdownNode(node *utils.PageNode, dir string, used map[string]bool) (int, error) {
	name := uniqueName(slugify(node.Title, node.ID), used)
	if len(node.Children) > 0 {
		sub := filepath.Join(dir, name)
		return writeMarkdownTree(node, sub)
	}
	if node.Page == nil {
		// Nothing was fetched for this page; the error is reported
		// separately. Writing an empty file would put a plausible-looking
		// blank page in the backup.
		return 0, nil
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(node.Markdown), 0o644); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return 1, nil
}

// slugify turns a page title into a single safe path segment.
//
// Page titles are remote data being used to build filesystem paths, which
// makes this a path-traversal sink: a page titled "../../.ssh/authorized_keys"
// must not be able to place a file outside the export directory. Rather
// than blacklisting separators and dot segments — where one missed case
// is a written file in the wrong place — this keeps only an allowlist of
// letters and digits, collapsing every other run into a single dash, so no
// output can contain a separator or be a dot segment by construction.
//
// The id is the fallback and the tiebreaker: a title of only punctuation,
// or an empty one, still gets a filename that identifies the page.
func slugify(title, id string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case b.Len() > 0 && !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	// Long titles make unwieldy paths, and some filesystems cap a single
	// component at 255 bytes.
	if len(name) > 80 {
		name = strings.Trim(name[:80], "-")
	}
	if name == "" {
		return shortID(id)
	}
	return name
}

// uniqueName disambiguates two sibling pages with the same title, which
// Notion allows and a filesystem does not.
func uniqueName(name string, used map[string]bool) string {
	candidate := name
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	used[candidate] = true
	return candidate
}

// shortID trims a page id to its first segment, enough to tell sibling
// pages apart without putting a full uuid in every path.
func shortID(id string) string {
	clean := strings.ReplaceAll(id, "-", "")
	if len(clean) > 8 {
		clean = clean[:8]
	}
	if clean == "" {
		return "page"
	}
	return "page-" + clean
}

func init() {
	pagesCmd.AddCommand(pagesExportCmd)
	pagesExportCmd.Flags().IntVar(&pagesExportDepth, "depth", -1,
		"How deep to recurse: 0 = this page only, 1 = plus its sub-pages, -1 = unlimited")
	pagesExportCmd.Flags().StringVar(&pagesExportFormat, "format", "json", "Output format: json|md|tree")
	pagesExportCmd.Flags().StringVarP(&pagesExportOut, "out", "o", "", "Directory to write markdown files into (--format md only)")
}
