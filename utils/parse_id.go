// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// notionIDInString matches the 32-hex-with-optional-dashes Notion id shape
// when it appears anywhere inside a larger string (e.g. embedded in a URL
// path or slug). Anchored matching is handled by uuidPattern in aliases.go;
// this variant is unanchored on purpose so the URL-extraction path in
// ParseNotionID can pick the trailing id out of slugs like
// "Page-Title-abc123def4567890abc123def4567890".
var notionIDInString = regexp.MustCompile(`[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}`)

// ParseNotionID accepts the inputs the fetch dispatcher (and any future
// URL-or-id callers) need to handle and returns the canonical dashed UUID
// form Notion's API expects.
//
// Accepted forms:
//
//	abc123def4567890abc123def4567890        // bare 32-hex
//	abc123de-f456-7890-abc1-23def4567890    // dashed UUID
//	https://www.notion.so/Workspace/Page-Title-<id>
//	https://notion.so/<id>
//	notion.so/<id>
//	notion.so/<id>?v=<view-id>              // view fragment ignored
//
// Returns the dashed 8-4-4-4-12 form. Any input that does not contain a
// 32-hex (with or without dashes) sequence returns an error citing the
// original input so callers can produce a clean user-facing message.
//
// When a URL contains multiple 32-hex sequences (e.g. a page url with a
// `?v=<view-id>` query suffix), the FIRST id in the path is returned —
// that is the page/database id, not the secondary view id. Query strings
// and fragments are stripped before scanning.
func ParseNotionID(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", fmt.Errorf("parse notion id: empty input")
	}

	// Bare-id fast path: input is already the 32-hex or dashed UUID with
	// nothing surrounding it. Normalise to dashed form and return.
	if uuidPattern.MatchString(s) {
		return canonicalizeNotionID(s), nil
	}

	// URL/slug path: scan for the first id-shaped substring inside the
	// path. Strip ?query and #fragment first so query parameters
	// (notably the ?v=<view-id> Notion appends to database URLs) cannot
	// shadow the page id we actually want.
	path := s
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if match := notionIDInString.FindString(path); match != "" {
		return canonicalizeNotionID(match), nil
	}

	return "", fmt.Errorf("parse notion id: %q does not contain a Notion id (want 32-hex, dashed uuid, or notion.so URL)", input)
}

// canonicalizeNotionID strips dashes from a matched id and re-inserts them
// at the canonical 8-4-4-4-12 boundaries so every consumer of the result
// can rely on a single shape regardless of which input form was passed in.
// The caller MUST have already validated the input via uuidPattern or
// notionIDInString — this function does not re-validate.
func canonicalizeNotionID(raw string) string {
	hex := strings.ReplaceAll(raw, "-", "")
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}
