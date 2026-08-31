// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// fetchCmd is the unified URL/id dispatcher. It mirrors the Notion MCP
// server's notion-fetch tool: hand it any Notion URL or id and it figures
// out whether the resource is a page or a database, then prints the object
// (or its JSON form when --json is set) along with a follow-up hint.
//
// Probe order is page → database. Block-level fallback is intentionally
// out of scope per issue #38: block ids returned from `blocks list` are
// already addressable via that command, and probing /v1/blocks here would
// double the round-trip cost on every cold cache miss.
//
// --resolve-mentions is honoured by being accepted on the command line
// (the persistent flag wires it for free); it is mostly a no-op because
// fetch returns raw API objects rather than rendered rich text. Callers
// who want mention resolution should use `blocks list --resolve-mentions`.
var fetchCmd = &cobra.Command{
	Use:   "fetch <url-or-id>",
	Short: "Fetch a Notion page or database by URL or id, auto-detecting type",
	Long: `Fetch any Notion resource by URL or bare id without having to know
its type up front.

Accepted inputs:
  https://www.notion.so/Workspace/Page-Title-<id>
  https://notion.so/<id>
  notion.so/<id>
  <32-hex bare id>
  <dashed uuid>

The command probes /v1/pages first; on 404 it falls back to /v1/databases.
When neither hits, it returns a clear "no resource found" error.

Examples:
  notioncli fetch https://www.notion.so/Workspace/Project-abc123def456...
  notioncli fetch abc123def4567890abc123def4567890
  notioncli fetch <id> --json | jq .`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := utils.ParseNotionID(args[0])
		if err != nil {
			return jsonErrorOr(cmd, fmt.Errorf("fetch: %w", err))
		}

		apiKey, _ := utils.SetAPIConfig()
		if apiKey == "" {
			return jsonErrorOr(cmd, fmt.Errorf("fetch: %w", utils.ErrMissingAPIKey))
		}
		client := utils.NewClient(apiKey, utils.WithBaseURL(utils.GetBaseURL()))

		ctx := context.Background()

		// Probe pages first. Notion does not expose a way to know the
		// resource type from the id alone, so we GET /pages/{id} and on
		// 404 fall through to /databases/{id}. Other errors (auth,
		// transport) bubble up immediately because there is no point
		// double-probing them.
		page, pageRaw, pageErr := utils.NewPageClient(client).GetRaw(ctx, id)
		if pageErr == nil {
			return emitPage(cmd, page, pageRaw)
		}
		if !isNotFound(pageErr) {
			return jsonErrorOr(cmd, fmt.Errorf("fetch: probe page: %w", pageErr))
		}

		db, dbRaw, dbErr := utils.NewDatabaseClient(client).GetRaw(ctx, id)
		if dbErr == nil {
			return emitDatabase(cmd, db, dbRaw)
		}
		if !isNotFound(dbErr) {
			return jsonErrorOr(cmd, fmt.Errorf("fetch: probe database: %w", dbErr))
		}

		// Wrap the database probe's error rather than replacing it. Notion
		// documents object_not_found as "does not exist OR the integration
		// has not been given access" — a flat "no page or database found"
		// sends the user hunting for a typo when the page is open in their
		// browser and simply is not shared. Keeping the cause preserves
		// that remediation, plus request_id, and lets --json emit the
		// structured fields (issues #101, #107).
		return jsonErrorOr(cmd, fmt.Errorf("fetch: no page or database found at id %s: %w", id, dbErr))
	},
}

// emitPage formats a page result for the current output mode. JSON paths
// emit the raw object so the round-trip is loss-free; human paths print a
// minimal type/id/title summary plus a follow-up command hint.
//
// raw is the undecoded response body. Re-marshalling the typed Page here
// dropped every top-level key the struct does not model — icon, cover,
// created_by, last_edited_by — which contradicted this function's own
// loss-free contract (issue #80). raw is nil only when a caller
// constructed the Page by hand; fall back to the typed encode then.
func emitPage(cmd *cobra.Command, page *utils.Page, raw json.RawMessage) error {
	if globalJSON {
		if len(raw) > 0 {
			return jsonErrorOr(cmd, emitRaw(cmd.OutOrStdout(), raw))
		}
		return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), page))
	}
	w := cmd.OutOrStdout()
	title := pagePlainTitle(page)
	if title == "" {
		title = "(untitled)"
	}
	fmt.Fprintf(w, "%s %s\n", color.GreenString("page"), page.ID)
	fmt.Fprintf(w, "  title: %s\n", title)
	if page.URL != "" {
		fmt.Fprintf(w, "  url:   %s\n", page.URL)
	}
	fmt.Fprintf(w, "  hint:  notioncli blocks list --page %s\n", page.ID)
	return nil
}

// emitDatabase formats a database result for the current output mode.
// See emitPage for why the JSON path emits raw bytes.
func emitDatabase(cmd *cobra.Command, db *utils.Database, raw json.RawMessage) error {
	if globalJSON {
		if len(raw) > 0 {
			return jsonErrorOr(cmd, emitRaw(cmd.OutOrStdout(), raw))
		}
		return jsonErrorOr(cmd, emitJSON(cmd.OutOrStdout(), db))
	}
	w := cmd.OutOrStdout()
	title := databasePlainTitle(db)
	if title == "" {
		title = "(untitled)"
	}
	fmt.Fprintf(w, "%s %s\n", color.GreenString("database"), db.ID)
	fmt.Fprintf(w, "  title: %s\n", title)
	if db.URL != "" {
		fmt.Fprintf(w, "  url:   %s\n", db.URL)
	}
	fmt.Fprintf(w, "  hint:  notioncli databases query %s\n", db.ID)
	return nil
}

// pagePlainTitle digs the plain-text title out of a page's loose
// Properties map. Returns "" when no title property is found so callers
// can fall back to a placeholder. Walks the property map and matches by
// the Notion property type (== "title"), so renamed title columns
// (e.g. "Name", "Client Name", "Project") work — see issue #60.
//
// Concatenates every rich-text run rather than returning the first
// non-empty one, so titles split across multiple runs (mentions, mixed
// formatting, links) round-trip in full — closes #65.
func pagePlainTitle(page *utils.Page) string {
	if page == nil {
		return ""
	}
	var sb strings.Builder
	for _, v := range page.Properties {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := m["title"].([]interface{})
		if !ok {
			continue
		}
		for _, item := range items {
			run, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if pt, ok := run["plain_text"].(string); ok {
				sb.WriteString(pt)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	return ""
}

// databasePlainTitle returns the concatenated plain-text title of a
// Database. Notion serialises database titles as a rich_text array; we
// stitch the runs together and trim whitespace.
func databasePlainTitle(db *utils.Database) string {
	if db == nil {
		return ""
	}
	var sb strings.Builder
	for _, rt := range db.Title {
		if rt.PlainText != "" {
			sb.WriteString(rt.PlainText)
		} else if rt.Text.Content != "" {
			sb.WriteString(rt.Text.Content)
		}
	}
	return strings.TrimSpace(sb.String())
}

// isNotFound reports whether err looks like a 404 from the Notion API.
// utils.decodeInto wraps non-2xx responses as "unexpected status N: ..."
// rather than exposing typed status codes, so this check is a string
// scan. When the error structure grows a typed shape, swap this for an
// errors.As / errors.Is call.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	// Match the typed error rather than its rendered text. This used to
	// walk the chain looking for the substring "unexpected status 404",
	// which coupled the page/database dispatch to a message format —
	// exactly the fragility issue #101 describes. errors.As sees through
	// any number of fmt.Errorf("...: %w") layers on its own.
	var apiErr *utils.APIError
	return errors.As(err, &apiErr) && apiErr.IsNotFound()
}

func init() {
	rootCmd.AddCommand(fetchCmd)
}
