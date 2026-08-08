package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// passthrough forwards a READ of one merchant's flexitype tenant, with that
// merchant's service-account token attached here.
//
// It exists so the merchant console can use the TypeScript SDK — its client,
// its React hooks and its soft-typing helpers — against the real API surface
// without the browser ever holding a merchant credential. The console builds
// its client with
//
//	createClient({ baseUrl: '/api/merchants/alpine/flexitype' })
//
// which the SDK turns into `/api/merchants/alpine/flexitype/api/v1/...`, so
// every path below this route is a genuine flexitype path.
//
// The forwarding rules are narrow on purpose:
//
//   - GET and HEAD only. A write goes through this service's own endpoints,
//     which batch a whole product into ONE atomic call. A raw passthrough
//     write would let the console leave a half-written product behind, which
//     is exactly what the storefront must never project.
//   - Nothing under `admin/`. Those endpoints create tenants and service
//     accounts. A merchant token cannot use them anyway; refusing here states
//     the intent rather than relying on the other side to say no.
//   - The merchant comes from the PATH, and the token is looked up from it.
//     There is no header a caller can send that selects a different merchant.
//
// The response is streamed back with a small allow-list of headers. A
// `Set-Cookie` or an authentication header from the upstream is dropped rather
// than relayed onto the console's origin.
type passthrough struct {
	store   *Store
	baseURL string
	http    *http.Client
	log     Logger
}

func newPassthrough(store *Store, baseURL string, log Logger) *passthrough {
	return &passthrough{
		store:   store,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}
}

// forwardedResponseHeaders is what a proxied response may carry back. Anything
// else — cookies, upstream authentication headers, transport headers — is
// dropped.
var forwardedResponseHeaders = []string{
	"Content-Type",
	"Content-Length",
	"ETag",
	"Last-Modified",
	"Cache-Control",
}

func (p *passthrough) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed,
			"this passthrough is read-only; write through the merchant API so the whole product lands in one atomic batch")
		return
	}

	merchant, ok, err := p.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		p.log.Error("passthrough merchant lookup", "error", err)
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such merchant")
		return
	}

	rest := strings.TrimPrefix(r.PathValue("path"), "/")
	if rest == "" || strings.Contains(rest, "..") {
		writeError(w, http.StatusNotFound, "no such path")
		return
	}
	if strings.HasPrefix(rest, "admin/") || rest == "admin" {
		writeError(w, http.StatusForbidden, "the admin API is not reachable through the merchant console")
		return
	}

	target := p.baseURL + "/api/v1/" + rest
	if raw := r.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	// Parse before use so a path segment that is not a valid URL fails here
	// rather than inside the transport.
	parsed, err := url.Parse(target)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such path")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, parsed.String(), nil)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such path")
		return
	}
	req.Header.Set("Authorization", "Bearer "+merchant.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "marketplace-platform-passthrough")

	resp, err := p.http.Do(req)
	if err != nil {
		// The error can carry the request URL but never the header, so the
		// token cannot reach a log line through it.
		p.log.Error("passthrough request", "merchant", merchant.ID, "error", err)
		writeError(w, http.StatusBadGateway, "flexitype did not answer")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for _, name := range forwardedResponseHeaders {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		p.log.Error("passthrough copy", "merchant", merchant.ID, "error", err)
	}
}
