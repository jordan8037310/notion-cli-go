// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("sk_test")
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.apiKey != "sk_test" {
		t.Errorf("apiKey=%q want sk_test", c.apiKey)
	}
	if c.BaseURL() != DefaultBaseURL {
		t.Errorf("baseURL=%q want %q", c.BaseURL(), DefaultBaseURL)
	}
	if c.APIVersion() != NotionAPIVersion {
		t.Errorf("apiVersion=%q want %q", c.APIVersion(), NotionAPIVersion)
	}
	if c.HTTPClient() == nil {
		t.Error("HTTPClient is nil; want a non-nil default *http.Client")
	}
}

func TestWithBaseURL(t *testing.T) {
	c := NewClient("sk_test", WithBaseURL("https://example.test/v1"))
	if c.BaseURL() != "https://example.test/v1" {
		t.Errorf("baseURL=%q want https://example.test/v1", c.BaseURL())
	}
}

func TestWithAPIVersion(t *testing.T) {
	c := NewClient("sk_test", WithAPIVersion("2099-01-01"))
	if c.APIVersion() != "2099-01-01" {
		t.Errorf("apiVersion=%q want 2099-01-01", c.APIVersion())
	}
}

func TestWithHTTPClient(t *testing.T) {
	custom := &http.Client{}
	c := NewClient("sk_test", WithHTTPClient(custom))
	if c.HTTPClient() != custom {
		t.Error("WithHTTPClient did not set the injected client")
	}
	// nil is a no-op (keeps the default client rather than crashing later).
	c2 := NewClient("sk_test", WithHTTPClient(nil))
	if c2.HTTPClient() == nil {
		t.Error("WithHTTPClient(nil) should leave the default client in place")
	}
}

func TestBaseURL(t *testing.T) {
	c := NewClient("sk_test", WithBaseURL("https://mock/v1"))
	if got := c.BaseURL(); got != "https://mock/v1" {
		t.Errorf("BaseURL()=%q", got)
	}
}

func TestAPIVersion(t *testing.T) {
	c := NewClient("sk_test", WithAPIVersion("2099-12-31"))
	if got := c.APIVersion(); got != "2099-12-31" {
		t.Errorf("APIVersion()=%q", got)
	}
}

func TestHTTPClient(t *testing.T) {
	hc := &http.Client{}
	c := NewClient("sk_test", WithHTTPClient(hc))
	if c.HTTPClient() != hc {
		t.Error("HTTPClient() did not return the injected *http.Client")
	}
}

// TestClientSendsNotionHeaders verifies every outbound request carries the
// Authorization, Notion-Version, and Accept headers. Uses BlockClient as a
// convenient exerciser of the full newRequest/do path.
func TestClientSendsNotionHeaders(t *testing.T) {
	var gotAuth, gotVersion, gotAccept, gotContentType string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Notion-Version")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c := NewClient("sk_test_abc",
		WithBaseURL(srv.URL),
		WithAPIVersion("2099-01-01"),
	)

	// PATCH carries a body, so Content-Type must be set. Use AddNewToDoItem
	// via BlockClient because it's the simplest body-bearing path.
	bc := NewBlockClient(c)
	if err := bc.AddNewToDoItem(context.Background(), "pageID", "hello"); err != nil {
		t.Fatalf("AddNewToDoItem: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method=%s want PATCH", gotMethod)
	}
	if gotAuth != "Bearer sk_test_abc" {
		t.Errorf("Authorization=%q want 'Bearer sk_test_abc'", gotAuth)
	}
	if gotVersion != "2099-01-01" {
		t.Errorf("Notion-Version=%q want 2099-01-01 (WithAPIVersion override)", gotVersion)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept=%q want application/json", gotAccept)
	}
	if gotContentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type=%q want application/json; charset=utf-8", gotContentType)
	}
}

func TestClientDefaultAPIVersion(t *testing.T) {
	var gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("Notion-Version")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", WithBaseURL(srv.URL))
	bc := NewBlockClient(c)
	if _, err := bc.GetBlocks(context.Background(), "pageID"); err != nil {
		t.Fatalf("GetBlocks: %v", err)
	}
	if gotVersion != NotionAPIVersion {
		t.Errorf("Notion-Version=%q want %q (default)", gotVersion, NotionAPIVersion)
	}
}

// TestDecodeIntoNon2xx covers the non-2xx branch of decodeInto: the server
// returns HTTP 400 with an error payload, and the caller should get a
// non-nil error whose message surfaces the status code and the body.
func TestDecodeIntoNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", WithBaseURL(srv.URL))
	bc := NewBlockClient(c)

	_, err := bc.GetBlocks(context.Background(), "pageID")
	if err == nil {
		t.Fatal("expected error from 400 response, got nil")
	}
	if msg := err.Error(); !strings.Contains(msg, "400") || !strings.Contains(msg, "bad request") {
		t.Errorf("error=%q should include status 400 and body", msg)
	}
}

func TestClientRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", WithBaseURL(srv.URL))
	bc := NewBlockClient(c)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before dispatch

	_, err := bc.GetBlocks(ctx, "pageID")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// --- Resource-client stub constructors ---

func TestNewBlockClient(t *testing.T) {
	c := NewClient("sk_test")
	bc := NewBlockClient(c)
	if bc == nil {
		t.Fatal("NewBlockClient returned nil")
	}
	if bc.c != c {
		t.Error("NewBlockClient did not store the injected *Client")
	}
}

func TestNewPageClient(t *testing.T) {
	c := NewClient("sk_test")
	pc := NewPageClient(c)
	if pc == nil {
		t.Fatal("NewPageClient returned nil")
	}
	if pc.c != c {
		t.Error("NewPageClient did not store the injected *Client")
	}
}

func TestNewDatabaseClient(t *testing.T) {
	c := NewClient("sk_test")
	dc := NewDatabaseClient(c)
	if dc == nil {
		t.Fatal("NewDatabaseClient returned nil")
	}
	if dc.c != c {
		t.Error("NewDatabaseClient did not store the injected *Client")
	}
}

func TestNewSearchClient(t *testing.T) {
	c := NewClient("sk_test")
	sc := NewSearchClient(c)
	if sc == nil {
		t.Fatal("NewSearchClient returned nil")
	}
	if sc.c != c {
		t.Error("NewSearchClient did not store the injected *Client")
	}
}

func TestNewCommentClient(t *testing.T) {
	c := NewClient("sk_test")
	cc := NewCommentClient(c)
	if cc == nil {
		t.Fatal("NewCommentClient returned nil")
	}
	if cc.c != c {
		t.Error("NewCommentClient did not store the injected *Client")
	}
}

func TestNewUserClient(t *testing.T) {
	c := NewClient("sk_test")
	uc := NewUserClient(c)
	if uc == nil {
		t.Fatal("NewUserClient returned nil")
	}
	if uc.c != c {
		t.Error("NewUserClient did not store the injected *Client")
	}
}
