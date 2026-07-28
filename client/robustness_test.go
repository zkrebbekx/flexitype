package client

import (
	"context"
	"encoding/json"
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

// Given a role write, When UpsertRole sends it, Then the request is a PUT to
// /api/v1/roles carrying the whole permission set, because a role write
// replaces the role rather than patching it.
func TestUpsertRoleSendsWholeRole(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"01","name":"analyst","tenant_id":"acme",
			"scopes":["read"],"field_permissions":{"salary":"none"}}`))
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	role, err := c.Admin().UpsertRole(context.Background(), UpsertRoleInput{
		TenantName:       "acme",
		Name:             "analyst",
		Scopes:           []string{"read"},
		FieldPermissions: map[string]string{"salary": "none"},
	})
	if err != nil {
		t.Fatalf("UpsertRole: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/api/v1/roles" {
		t.Fatalf("path = %s, want /api/v1/roles", gotPath)
	}
	if gotBody["tenant_name"] != "acme" || gotBody["name"] != "analyst" {
		t.Fatalf("body did not carry the role identity: %v", gotBody)
	}
	perms, ok := gotBody["field_permissions"].(map[string]any)
	if !ok || perms["salary"] != "none" {
		t.Fatalf("body did not carry the permission set: %v", gotBody)
	}
	if role.FieldPermissions["salary"] != "none" {
		t.Fatalf("decoded role lost its permission set: %+v", role)
	}
}

// Given a tenant name, When ListRoles reads them, Then the tenant travels as
// the tenant_name query parameter the endpoint requires, and an empty name is
// refused before a request is made — the server has no cross-tenant listing.
func TestListRolesRequiresTenant(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"01","name":"analyst"}]}`))
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	roles, err := c.Admin().ListRoles(context.Background(), "acme")
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if gotQuery != "tenant_name=acme" {
		t.Fatalf("query = %q, want tenant_name=acme", gotQuery)
	}
	if len(roles) != 1 || roles[0].Name != "analyst" {
		t.Fatalf("roles = %+v, want one named analyst", roles)
	}
	if _, err := c.Admin().ListRoles(context.Background(), ""); err == nil {
		t.Fatal("expected an empty tenant name to be refused")
	}
}

// Given a role name, When DeleteRole removes it, Then the tenant travels in
// the query string while the name travels in the path, and an empty tenant is
// refused before a request is made.
func TestDeleteRoleCarriesTenantAndName(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Admin().DeleteRole(context.Background(), "acme", "analyst"); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/roles/analyst" {
		t.Fatalf("path = %s, want /api/v1/roles/analyst", gotPath)
	}
	if gotQuery != "tenant_name=acme" {
		t.Fatalf("query = %q, want tenant_name=acme", gotQuery)
	}
	if err := c.Admin().DeleteRole(context.Background(), "", "analyst"); err == nil {
		t.Fatal("expected an empty tenant name to be refused")
	}
}

// Given an account id, When AssignRoles replaces its roles, Then the request
// is a PUT to that account's roles sub-resource carrying both lists, because
// the endpoint replaces rather than merges.
func TestAssignRolesReplacesBothLists(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.Admin().AssignRoles(context.Background(), "01ACCOUNT", AssignRolesInput{
		Roles:            []string{"analyst"},
		FieldPermissions: map[string]string{"ssn": "none"},
	})
	if err != nil {
		t.Fatalf("AssignRoles: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/api/v1/service-accounts/01ACCOUNT/roles" {
		t.Fatalf("path = %s, want the account's roles sub-resource", gotPath)
	}
	roles, ok := gotBody["roles"].([]any)
	if !ok || len(roles) != 1 || roles[0] != "analyst" {
		t.Fatalf("body did not carry the role list: %v", gotBody)
	}
	if _, ok := gotBody["field_permissions"]; !ok {
		t.Fatal("body omitted field_permissions, so the overrides could not be cleared")
	}
}

// Given the server's nested feed shape, When FeedEvent decodes it, Then the
// flat fields are populated — the client type had no key in common with the
// wire form, so List returned the right NUMBER of events with every field
// zero and no error, and a consumer dispatching on Type saw "" for ever.
func TestFeedEventDecodesTheNestedEnvelope(t *testing.T) {
	raw := []byte(`{"seq":42,"envelope":{
		"id":"01ABC","type":"flexitype.value.updated","aggregate_type":"value",
		"aggregate_id":"v1","tenant_id":"acme","actor":"ci",
		"occurred_at":"2026-07-28T10:00:00Z","recorded_at":"2026-07-28T10:00:01Z",
		"schema_version":1,"payload":{"entity_id":"e1"}}}`)

	var ev FeedEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.Seq != 42 {
		t.Fatalf("seq = %d, want 42", ev.Seq)
	}
	if ev.Type != "flexitype.value.updated" {
		t.Fatalf("type = %q, want the envelope's type", ev.Type)
	}
	if ev.ID != "01ABC" || ev.TenantID != "acme" || ev.Actor != "ci" {
		t.Fatalf("identity fields not lifted from the envelope: %+v", ev)
	}
	if ev.OccurredAt.IsZero() || ev.RecordedAt.IsZero() || ev.SchemaVersion != 1 {
		t.Fatalf("envelope metadata not lifted: %+v", ev)
	}
	if string(ev.Payload) == "" {
		t.Fatal("payload not lifted from the envelope")
	}
}

// Given the feed's int64 cursor, When ListPage reads a page, Then it decodes
// and the cursor round-trips — NextCursor was declared a string while the
// server always emits a number, so this call failed on every invocation,
// including against an empty feed.
func TestListPageDecodesTheIntegerCursor(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"seq":7,"envelope":{"id":"01","type":"t"}}],"next_cursor":7}`))
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page, err := c.Events().ListPage(context.Background(), 3, nil, 0)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if page.NextCursor != 7 {
		t.Fatalf("next cursor = %d, want 7", page.NextCursor)
	}
	if len(page.Events) != 1 || page.Events[0].Seq != 7 || page.Events[0].Type != "t" {
		t.Fatalf("events not decoded: %+v", page.Events)
	}
	if gotQuery != "after=3" {
		t.Fatalf("query = %q, want after=3", gotQuery)
	}

	// The cursor a page returns must be usable as the next page's After.
	if _, err := c.Events().ListPage(context.Background(), page.NextCursor, nil, 0); err != nil {
		t.Fatalf("resume: %v", err)
	}
}

// Given a saved-view full replace with no columns, When Update sends it, Then
// columns travels as [] — the server reads null as "absent", so a nil slice
// made Update neither a full replace nor sparse: it cleared query and sort
// while leaving columns as stored.
func TestSavedViewUpdateAlwaysSendsColumns(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"01","name":"renamed","version":2}`))
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.SavedViews().Update(context.Background(), "01", SavedViewInput{
		Name: "renamed", RootType: "product",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	cols, ok := gotBody["columns"]
	if !ok {
		t.Fatal("columns omitted, so the replace would leave the stored value")
	}
	if arr, isArr := cols.([]any); !isArr || len(arr) != 0 {
		t.Fatalf("columns = %v, want an empty array", cols)
	}
}

// Given a revision value with a scope and a typed form, When it decodes, Then
// locale, channel and typed survive — without them AsOf returned a localized
// entity as N rows with identical InternalName and no way to tell them apart,
// and rendered a quantity as the lossy display string.
func TestRevisionValueCarriesScopeAndTypedForm(t *testing.T) {
	raw := []byte(`{"attribute_definition_id":"01","internal_name":"price",
		"display_name":"Price","data_type":"quantity","locale":"fr-FR","channel":"web",
		"value":"10 kg","typed":{"type":"quantity","magnitude":"10","unit":"kg"}}`)

	var v RevisionValue
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Locale != "fr-FR" || v.Channel != "web" {
		t.Fatalf("scope lost: %+v", v)
	}
	if len(v.Typed) == 0 {
		t.Fatal("typed form lost, so a quantity cannot be reconstructed")
	}
	if v.Value != "10 kg" {
		t.Fatalf("display form = %q", v.Value)
	}
}
