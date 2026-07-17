package client

import (
	"net/http"
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
