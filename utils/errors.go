// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// APIError is a Notion error response, kept structured rather than flattened
// into a sentence.
//
// The previous behaviour spliced the raw body into "unexpected status %d: %s",
// which cost three things: RequestID — the first thing Notion support asks
// for — was buried in prose; callers could not branch on Code without
// substring-matching (isQueryFallbackTrigger did exactly that); and JSON-mode
// consumers got the whole flattened sentence in one "error" string instead of
// a shape they could read. See issue #101.
//
// Fields mirror https://developers.notion.com/reference/status-codes. The
// envelope was confirmed live to carry object/status/code/message/request_id.
type APIError struct {
	Status     int             `json:"status"`
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	RequestID  string          `json:"request_id,omitempty"`
	Extra      json.RawMessage `json:"additional_data,omitempty"`
	RawBody    string          `json:"-"`
	Endpoint   string          `json:"-"`
	Suggestion string          `json:"-"`
}

// Error renders the message a human sees. It leads with Notion's own text,
// appends the remediation when we have one, and keeps request_id last so it
// is present without dominating the line.
func (e *APIError) Error() string {
	var b strings.Builder
	msg := e.Message
	if msg == "" {
		msg = e.RawBody
	}
	if e.Code != "" {
		fmt.Fprintf(&b, "%s (HTTP %d %s)", msg, e.Status, e.Code)
	} else {
		fmt.Fprintf(&b, "unexpected status %d: %s", e.Status, msg)
	}
	if e.Suggestion != "" {
		fmt.Fprintf(&b, " — %s", e.Suggestion)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " [request_id %s]", e.RequestID)
	}
	return b.String()
}

// IsNotFound and IsForbidden let callers branch on the condition instead of
// grepping a formatted string.
func (e *APIError) IsNotFound() bool  { return e.Status == http.StatusNotFound }
func (e *APIError) IsForbidden() bool { return e.Status == http.StatusForbidden }

// IsRateLimited reports the two statuses Notion documents as retryable with
// a Retry-After header. Nothing retries yet (issue #43); classifying them
// here is what a retry layer will branch on.
func (e *APIError) IsRateLimited() bool {
	return e.Status == http.StatusTooManyRequests || e.Status == 529
}

// parseAPIError turns a non-2xx response body into an *APIError, attaching
// remediation for the failures whose raw text sends users in the wrong
// direction (issue #107).
func parseAPIError(status int, endpoint string, body []byte) *APIError {
	e := &APIError{Status: status, Endpoint: endpoint, RawBody: strings.TrimSpace(string(body))}
	// A body that will not parse is not an error here — some proxies and
	// gateways answer with plain text, and RawBody still carries it.
	_ = json.Unmarshal(body, e)
	e.Suggestion = suggestionFor(e)
	return e
}

// suggestionFor maps a failure onto the action that actually resolves it.
//
// The 404 case matters most: Notion documents object_not_found as "the
// resource does not exist OR the integration has not been given access to
// it". Reporting only "not found" sends people hunting for a typo when the
// page is right there in their browser and simply is not shared — the single
// most common Notion integration mistake.
func suggestionFor(e *APIError) string {
	switch {
	case e.Status == http.StatusNotFound:
		// Notion's own 404 text often already says "Make sure the relevant
		// pages and databases are shared with your integration". Repeating
		// it makes the line long and reads like a stutter, so only add the
		// hint when the message has not covered it.
		if strings.Contains(strings.ToLower(e.Message), "shared with your integration") {
			return ""
		}
		return "the id may be wrong, or the page/database may not be shared with this integration " +
			"(open it in Notion → ••• → Connections → add your integration)"
	case e.Status == http.StatusForbidden:
		return "this integration lacks the capability for that operation; check its capabilities in " +
			"Notion → Settings → Connections"
	case e.Status == http.StatusUnauthorized:
		return "the API key was rejected; check NOTION_API_KEY"
	case e.IsRateLimited():
		return "rate limited; retry after a short pause (automatic backoff is issue #43)"
	case e.Status >= 500:
		return "Notion-side error; this is usually transient and worth retrying"
	}
	return ""
}
