package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This module is deliberately dependency-free (see the package doc), so these
// tests use the standard library's testing package rather than the BDD helper
// the rest of the repository uses.

// Given base URLs an adopter might type, When New parses them, Then a URL no
// request can use is refused with a message naming the problem.
//
// url.Parse alone rejects almost nothing: it reads "localhost:8080" as the
// scheme "localhost" with opaque "8080", so the most natural thing to type
// for a local service constructed successfully and then failed every request
// with "unsupported protocol scheme" — pointing an adopter at their network
// or their token rather than at one missing "http://".
func TestNewRejectsUnusableBaseURL(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"localhost:8080", "scheme"},
		{"flexitype.internal", "scheme"},
		{"not a url", "scheme"},
		{"http://", "host"},
		{"", "required"},
	} {
		c, err := New(tc.in)
		if err == nil {
			t.Errorf("New(%q) = accepted; want an error", tc.in)
			continue
		}
		if c != nil {
			t.Errorf("New(%q) returned a client alongside its error", tc.in)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("New(%q) error = %q; want it to mention %q", tc.in, err, tc.want)
		}
	}

	for _, in := range []string{
		"http://localhost:8080",
		"https://flexitype.internal",
		"http://127.0.0.1:9000/api/v1",
	} {
		if _, err := New(in); err != nil {
			t.Errorf("New(%q) = %v; want it accepted", in, err)
		}
	}
}

// Given a server that throttles with a Retry-After header, When the client
// decodes the failure, Then the wait hint reaches the caller.
//
// decodeError read only the body and the status; resp.Header was never
// consulted, so the hint the server sets on every throttle was dropped and
// callers either failed outright or invented their own backoff.
func TestRetryAfterReachesTheCaller(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"slow down"}}`))
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Features(context.Background())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Features error = %v; want an *APIError", err)
	}
	if apiErr.Code != CodeRateLimited {
		t.Errorf("code = %q; want %q", apiErr.Code, CodeRateLimited)
	}
	if apiErr.RetryAfter != 2*time.Second {
		t.Errorf("RetryAfter = %v; want 2s", apiErr.RetryAfter)
	}
}

// Given the two forms RFC 9110 allows for Retry-After, When they are parsed,
// Then both yield a wait, and a date in the past yields zero rather than a
// negative wait.
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{" 5 ", 5 * time.Second},
		{"0", 0},
		{"-3", 0},
		{"garbage", 0},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		if got := parseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

// Given a server that throttles once and then succeeds, When retrying is
// enabled, Then the call succeeds on a later attempt; with retrying off it
// fails after exactly one.
func TestRetryPolicyRetriesIdempotentCalls(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"slow down"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"search":true}`))
	}))
	defer ts.Close()

	off, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := off.Features(context.Background()); err == nil {
		t.Fatal("Features succeeded with retrying off; want the throttle to surface")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts with retrying off = %d; want 1", got)
	}

	calls.Store(0)
	on, err := New(ts.URL, WithRetry(fastPolicy()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	features, err := on.Features(context.Background())
	if err != nil {
		t.Fatalf("Features with retrying on: %v", err)
	}
	if !features.Search {
		t.Error("decoded the retried response wrongly")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("attempts with retrying on = %d; want 2", got)
	}
}

// Given a server that always throttles, When a POST is retried, Then it is
// not: a create may already have been applied before the failure, so
// replaying it can create a second resource. A GET is retried to the limit.
func TestRetryLeavesUnsafeMethodsAlone(t *testing.T) {
	var posts, gets atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		} else {
			gets.Add(1)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"no"}}`))
	}))
	defer ts.Close()

	policy := fastPolicy()
	c, err := New(ts.URL, WithRetry(policy))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if _, err := c.Types().Create(ctx, CreateTypeInput{InternalName: "p", DisplayName: "P"}); err == nil {
		t.Fatal("Create succeeded against a throttling server")
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("POST attempts = %d; want 1 — a POST must never be replayed", got)
	}

	if _, err := c.Features(ctx); err == nil {
		t.Fatal("Features succeeded against a throttling server")
	}
	if got := int(gets.Load()); got != policy.MaxAttempts {
		t.Errorf("GET attempts = %d; want %d", got, policy.MaxAttempts)
	}
}

// Given a policy with a long backoff, When the caller's context is already
// cancelled, Then the retry loop stops rather than outliving the caller.
func TestRetryHonoursContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"down"}}`))
	}))
	defer ts.Close()

	policy := DefaultRetryPolicy()
	policy.BaseDelay = time.Hour // hangs if the loop ignores the context
	c, err := New(ts.URL, WithRetry(policy))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, e := c.Features(ctx); done <- e }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Features succeeded against a failing server")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the retry loop outlived the cancelled context")
	}
}

// Given a server-supplied wait hint, When the backoff is computed, Then the
// hint wins over the client's own delay: the server knows when its token
// bucket refills and the client does not.
func TestWaitBeforePrefersTheServerHint(t *testing.T) {
	p := DefaultRetryPolicy()
	p.Jitter = 0
	if got := p.waitBefore(2, 3*time.Second); got != 3*time.Second {
		t.Errorf("waitBefore with a hint = %v; want 3s", got)
	}
	if got := p.waitBefore(2, 0); got != p.BaseDelay {
		t.Errorf("waitBefore(2) = %v; want the base delay %v", got, p.BaseDelay)
	}
	if got := p.waitBefore(3, 0); got != 2*p.BaseDelay {
		t.Errorf("waitBefore(3) = %v; want %v", got, 2*p.BaseDelay)
	}
	if got := p.waitBefore(20, 0); got != p.MaxDelay {
		t.Errorf("waitBefore(20) = %v; want it capped at %v", got, p.MaxDelay)
	}
}

// Given a server that always fails, When a pointer-returning method is
// called, Then it returns nil rather than a pointer to a zero value.
//
// Roughly fifty methods returned a non-nil pointer alongside the error while
// a handful returned nil. Someone who learned nil-on-error from Features and
// applied it to Get got the opposite, and code that nil-checked instead of
// error-checking carried on with an empty id and zero timestamps — a bug that
// surfaced far from this package.
func TestPointerMethodsReturnNilOnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"nope"}}`))
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if v, err := c.Types().Get(ctx, "01J"); err == nil || v != nil {
		t.Errorf("Types().Get = (%v, %v); want (nil, error)", v, err)
	}
	if v, err := c.Attributes().Get(ctx, "01J"); err == nil || v != nil {
		t.Errorf("Attributes().Get = (%v, %v); want (nil, error)", v, err)
	}
	if v, err := c.SavedViews().Get(ctx, "01J"); err == nil || v != nil {
		t.Errorf("SavedViews().Get = (%v, %v); want (nil, error)", v, err)
	}
	if v, err := c.Features(ctx); err == nil || v != nil {
		t.Errorf("Features = (%v, %v); want (nil, error)", v, err)
	}
}

// Given a forward-only keyset API, When a caller pages back and forth, Then
// CursorStack supplies the cursor for each page.
//
// PageInfo.HasPreviousPage reports that a previous page exists, but the API
// has no "before" cursor and no direction, so an adopter who wires a Back
// button finds nothing to call. The field reads like a supported capability
// and nothing said otherwise.
func TestCursorStack(t *testing.T) {
	cursor := func(s string) *string { return &s }
	var st CursorStack

	if got := st.Current(); got != "" {
		t.Errorf("first page cursor = %q; want empty", got)
	}
	if got := st.Depth(); got != 0 {
		t.Errorf("first page depth = %d; want 0", got)
	}
	if _, ok := st.Pop(); ok {
		t.Error("Pop on the first page reported success; want false")
	}

	if next, ok := st.Push(PageInfo{HasNextPage: true, NextCursor: cursor("c1")}); !ok || next != "c1" {
		t.Fatalf("Push = (%q, %v); want (\"c1\", true)", next, ok)
	}
	if next, ok := st.Push(PageInfo{HasNextPage: true, NextCursor: cursor("c2")}); !ok || next != "c2" {
		t.Fatalf("Push = (%q, %v); want (\"c2\", true)", next, ok)
	}
	if got := st.Depth(); got != 2 {
		t.Errorf("depth after two pushes = %d; want 2", got)
	}

	if back, ok := st.Pop(); !ok || back != "c1" {
		t.Errorf("Pop = (%q, %v); want (\"c1\", true)", back, ok)
	}
	if back, ok := st.Pop(); !ok || back != "" {
		t.Errorf("Pop to the first page = (%q, %v); want (\"\", true)", back, ok)
	}
	if _, ok := st.Pop(); ok {
		t.Error("Pop past the first page reported success; want false")
	}

	// A Next button at the end of a list must be a no-op, not an empty page.
	var end CursorStack
	if _, ok := end.Push(PageInfo{HasNextPage: false}); ok {
		t.Error("Push with no next page reported success")
	}
	if _, ok := end.Push(PageInfo{HasNextPage: true}); ok {
		t.Error("Push with no cursor reported success; it would re-request this page")
	}
}

// fastPolicy retries as the default does, without the waiting.
func fastPolicy() RetryPolicy {
	p := DefaultRetryPolicy()
	p.BaseDelay = time.Millisecond
	p.MaxDelay = time.Millisecond
	p.Jitter = 0
	return p
}
