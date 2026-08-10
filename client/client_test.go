package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// This module is deliberately dependency-free (see the package doc), so its
// tests use the standard library's testing package rather than the BDD helper
// the rest of the repository uses — keeping the SDK's go.mod at zero deps.

// Given a client built with no custom HTTP client, When New returns it, Then
// its HTTP client carries the 30s default request timeout that guards callers
// against a stalled server.
func TestNewAppliesDefaultTimeout(t *testing.T) {
	c, err := New("https://flexitype.internal")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http == nil {
		t.Fatal("expected a default HTTP client, got nil")
	}
	if c.http.Timeout != defaultTimeout {
		t.Fatalf("default timeout = %v, want %v", c.http.Timeout, defaultTimeout)
	}
	if defaultTimeout != 30*time.Second {
		t.Fatalf("defaultTimeout = %v, want 30s", defaultTimeout)
	}
}

// Given WithHTTPClient with a custom client, When New applies it, Then that
// client (and its timeout) replaces the default.
func TestWithHTTPClientOverridesDefault(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	c, err := New("https://flexitype.internal", WithHTTPClient(custom))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http != custom {
		t.Fatal("expected the custom HTTP client to replace the default")
	}
	if c.http.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", c.http.Timeout)
	}
}

// Given WithHTTPClient(nil), When New applies it, Then the nil is ignored and
// the timed default client is retained rather than reverting to a
// no-timeout http.DefaultClient.
func TestWithHTTPClientIgnoresNil(t *testing.T) {
	c, err := New("https://flexitype.internal", WithHTTPClient(nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http == nil || c.http.Timeout != defaultTimeout {
		t.Fatalf("nil custom client should retain the default timeout, got %+v", c.http)
	}
}

// Given an update that names the version the caller read, When the client
// encodes it, Then the wire body carries `version` — issue #597, where the
// compare-and-swap existed on the server and no client ever sent one.
func TestUpdateAttributeSendsTheVersionRead(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a1","version":8}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	read := 7
	if _, err := c.Attributes().Update(context.Background(), "a1", UpdateAttributeInput{
		DisplayName: "SKU", Version: &read,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["version"] != float64(7) {
		t.Fatalf("version = %v, want 7 — the swap guards nothing without it", sent["version"])
	}
}

// Given an update with no version, When the client encodes it, Then the field
// is absent, so a caller written before the swap existed keeps last-write-wins
// rather than swapping against version zero and failing every write.
func TestUpdateAttributeOmitsAnAbsentVersion(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a1","version":8}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Attributes().Update(context.Background(), "a1", UpdateAttributeInput{
		DisplayName: "SKU",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if _, present := sent["version"]; present {
		t.Fatalf("version must be absent when unset, body = %s", body)
	}
}
