// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestAPIError_ParsesTheNotionEnvelope guards issue #101. The client used to
// splice the raw body into "unexpected status %d: %s", losing request_id —
// the first thing Notion support asks for — and forcing callers to
// substring-match prose to branch on a condition.
//
// The envelope below is the shape a live 404 actually returned.
func TestAPIError_ParsesTheNotionEnvelope(t *testing.T) {
	body := []byte(`{"object":"error","status":404,"code":"object_not_found",
		"message":"Could not find database with ID: abc.",
		"request_id":"30ecefe3-c2cf-4772-ac9e-d1f8b1daa6af"}`)

	e := parseAPIError(http.StatusNotFound, "/databases/abc", body)

	if e.Code != "object_not_found" {
		t.Errorf("Code = %q", e.Code)
	}
	if e.RequestID != "30ecefe3-c2cf-4772-ac9e-d1f8b1daa6af" {
		t.Errorf("RequestID = %q — support cannot trace a failure without it", e.RequestID)
	}
	if !e.IsNotFound() || e.IsForbidden() || e.IsRateLimited() {
		t.Errorf("classification wrong for a 404: %+v", e)
	}
	msg := e.Error()
	for _, want := range []string{"Could not find database", "object_not_found", "request_id"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rendered message missing %q: %s", want, msg)
		}
	}
}

// TestAPIError_SuggestsTheFix guards issue #107. Notion documents 404 as
// "does not exist OR the integration has not been given access" — reporting
// only "not found" sends people hunting for a typo when the page is right
// there and simply is not shared.
func TestAPIError_SuggestsTheFix(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		want   string
	}{
		{"404 names the sharing case", http.StatusNotFound, "shared with this integration"},
		{"403 names capabilities", http.StatusForbidden, "capabilit"},
		{"401 names the key", http.StatusUnauthorized, "NOTION_API_KEY"},
		{"429 names rate limiting", http.StatusTooManyRequests, "rate limited"},
		{"5xx says transient", http.StatusBadGateway, "transient"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := parseAPIError(tt.status, "/x", []byte(`{"code":"c","message":"m"}`))
			if !strings.Contains(strings.ToLower(e.Error()), strings.ToLower(tt.want)) {
				t.Errorf("HTTP %d message lacks %q: %s", tt.status, tt.want, e.Error())
			}
		})
	}

	// A 400 has no generic remediation — better silent than misleading.
	// The old data-source hint was appended to every non-2xx, telling users
	// with an auth failure to go check their id.
	e := parseAPIError(http.StatusBadRequest, "/x", []byte(`{"code":"validation_error","message":"bad"}`))
	if e.Suggestion != "" {
		t.Errorf("400 should carry no generic suggestion, got %q", e.Suggestion)
	}
}

// TestAPIError_SurvivesNonJSONBodies covers gateways and proxies that answer
// with plain text. The status still classifies; the body is preserved.
func TestAPIError_SurvivesNonJSONBodies(t *testing.T) {
	e := parseAPIError(http.StatusBadGateway, "/x", []byte("upstream connect error"))
	if !strings.Contains(e.Error(), "upstream connect error") {
		t.Errorf("non-JSON body lost: %s", e.Error())
	}
	if e.Status != http.StatusBadGateway {
		t.Errorf("Status = %d", e.Status)
	}
}

// TestAPIError_IsReachableThroughErrorsAs is the property the whole change
// exists for: callers branch on the condition, not on the text, no matter
// how many wrapping layers sit in between.
func TestAPIError_IsReachableThroughErrorsAs(t *testing.T) {
	inner := parseAPIError(http.StatusNotFound, "/pages/x", []byte(`{"code":"object_not_found"}`))
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("get page: %w", inner))

	var got *APIError
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As could not reach the APIError through two wraps")
	}
	if !got.IsNotFound() {
		t.Error("recovered error lost its status")
	}
}

// TestClient_HasARequestTimeout guards issue #98. Go's zero-value
// http.Client has NO timeout and every call site passes
// context.Background(), so a stalled connection hung the process forever.
func TestClient_HasARequestTimeout(t *testing.T) {
	if got := NewClient("k").HTTPClient().Timeout; got != DefaultTimeout {
		t.Errorf("default client timeout = %v, want %v — a stalled connection would hang forever", got, DefaultTimeout)
	}
	if got := NewClient("k", WithTimeout(5*time.Second)).HTTPClient().Timeout; got != 5*time.Second {
		t.Errorf("WithTimeout not applied: %v", got)
	}
}

// TestClient_TimeoutActuallyFires proves the timeout is enforced, not merely
// configured: a server that accepts and never answers must not hang us.
func TestClient_TimeoutActuallyFires(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	c := NewClient("k", WithBaseURL(srv.URL), WithTimeout(150*time.Millisecond))
	req, err := c.newRequest(defaultCtx(), http.MethodGet, "/pages/x", nil)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}

	done := make(chan error, 1)
	go func() { _, e := c.do(req); done <- e }()

	select {
	case e := <-done:
		if e == nil {
			t.Fatal("expected a timeout error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not time out — the client is unbounded")
	}
}
