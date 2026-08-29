// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SearchClient is the typed resource client for the Notion search API.
//
// The Notion search endpoint (POST /v1/search) returns pages and databases
// the integration has access to, optionally filtered by object type. Results
// are paginated via the standard has_more / next_cursor envelope.
type SearchClient struct {
	c *Client
}

// NewSearchClient wraps a *Client with search-resource methods.
func NewSearchClient(c *Client) *SearchClient {
	return &SearchClient{c: c}
}

// SearchFilter restricts results to a single object type ("page" or
// "database"). The Notion API expects {"property":"object","value":"page"}.
type SearchFilter struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// SearchRequest is the body sent to POST /v1/search. Fields are pointer- or
// zero-value-elidable so we only send what callers specified.
type SearchRequest struct {
	Query       string        `json:"query,omitempty"`
	Filter      *SearchFilter `json:"filter,omitempty"`
	PageSize    int           `json:"page_size,omitempty"`
	StartCursor string        `json:"start_cursor,omitempty"`
}

// SearchResult is a single entry in the search response. The Notion API
// returns a heterogeneous result shape (page or database with nested
// properties, icon, url, etc.) so we preserve the raw payload as
// json.RawMessage and surface only the fields the CLI needs for its table
// view. Callers that want the full object can re-decode Raw themselves.
type SearchResult struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	URL            string          `json:"url"`
	CreatedTime    string          `json:"created_time"`
	LastEditedTime string          `json:"last_edited_time"`
	Icon           *Icon           `json:"icon,omitempty"`
	Parent         json.RawMessage `json:"parent,omitempty"`
	Properties     json.RawMessage `json:"properties,omitempty"`
	Title          json.RawMessage `json:"title,omitempty"`
	// Raw is the untouched JSON payload for this result. It exists so the
	// --json output path can pass the full Notion object through without
	// re-marshalling (the search endpoint is heterogeneous — pages and
	// databases in one list — and the typed fields above intentionally
	// model only what the table view needs). Apply this Raw pass-through
	// pattern selectively: it's the right call for heterogeneous endpoints
	// (search, page reads) but overkill for stable-typed responses, where
	// re-marshalling from typed fields is lossless and simpler.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON preserves the raw payload alongside the typed fields so the
// --json flag can pass through the full Notion response unchanged. See the
// Raw field's godoc for guidance on when to adopt this pattern.
func (r *SearchResult) UnmarshalJSON(data []byte) error {
	type alias SearchResult
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = SearchResult(a)
	r.Raw = append(r.Raw[:0], data...)
	return nil
}

// Icon represents a Notion icon, which can be emoji, external URL, or a
// hosted file. Only the type-specific field will be populated.
type Icon struct {
	Type     string        `json:"type"`
	Emoji    string        `json:"emoji,omitempty"`
	External *IconExternal `json:"external,omitempty"`
	File     *IconFile     `json:"file,omitempty"`
}

// Display returns a compact representation of the icon suitable for table
// output. Empty string if no icon is set.
func (i *Icon) Display() string {
	if i == nil {
		return ""
	}
	switch i.Type {
	case "emoji":
		return i.Emoji
	case "external":
		if i.External != nil {
			return i.External.URL
		}
	case "file":
		if i.File != nil {
			return i.File.URL
		}
	}
	return ""
}

// IconExternal is the external-URL variant of an Icon.
type IconExternal struct {
	URL string `json:"url"`
}

// IconFile is the hosted-file variant of an Icon.
type IconFile struct {
	URL        string `json:"url"`
	ExpiryTime string `json:"expiry_time,omitempty"`
}

// SearchResponse is one page of search results returned by the Notion API.
type SearchResponse struct {
	Object     string         `json:"object"`
	Results    []SearchResult `json:"results"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

// checkAuth mirrors the guard pattern used by every other typed client
// (PageClient/UserClient/TeamClient/etc.). It maps a nil receiver, nil
// underlying *Client, or empty API key to ErrMissingAPIKey instead of a
// runtime panic or an opaque server-side 401. Library callers that wire
// SearchClient through a test seam (NewSearchClient(nil)) get a typed
// error rather than a deref panic. See issue #54.
func (s *SearchClient) checkAuth() error {
	if s == nil || s.c == nil || s.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// Search performs a single POST /v1/search call and returns the immediate
// page of results. Callers that need to walk every page should use
// SearchAll, which handles cursor pagination.
func (s *SearchClient) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if err := s.checkAuth(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	httpReq, err := s.c.newRequest(ctx, http.MethodPost, "/search", req)
	if err != nil {
		return nil, err
	}
	resp, err := s.c.do(httpReq)
	if err != nil {
		return nil, err
	}
	var out SearchResponse
	if err := decodeInto(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchAll walks pagination until the server reports HasMore=false or the
// supplied limit is reached. A non-positive limit means "return everything".
// When limit is positive, the returned slice is truncated to exactly limit
// entries (or fewer if the server ran out first). Mid-pagination errors are
// wrapped with the 1-based page number so callers can see how far the walk
// got before failing.
func (s *SearchClient) SearchAll(ctx context.Context, req SearchRequest, limit int) ([]SearchResult, error) {
	var all []SearchResult
	cursor := req.StartCursor
	for pageNum := 1; ; pageNum++ {
		page := req
		page.StartCursor = cursor
		resp, err := s.Search(ctx, page)
		if err != nil {
			return nil, fmt.Errorf("search page %d: %w", pageNum, err)
		}
		all = append(all, resp.Results...)
		if limit > 0 && len(all) >= limit {
			return all[:limit], nil
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return all, nil
		}
		cursor = resp.NextCursor
	}
}
