// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
)

// PageTitleResolver looks up a Notion page's title by ID. The
// RenderRichText path consults this when rendering page mentions so that
// a raw "[page:<id>]" marker can be expanded into "[<title>]". Return
// ("", error) to signal the caller should fall back to the default
// "[page:<id>]" rendering (missing auth, 404, transport failure, etc.).
type PageTitleResolver interface {
	ResolvePageTitle(ctx context.Context, pageID string) (string, error)
}

// NoPageResolver is the zero-config resolver: it always errors, which
// keeps the legacy "[page:<id>]" rendering. Used when the caller has
// not wired up a real resolver. Safe to use as a value receiver.
type NoPageResolver struct{}

// ResolvePageTitle always returns an error so RenderRichTextWithResolver
// falls back to the default "[page:<id>]" marker.
func (NoPageResolver) ResolvePageTitle(ctx context.Context, pageID string) (string, error) {
	return "", fmt.Errorf("no page title resolver configured")
}

// CachingPageResolver wraps a *PageClient and memoises title lookups
// within a single render pass. Call sites should create a fresh resolver
// per CLI invocation — the cache is intentionally process-local and
// unbounded so stale titles cannot leak across runs and so a page that
// is mentioned N times in a rendered view only triggers a single API
// call. Not safe for concurrent use; render paths are sequential.
type CachingPageResolver struct {
	client *PageClient
	cache  map[string]string
	errs   map[string]error
}

// NewCachingPageResolver returns a CachingPageResolver that delegates
// to the given *PageClient. The caller retains ownership of the client.
// Passing a nil client is permitted — every lookup will then error and
// RenderRichTextWithResolver will fall back to "[page:<id>]".
func NewCachingPageResolver(c *PageClient) *CachingPageResolver {
	return &CachingPageResolver{
		client: c,
		cache:  make(map[string]string),
		errs:   make(map[string]error),
	}
}

// ResolvePageTitle returns the cached title for pageID, fetching it via
// PageClient.Get on first call. Subsequent calls for the same id hit
// the cache — including the negative cache for ids that previously
// errored, so a 404 on one mention does not trigger a retry storm when
// the same page is referenced many times in one block.
//
// Returns ("", err) on:
//   - nil client (no-op resolver configured)
//   - empty pageID (caller programming error)
//   - any error from PageClient.Get (cached for subsequent calls)
//
// Returns ("", nil) when the page exists but has no title property —
// the caller (RenderRichTextWithResolver) interprets that as a fallback
// signal and emits the legacy "[page:<id>]" marker.
func (r *CachingPageResolver) ResolvePageTitle(ctx context.Context, pageID string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("resolve page title: nil resolver")
	}
	if pageID == "" {
		return "", fmt.Errorf("resolve page title: empty page id")
	}
	if title, ok := r.cache[pageID]; ok {
		return title, nil
	}
	if err, ok := r.errs[pageID]; ok {
		return "", err
	}
	if r.client == nil {
		err := fmt.Errorf("resolve page title: no page client configured")
		r.errs[pageID] = err
		return "", err
	}
	page, err := r.client.Get(ctx, pageID)
	if err != nil {
		wrapped := fmt.Errorf("resolve page title: %w", err)
		r.errs[pageID] = wrapped
		return "", wrapped
	}
	title := extractPageTitle(page)
	r.cache[pageID] = title
	return title, nil
}

// extractPageTitle returns the plain-text title of p by concatenating
// the plain_text segments of the page's "title"-typed property.
// Returns "" when the page has no title property or the property is
// empty — RenderRichTextWithResolver treats that as a fallback signal
// and emits "[page:<id>]".
//
// Notion pages can name their title property anything (Notion lets users
// rename "Name" on database-parented pages), so we scan properties for
// the one whose type is "title" rather than keying on a literal name.
func extractPageTitle(p *Page) string {
	if p == nil {
		return ""
	}
	for _, v := range p.Properties {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		// Only consider properties explicitly typed as "title". A page's
		// title property may be stored under any key (e.g. "Name" on
		// database rows), but always has type="title".
		if t, _ := m["type"].(string); t != "" && t != "title" {
			continue
		}
		items, ok := m["title"].([]interface{})
		if !ok {
			continue
		}
		var out string
		for _, item := range items {
			run, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if pt, ok := run["plain_text"].(string); ok {
				out += pt
			}
		}
		if out != "" {
			return out
		}
	}
	return ""
}
