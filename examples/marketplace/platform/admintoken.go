package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// TokenSource returns the flexitype admin credential to present on the next
// request.
//
// It is a function rather than a string because the credential can change
// while this process runs: a secret manager rotates it, or — in the compose
// stack — seed.sh writes the file after the platform has already started.
// Reading it once at boot left the platform authenticating with a stale token
// until somebody restarted it, and every admin call failed with "invalid
// credentials".
type TokenSource func() (string, error)

// staticToken is a TokenSource over one fixed value.
func staticToken(token string) TokenSource {
	return func() (string, error) { return token, nil }
}

// fileToken reads the token from path, re-reading it whenever the file
// changes. The stat is cheap and happens per request; the read only happens
// when the file's size or modification time moved.
type fileToken struct {
	path string

	mu      sync.Mutex
	token   string
	modTime time.Time
	size    int64
}

func newFileToken(path string) *fileToken { return &fileToken{path: path} }

// Token returns the file's current contents, trimmed.
func (f *fileToken) Token() (string, error) {
	info, err := os.Stat(f.path)
	if err != nil {
		return "", fmt.Errorf("read admin token %s: %w", f.path, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && info.ModTime().Equal(f.modTime) && info.Size() == f.size {
		return f.token, nil
	}
	raw, err := os.ReadFile(f.path) //nolint:gosec // an operator-supplied secret path
	if err != nil {
		return "", fmt.Errorf("read admin token %s: %w", f.path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("admin token file %s is empty", f.path)
	}
	f.token, f.modTime, f.size = token, info.ModTime(), info.Size()
	return token, nil
}

// waitFor blocks until the file holds a token, so the platform can be started
// before the credential is mounted.
func (f *fileToken) waitFor(ctx context.Context, log Logger) error {
	logged := false
	for {
		if _, err := f.Token(); err == nil {
			return nil
		}
		if !logged {
			log.Info("waiting for the flexitype admin token", "file", f.path)
			logged = true
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("no admin token at %s: %w", f.path, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// newAdminTokenSource resolves the admin credential from the environment.
func newAdminTokenSource(ctx context.Context, log Logger) (TokenSource, error) {
	if token := os.Getenv("FLEXITYPE_ADMIN_TOKEN"); token != "" {
		return staticToken(token), nil
	}
	// flexitype prints its bootstrap admin token to stdout ONCE, and its image
	// is distroless, so a compose stack has no way to capture it into an
	// environment variable. This file is how the credential arrives; in a real
	// deployment it is a mounted secret.
	path := os.Getenv("FLEXITYPE_ADMIN_TOKEN_FILE")
	if path == "" {
		return nil, errors.New("set FLEXITYPE_ADMIN_TOKEN or FLEXITYPE_ADMIN_TOKEN_FILE")
	}
	source := newFileToken(path)
	if err := source.waitFor(ctx, log); err != nil {
		return nil, err
	}
	return source.Token, nil
}

// bearerTransport presents the CURRENT admin token on every request.
//
// client.WithToken captures one string when the client is built, so a client
// built with it can never pick a rotated credential up. This transport asks
// the source per request instead.
type bearerTransport struct {
	source TokenSource
	base   http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := b.source()
	if err != nil {
		return nil, err
	}
	// A RoundTripper must not modify the request it is given.
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
