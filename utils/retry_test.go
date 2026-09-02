// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetry keeps the loop's timing out of the test suite's wall clock
// while still exercising the real code path.
func fastRetry() Option { return WithRetryBackoff(time.Millisecond, 5*time.Millisecond) }

// TestRetry_429IsRetriedAndHonoursRetryAfter covers the case the issue was
// filed for: Notion rate-limits at roughly 3 req/s, so a paginated walk
// through a large database hits 429 eventually. Guessing a shorter delay
// than Retry-After just earns another one, so the header wins.
func TestRetry_429IsRetriedAndHonoursRetryAfter(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"rate_limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"page","id":"p1"}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), fastRetry())
	req, _ := c.newRequest(context.Background(), http.MethodGet, "/pages/p1", nil)
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry", resp.StatusCode)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("made %d requests, want 2 (one 429, one success)", got)
	}
}

// TestRetry_RetryAfterBeatsBackoff pins the precedence directly.
func TestRetry_RetryAfterBeatsBackoff(t *testing.T) {
	p := retryPolicy{base: time.Second, maxWait: time.Minute}
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"7"}}}
	if got := retryDelay(p, 0, resp); got != 7*time.Second {
		t.Errorf("delay = %v, want the server's 7s", got)
	}
	// Still clamped: a server asking for an hour must not hang the CLI.
	resp.Header.Set("Retry-After", "3600")
	if got := retryDelay(p, 0, resp); got != time.Minute {
		t.Errorf("delay = %v, want it clamped to maxWait", got)
	}
	// Unparseable falls back to backoff rather than erroring — a header
	// we cannot read is not a reason to abandon the request.
	resp.Header.Set("Retry-After", "Wed, 21 Oct 2026 07:28:00 GMT")
	if got := retryDelay(p, 0, resp); got < time.Second {
		t.Errorf("delay = %v, want backoff when Retry-After is unparseable", got)
	}
}

// TestRetry_5xxOnlyForIdempotentMethods is the load-bearing safety
// property. A 502 on POST /v1/pages may mean the page WAS created and the
// gateway lost the response; replaying would create a second one. PATCH is
// unsafe for the same reason — PATCH /blocks/{id}/children APPENDS, so a
// replay duplicates blocks.
func TestRetry_5xxOnlyForIdempotentMethods(t *testing.T) {
	for _, tt := range []struct {
		method    string
		wantCalls int64
		why       string
	}{
		{http.MethodGet, 4, "GET is safe to replay"},
		{http.MethodDelete, 4, "DELETE is idempotent"},
		{http.MethodPost, 1, "POST may have been applied; replaying could double-create"},
		{http.MethodPatch, 1, "PATCH append is not idempotent; replaying duplicates blocks"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			var calls int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&calls, 1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer srv.Close()

			c := NewClient("k", WithBaseURL(srv.URL), fastRetry())
			req, _ := c.newRequest(context.Background(), tt.method, "/x", map[string]string{"a": "b"})
			resp, _ := c.do(req)
			drain(resp)

			if got := atomic.LoadInt64(&calls); got != tt.wantCalls {
				t.Errorf("%s made %d requests, want %d — %s", tt.method, got, tt.wantCalls, tt.why)
			}
		})
	}
}

// TestRetry_4xxIsNeverRetried keeps a real bug surfacing immediately
// instead of being hammered four times first.
func TestRetry_4xxIsNeverRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized,
		http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&calls, 1)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			c := NewClient("k", WithBaseURL(srv.URL), fastRetry())
			req, _ := c.newRequest(context.Background(), http.MethodGet, "/x", nil)
			resp, _ := c.do(req)
			drain(resp)

			if got := atomic.LoadInt64(&calls); got != 1 {
				t.Errorf("HTTP %d made %d requests, want 1 — a client error must surface at once", status, got)
			}
		})
	}
}

// TestRetry_BodyIsReplayed guards the subtle failure. A request body is a
// one-shot reader, so a naive retry sends an empty body and turns a
// transient 502 into a baffling 400.
func TestRetry_BodyIsReplayed(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), fastRetry())
	req, _ := c.newRequest(context.Background(), http.MethodPost, "/x", map[string]string{"name": "value"})
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	drain(resp)

	if len(bodies) != 2 {
		t.Fatalf("made %d requests, want 2", len(bodies))
	}
	for i, b := range bodies {
		if !strings.Contains(b, `"name":"value"`) {
			t.Errorf("attempt %d sent body %q — the body was not replayed", i+1, b)
		}
	}
}

// TestRetry_ContextCancellationAborts confirms ctrl-C is honoured during a
// backoff. A plain time.Sleep would ignore the context and leave the CLI
// unresponsive for the length of the wait.
func TestRetry_ContextCancellationAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient("k", WithBaseURL(srv.URL))
	req, _ := c.newRequest(ctx, http.MethodGet, "/x", nil)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.do(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v — cancellation did not interrupt the 30s Retry-After wait", elapsed)
	}
}

// TestRetry_ExhaustionReturnsTheRealResponse: after the last attempt the
// caller must see the actual status and body, not a synthesised "gave up"
// error that hides request_id.
func TestRetry_ExhaustionReturnsTheRealResponse(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"object":"error","status":429,"code":"rate_limited","request_id":"rq-1"}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), fastRetry())
	req, _ := c.newRequest(context.Background(), http.MethodGet, "/x", nil)
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do returned a transport error: %v", err)
	}

	var page struct{}
	decErr := decodeInto(resp, &page)
	var apiErr *APIError
	if !errors.As(decErr, &apiErr) {
		t.Fatalf("want an *APIError after exhaustion, got %v", decErr)
	}
	if apiErr.RequestID != "rq-1" || !apiErr.IsRateLimited() {
		t.Errorf("exhausted error lost detail: %+v", apiErr)
	}
	if got := atomic.LoadInt64(&calls); got != int64(DefaultMaxAttempts) {
		t.Errorf("made %d attempts, want %d", got, DefaultMaxAttempts)
	}
}

// TestRetry_DisabledMakesOneAttempt covers WithMaxRetries(0), which the
// error-propagation tests rely on to stay fast.
func TestRetry_DisabledMakesOneAttempt(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), WithMaxRetries(0))
	req, _ := c.newRequest(context.Background(), http.MethodGet, "/x", nil)
	resp, _ := c.do(req)
	drain(resp)

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("made %d requests with retries disabled, want 1", got)
	}
}

// TestRetry_BackoffGrowsAndIsClamped checks the shape of the delay curve.
func TestRetry_BackoffGrowsAndIsClamped(t *testing.T) {
	p := retryPolicy{base: 100 * time.Millisecond, maxWait: time.Second}
	var prev time.Duration
	for attempt := 0; attempt < 3; attempt++ {
		d := retryDelay(p, attempt, nil)
		if d < prev {
			t.Errorf("attempt %d delay %v is shorter than the previous %v", attempt, d, prev)
		}
		if d > p.maxWait {
			t.Errorf("attempt %d delay %v exceeds maxWait %v", attempt, d, p.maxWait)
		}
		prev = d
	}
	// Far past the ceiling, including where the shift would overflow.
	if d := retryDelay(p, 62, nil); d != p.maxWait {
		t.Errorf("a huge attempt number gave %v, want the clamp %v", d, p.maxWait)
	}
}
