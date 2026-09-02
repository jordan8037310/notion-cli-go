// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// Retry defaults. Notion rate-limits at roughly 3 requests/second and
// answers 429 with a Retry-After header, so a paginated walk through a
// large database will hit it eventually. Without retries that surfaced as
// a user-visible failure part-way through — worse on a write path, where
// the caller cannot tell how far it got.
const (
	DefaultMaxAttempts  = 4
	DefaultRetryBase    = 500 * time.Millisecond
	DefaultRetryMaxWait = 30 * time.Second
)

// retryPolicy governs do()'s retry loop.
type retryPolicy struct {
	maxAttempts int
	base        time.Duration
	maxWait     time.Duration
	// sleep is indirected so tests can run the loop without real delays.
	sleep func(context.Context, time.Duration) error
}

func defaultRetryPolicy() retryPolicy {
	return retryPolicy{
		maxAttempts: DefaultMaxAttempts,
		base:        DefaultRetryBase,
		maxWait:     DefaultRetryMaxWait,
		sleep:       sleepCtx,
	}
}

// WithMaxRetries caps how many times a request is retried. n is the number
// of RETRIES, so 0 disables them and the client makes a single attempt.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n < 0 {
			n = 0
		}
		c.retry.maxAttempts = n + 1
	}
}

// WithRetryBackoff overrides the exponential-backoff base and the ceiling
// any single wait is clamped to.
func WithRetryBackoff(base, max time.Duration) Option {
	return func(c *Client) {
		if base > 0 {
			c.retry.base = base
		}
		if max > 0 {
			c.retry.maxWait = max
		}
	}
}

// sleepCtx waits for d, or returns early if the context is cancelled. A
// plain time.Sleep would ignore ctx and make ctrl-C feel broken during a
// 30-second backoff.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// isIdempotent reports whether a method is safe to replay when the server
// gave no usable answer.
//
// GET and DELETE only — deliberately NOT PATCH or POST, and this is the
// load-bearing decision in the whole file. A 502 on POST /v1/pages may mean
// the page WAS created and the gateway lost the response; retrying would
// create a second one. PATCH is not safe either, because
// PATCH /v1/blocks/{id}/children APPENDS — a replay would duplicate blocks,
// which is exactly the silent-duplication failure `blocks list` used to
// cause (#88).
//
// Notion's own published retry example draws the line in the same place.
func isIdempotent(method string) bool {
	return method == http.MethodGet || method == http.MethodDelete
}

// shouldRetry decides whether to try again, given a response and/or a
// transport error.
//
//   - transport error (no response at all): retry only idempotent methods.
//     A POST that failed mid-flight may still have been applied.
//   - 429 / 529: always retry. These mean "too fast, try again" and carry
//     no risk of duplication, whatever the method.
//   - 5xx: retry idempotent methods only, per the reasoning above.
//   - every other 4xx: never. The request is wrong; retrying hammers the
//     API and delays a real error the caller needs to see.
func shouldRetry(method string, resp *http.Response, err error) bool {
	if err != nil {
		return isIdempotent(method)
	}
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, 529:
		return true
	case http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return isIdempotent(method)
	}
	return false
}

// retryDelay picks how long to wait before attempt n (0-based).
//
// Retry-After wins when the server sends it: Notion documents it on 429 and
// 529, and guessing shorter just earns another 429. Otherwise exponential
// backoff from base, plus jitter so concurrent clients do not resynchronise
// into a thundering herd. Always clamped to maxWait.
func retryDelay(p retryPolicy, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if secs, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			if secs > p.maxWait {
				return p.maxWait
			}
			return secs
		}
	}
	d := p.base << attempt
	if d > p.maxWait || d <= 0 {
		d = p.maxWait
	}
	// Up to 25% jitter, added rather than subtracted so a delay never
	// undercuts the backoff it was derived from.
	jitter := time.Duration(rand.Int63n(int64(d/4) + 1))
	if d+jitter > p.maxWait {
		return p.maxWait
	}
	return d + jitter
}

// parseRetryAfter reads the delta-seconds form of Retry-After, which is
// what Notion sends. The HTTP-date form is legal but unused here; an
// unparseable value falls back to backoff rather than erroring, because a
// header we cannot read is not a reason to give up on the request.
func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// drain empties and closes a response body so the connection returns to the
// pool. Skipping this on a retried response leaks a connection per attempt.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}
