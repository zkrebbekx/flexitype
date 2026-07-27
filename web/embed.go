// Package web embeds the built admin console SPA.
//
// dist/index.html is committed as a stub so that `go build` works from a
// clean checkout with no Node toolchain. Keep it committed: `all:dist` is a
// hard error when the directory is missing or holds nothing embeddable, so
// deleting it breaks `go build` in a package unrelated to whatever the
// caller was changing. `.gitignore` ignores everything else under dist/, so
// real build output is never committed.
//
// The console therefore ships only in the container image and the release
// binaries, both of which run `npm run build` first. A binary built any
// other way serves the stub, which says so.
package web

import "embed"

// Dist holds the built SPA (vite output).
//
//go:embed all:dist
var Dist embed.FS

// IndexHTML is the source console template. Vite copies its inline
// pre-paint theme <script> into the built dist/index.html verbatim, so its
// SHA-256 is the one pinned in the API server's Content-Security-Policy
// (a test cross-checks the two, so editing the inline script fails CI until
// the CSP hash is updated).
//
//go:embed index.html
var IndexHTML string
