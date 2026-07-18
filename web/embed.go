// Package web embeds the built admin console SPA. A committed stub
// index.html keeps `go build` working without Node; `npm run build` in
// web/ replaces the stub with the real console before release builds.
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
