// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fatih/color"
)

// RenderRichText returns a human-readable string built from a Notion
// rich-text array. Each segment's annotations (bold/italic/strike/
// underline/code + color) are wrapped in ANSI escapes via fatih/color,
// mentions are expanded into compact display markers, and inline
// equations are surfaced as "$expression$".
//
// Color policy:
//   - fatih/color respects the global color.NoColor flag. When --json is
//     active the rootCmd PreRun flips color.NoColor=true, which makes
//     every color.Sprint below a no-op and yields plain text. That is
//     intentional: JSON output paths should stay ANSI-clean.
//
// Mention policy (v1):
//   - user     → "@<name>" (falls back to "@<id>" when Name is empty)
//   - page     → "[page:<id>]" by default; when a PageTitleResolver is
//     supplied via RenderRichTextWithResolver and it returns a
//     non-empty title, renders as "[<title>]" instead
//   - date     → "<start>" or "<start..end>"
//   - database → "{db:<id>}"
//
// This overload keeps legacy call sites intact — it delegates to
// RenderRichTextWithResolver with a NoPageResolver which errors on every
// lookup and therefore preserves the "[page:<id>]" marker.
func RenderRichText(rt []RichText) string {
	return RenderRichTextWithResolver(context.Background(), rt, NoPageResolver{})
}

// RenderRichTextWithResolver is RenderRichText but routes page mentions
// through the supplied PageTitleResolver so "[page:<id>]" can be
// expanded to "[<title>]".
//
// Semantics:
//   - resolver == nil or NoPageResolver{} → legacy "[page:<id>]"
//   - resolver returns a non-empty title → "[<title>]"
//   - resolver returns ("", nil) (page exists but has no title) →
//     falls back to "[page:<id>]" rather than emitting "[]"
//   - resolver returns any error → falls back to "[page:<id>]"
//
// The resolver is invoked once per page mention segment; caching (so a
// block that mentions the same page many times triggers a single API
// call) is a concern of the resolver implementation — see
// CachingPageResolver.
func RenderRichTextWithResolver(ctx context.Context, rt []RichText, resolver PageTitleResolver) string {
	if len(rt) == 0 {
		return ""
	}
	var sb strings.Builder
	for i := range rt {
		sb.WriteString(renderSegmentWithResolver(ctx, &rt[i], resolver))
	}
	return sb.String()
}

// renderSegmentWithResolver produces the display string for a single
// rich-text run, picking the right payload shape (mention / equation /
// text), applying the run's annotations, and consulting the resolver
// for page mentions.
func renderSegmentWithResolver(ctx context.Context, r *RichText, resolver PageTitleResolver) string {
	raw := segmentPayloadWithResolver(ctx, r, resolver)
	return applyAnnotations(raw, r.Annotations)
}

// segmentPayloadWithResolver returns the bare, unannotated content of a
// rich-text run, consulting the resolver for page-mention title
// expansion. For mentions it expands into a display marker; for
// equations it wraps in "$…$"; for everything else it uses PlainText
// (or Text.Content as a fallback when PlainText is empty, which can
// happen on inputs the caller constructed by hand for write paths).
// Callers that do not need resolution can pass NoPageResolver{} and
// context.Background().
func segmentPayloadWithResolver(ctx context.Context, r *RichText, resolver PageTitleResolver) string {
	// Mention — type discriminator selects the sub-shape.
	if r.Type == "mention" && r.Mention != nil {
		return renderMention(ctx, r.Mention, r.PlainText, resolver)
	}
	// Inline equation.
	if r.Type == "equation" && r.Equation != nil {
		return "$" + r.Equation.Expression + "$"
	}
	// Plain text is the common case.
	if r.PlainText != "" {
		return r.PlainText
	}
	return r.Text.Content
}

// renderMention produces a compact marker for a mention segment. The
// fallback parameter is the run's PlainText — Notion populates it with a
// pre-rendered string (e.g. "@Jordan Ryan") that we use when the typed
// sub-object is missing or ambiguous. The resolver is consulted on
// page mentions; any error (or empty title) falls back to the legacy
// "[page:<id>]" marker.
func renderMention(ctx context.Context, m *Mention, fallback string, resolver PageTitleResolver) string {
	switch m.Type {
	case "user":
		if m.User != nil {
			if m.User.Name != "" {
				return "@" + m.User.Name
			}
			if m.User.ID != "" {
				return "@" + m.User.ID
			}
		}
	case "page":
		if m.Page != nil && m.Page.ID != "" {
			// Notion already renders the mention's title into plain_text
			// and sends it inline — verified live, and it holds even for
			// a page that has since been trashed. Use it: it is free,
			// it is Notion's own rendering, and it is what a human wants
			// to read.
			//
			// This used to be discarded in favour of "[page:<uuid>]",
			// which meant the default output showed a raw id while the
			// title sat unused in the same response — and
			// --resolve-mentions then spent one API call per unique page
			// re-fetching exactly that title. See issue #41.
			if fallback != "" {
				return "[" + fallback + "]"
			}
			// plain_text empty: fall back to the resolver, which is now
			// the flag's only remaining purpose.
			if resolver != nil {
				if title, err := resolver.ResolvePageTitle(ctx, m.Page.ID); err == nil && title != "" {
					return "[" + title + "]"
				}
			}
			return "[page:" + m.Page.ID + "]"
		}
	case "database":
		if m.Database != nil && m.Database.ID != "" {
			// Same as page mentions: Notion supplies the database title
			// in plain_text, and this rendered a raw uuid instead.
			if fallback != "" {
				return "{" + fallback + "}"
			}
			return "{db:" + m.Database.ID + "}"
		}
	case "date":
		if m.Date != nil {
			if m.Date.End != "" {
				return "<" + m.Date.Start + ".." + m.Date.End + ">"
			}
			if m.Date.Start != "" {
				return "<" + m.Date.Start + ">"
			}
		}
	}
	// Unknown / malformed mention — fall back to Notion's pre-rendered
	// PlainText so we never drop content silently.
	if fallback != "" {
		return fallback
	}
	return "<mention>"
}

// applyAnnotations wraps s in the ANSI escapes matching ann. Order is
// important: code wrapping goes innermost (so a bold code span reads
// "bold(`code`)" not "`bold(code)`"), and color goes outermost so the
// reset at the end clears everything in one go.
//
// When color.NoColor is true, color.New(...).Sprint is a no-op and this
// function is effectively identity + backtick wrapping for code runs.
func applyAnnotations(s string, ann Annotation) string {
	if s == "" {
		return s
	}
	out := s
	// Code wraps the payload in backticks regardless of color state so
	// the "this is inline code" signal survives --json.
	if ann.Code {
		out = "`" + out + "`"
	}
	var attrs []color.Attribute
	if ann.Bold {
		attrs = append(attrs, color.Bold)
	}
	if ann.Italic {
		attrs = append(attrs, color.Italic)
	}
	if ann.Strikethrough {
		attrs = append(attrs, color.CrossedOut)
	}
	if ann.Underline {
		attrs = append(attrs, color.Underline)
	}
	if ann.Code {
		// Dim inline code runs so they stand out from surrounding prose.
		attrs = append(attrs, color.Faint)
	}
	if c := colorAttribute(ann.Color); c != 0 {
		attrs = append(attrs, c)
	}
	if len(attrs) == 0 {
		return out
	}
	return color.New(attrs...).Sprint(out)
}

// colorAttribute maps a Notion annotation color name to a fatih/color
// foreground attribute. Background variants ("gray_background", etc.)
// are best-effort: we pick the closest foreground tone so the segment
// stays visually distinct without introducing ANSI background codes
// that many terminals render poorly. "default" and unknown values
// return 0, which the caller treats as "no color attribute".
func colorAttribute(name string) color.Attribute {
	switch name {
	case "gray", "gray_background":
		return color.FgHiBlack
	case "brown", "brown_background":
		return color.FgYellow
	case "orange", "orange_background":
		return color.FgHiYellow
	case "yellow", "yellow_background":
		return color.FgYellow
	case "green", "green_background":
		return color.FgGreen
	case "blue", "blue_background":
		return color.FgBlue
	case "purple", "purple_background":
		return color.FgMagenta
	case "pink", "pink_background":
		return color.FgHiMagenta
	case "red", "red_background":
		return color.FgRed
	}
	return 0
}

// PlainRichText returns the rich-text array as a single unannotated
// string: every segment's displayable payload concatenated, with
// mentions expanded to their markers and equations wrapped in "$…$" but
// no ANSI escapes applied. Used by JSON paths and by tests that need a
// deterministic string without fighting color.NoColor global state.
//
// This overload keeps legacy call sites intact — it delegates to
// PlainRichTextWithResolver with a NoPageResolver which errors on every
// lookup and therefore preserves the "[page:<id>]" marker.
func PlainRichText(rt []RichText) string {
	return PlainRichTextWithResolver(context.Background(), rt, NoPageResolver{})
}

// PlainRichTextWithResolver is PlainRichText but routes page mentions
// through the supplied PageTitleResolver so "[page:<id>]" can be
// expanded to "[<title>]". Semantics match RenderRichTextWithResolver
// (resolver error or empty title → legacy "[page:<id>]" marker); the
// only difference is that no ANSI annotations are applied.
//
// Used by FormatAllBlocks's snippet path — the 50-char truncation there
// byte-slices the string, which is safe only with no ANSI escapes.
func PlainRichTextWithResolver(ctx context.Context, rt []RichText, resolver PageTitleResolver) string {
	if len(rt) == 0 {
		return ""
	}
	var sb strings.Builder
	for i := range rt {
		sb.WriteString(segmentPayloadWithResolver(ctx, &rt[i], resolver))
	}
	return sb.String()
}

// ParseRichTextJSON parses a caller-supplied JSON document into a
// []RichText slice suitable for a write path (AddRichTextBlock). The
// input must be a JSON array; each element is unmarshalled through the
// standard RichText struct so mentions, equations, and annotations all
// round-trip.
//
// Validation:
//   - Top-level value must be an array (not an object, not a string).
//   - Empty array is rejected: the Notion API accepts it but it would
//     silently create an empty block, which is almost never what the
//     user meant when they passed --rich-text-json.
//   - Each element must have a non-empty displayable payload (text,
//     mention, or equation). A run with every payload empty would be
//     dropped by Notion and surface as a confusing partial write.
func ParseRichTextJSON(raw []byte) ([]RichText, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("rich-text JSON: empty input")
	}
	// Reject obvious non-array inputs up front for a better error
	// message than "cannot unmarshal object into []RichText".
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("rich-text JSON: top-level value must be an array")
	}
	var rt []RichText
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rt); err != nil {
		return nil, fmt.Errorf("rich-text JSON: decode: %w", err)
	}
	if len(rt) == 0 {
		return nil, fmt.Errorf("rich-text JSON: array is empty")
	}
	for i := range rt {
		if !richTextHasPayload(&rt[i]) {
			return nil, fmt.Errorf("rich-text JSON: segment %d has no text / mention / equation payload", i)
		}
	}
	return rt, nil
}

// richTextHasPayload reports whether a caller-built RichText carries
// anything Notion will render. A run with empty Text.Content + nil
// Mention + nil Equation is the empty run that ParseRichTextJSON
// rejects.
func richTextHasPayload(r *RichText) bool {
	if r.Mention != nil {
		return true
	}
	if r.Equation != nil && r.Equation.Expression != "" {
		return true
	}
	if r.Text.Content != "" || r.PlainText != "" {
		return true
	}
	return false
}

// richTextToAPI converts a caller-supplied []RichText into the
// `[]map[string]any` shape Notion's /blocks/{id}/children endpoint
// expects. Fields are emitted only when the segment actually sets them
// so a caller who supplies only {text:{content:"hi"}} doesn't end up
// pushing an empty annotations block that overrides Notion's defaults.
func richTextToAPI(rt []RichText) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(rt))
	for i := range rt {
		seg := &rt[i]
		m := map[string]interface{}{}
		// Default type is "text" when the caller omitted it but set
		// Text.Content; otherwise honour whatever they gave us.
		segType := seg.Type
		switch {
		case seg.Mention != nil:
			if segType == "" {
				segType = "mention"
			}
			m["mention"] = seg.Mention
		case seg.Equation != nil && seg.Equation.Expression != "":
			if segType == "" {
				segType = "equation"
			}
			m["equation"] = seg.Equation
		default:
			if segType == "" {
				segType = "text"
			}
			text := map[string]interface{}{"content": seg.Text.Content}
			if seg.Text.Link != nil {
				text["link"] = seg.Text.Link
			}
			m["text"] = text
		}
		m["type"] = segType
		if hasAnnotations(seg.Annotations) {
			m["annotations"] = seg.Annotations
		}
		if seg.Href != nil {
			m["href"] = seg.Href
		}
		out = append(out, m)
	}
	return out
}

// hasAnnotations reports whether any annotation flag is set. Used by
// richTextToAPI to avoid sending a no-op annotations block that clobbers
// downstream defaults.
func hasAnnotations(a Annotation) bool {
	return a.Bold || a.Italic || a.Strikethrough || a.Underline || a.Code || (a.Color != "" && a.Color != "default")
}
