// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// NotionAPIVersion is the pinned Notion API version used for every request.
// The 2026-03-11 release introduced the endpoints this CLI now consumes:
//   - GET /v1/teams (workspace team listing)
//   - POST /v1/data_sources/{id}/views (view create/update for database views)
//   - POST /v1/file_uploads (two-step file upload flow)
// Older responses continue to decode through existing structs; the pre-existing
// block, page, database, comment, search, and users endpoints are unchanged.
const NotionAPIVersion = "2026-03-11"

// DefaultBaseURL is the default Notion API base URL. It is exposed so tests
// and integrators can reason about the default target without reaching into
// a global.
const DefaultBaseURL = "https://api.notion.com/v1"

// Client is the shared HTTP client used by every typed resource client
// (BlockClient, PageClient, etc.). It owns the Notion credentials and the
// underlying *http.Client so callers can inject their own transport for
// tests or custom timeouts.
type Client struct {
	baseURL    string
	apiKey     string
	apiVersion string
	httpClient *http.Client
}

// Option mutates a Client during construction. Pass Options into NewClient
// to override defaults.
type Option func(*Client)

// WithBaseURL overrides the Notion API base URL. Useful for tests that wire
// an httptest.Server in front of the client.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = baseURL
	}
}

// WithAPIVersion overrides the Notion-Version header sent with every request.
// Defaults to NotionAPIVersion. Only override in tests that verify the
// header wiring — production callers should rely on the pinned constant.
func WithAPIVersion(version string) Option {
	return func(c *Client) {
		c.apiVersion = version
	}
}

// WithHTTPClient injects a custom *http.Client (for example, one with a
// tuned timeout or a test RoundTripper). If not provided, NewClient uses a
// zero-value *http.Client matching the previous package-level behavior.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// NewClient constructs a Client with sensible defaults: DefaultBaseURL,
// NotionAPIVersion, and a bare *http.Client. Override any of those via
// Options.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		apiKey:     apiKey,
		apiVersion: NotionAPIVersion,
		httpClient: &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the client's configured base URL. Primarily useful for
// tests and diagnostic output.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// APIVersion returns the client's configured Notion-Version value.
func (c *Client) APIVersion() string {
	return c.apiVersion
}

// HTTPClient returns the underlying *http.Client.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// newRequest builds an *http.Request with the standard Notion headers
// applied. It is unexported — callers should use one of the typed resource
// clients (BlockClient, etc.) rather than hand-assembling requests.
func (c *Client) newRequest(ctx context.Context, method, path string, body interface{}) (*http.Request, error) {
	var reader io.Reader
	hasBody := false
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewBuffer(buf)
		hasBody = true
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Notion-Version", c.apiVersion)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if hasBody {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	return req, nil
}

// do executes the request using the Client's *http.Client. The caller is
// responsible for reading and closing the response body.
//
// This wrapper currently delegates directly to *http.Client.Do, but is
// retained as the single extension point for future cross-cutting concerns
// (retries, rate limiting, structured logging) so individual resource
// clients do not each need to grow their own middleware stack.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// decodeInto reads resp.Body and unmarshals it into target. It always closes
// resp.Body. Returns an error if the status code is non-2xx or the payload
// fails to decode.
func decodeInto(resp *http.Response, target interface{}) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// expectStatus closes resp.Body and returns an error if the status code is
// outside the 2xx range. Use this for PATCH/DELETE calls where the response
// payload is uninteresting.
func expectStatus(resp *http.Response, want int) error {
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// defaultCtx returns a context for legacy package-level callers that do not
// yet plumb context.Context. Kept tight so it is easy to find and remove
// when callers are migrated.
func defaultCtx() context.Context {
	return context.Background()
}
