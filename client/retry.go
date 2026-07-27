package client

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"
)

// RetryPolicy controls whether and how a request is retried. The zero value
// retries nothing; build one with DefaultRetryPolicy and adjust it.
//
// Retrying is opt-in because it is not safe in general: a POST that creates a
// resource may have succeeded before the connection broke, so replaying it
// can create a second one. Only idempotent methods are ever retried — GET,
// HEAD, PUT and DELETE — and a POST is left alone whatever the policy says.
type RetryPolicy struct {
	// MaxAttempts counts the first try. 1 (or 0) disables retrying.
	MaxAttempts int
	// BaseDelay is the wait before the second attempt. Each further attempt
	// doubles it, up to MaxDelay.
	BaseDelay time.Duration
	// MaxDelay caps the backoff.
	MaxDelay time.Duration
	// Jitter scales the random part of each wait, as a fraction of the
	// computed delay (0.2 means ±20%). Without it, many clients throttled at
	// once retry in lockstep and throttle each other again.
	Jitter float64
	// RetryStatuses are the response statuses worth another attempt. A
	// status not listed here fails immediately, whatever its class.
	RetryStatuses []int
}

// DefaultRetryPolicy retries an idempotent request up to three times on 429
// and on the transient 5xx statuses a proxy or a restarting server emits,
// with exponential backoff from 200ms to 5s and ±20% jitter.
//
// A server-supplied Retry-After always wins over the computed delay: the
// server knows when its token bucket refills and the client does not.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:   3,
		BaseDelay:     200 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		Jitter:        0.2,
		RetryStatuses: []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
	}
}

// WithRetry enables retrying with the given policy.
//
// The paginating All(...) iterators are the sharp case: they issue many
// requests in tight succession, which is exactly the shape a per-account
// token bucket targets, so without retrying a bulk walk aborts part-way with
// no resumption point.
func WithRetry(p RetryPolicy) Option {
	return func(c *Client) { c.retry = p }
}

// retries reports whether the policy would retry this method and status.
// A POST is never retried: it may have been applied before the failure.
func (p RetryPolicy) retries(method string, status int) bool {
	if p.MaxAttempts < 2 || !idempotent(method) {
		return false
	}
	for _, s := range p.RetryStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// retriesTransport reports whether a transport-level failure (a reset
// connection, a refused dial) is worth another attempt. The request may not
// have reached the server at all, so this is still limited to idempotent
// methods.
func (p RetryPolicy) retriesTransport(method string) bool {
	return p.MaxAttempts >= 2 && idempotent(method)
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// waitBefore returns the delay before attempt number n (n starting at 2 for
// the first retry). serverHint, when non-zero, replaces the computed backoff.
func (p RetryPolicy) waitBefore(n int, serverHint time.Duration) time.Duration {
	if serverHint > 0 {
		if p.MaxDelay > 0 && serverHint > p.MaxDelay {
			// Honour the server's hint even past MaxDelay: waiting less than
			// it asks only earns another 429.
			return serverHint
		}
		return serverHint
	}
	delay := p.BaseDelay
	for i := 2; i < n; i++ {
		delay *= 2
		if p.MaxDelay > 0 && delay > p.MaxDelay {
			delay = p.MaxDelay
			break
		}
	}
	if p.Jitter > 0 {
		// rand/v2's global source is safe for concurrent use.
		spread := float64(delay) * p.Jitter
		delay = time.Duration(float64(delay) - spread + rand.Float64()*2*spread)
	}
	if delay < 0 {
		return 0
	}
	return delay
}

// sleep waits for d or until ctx ends, whichever comes first. It reports
// whether the wait completed; a cancelled context stops the retry loop rather
// than letting it outlive the caller's deadline.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// retryHint extracts the server's Retry-After from a typed API error.
func retryHint(err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}

// statusOf extracts the HTTP status from a typed API error, or 0.
func statusOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}
