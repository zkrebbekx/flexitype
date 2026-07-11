// Package web embeds the built admin console SPA. A committed stub
// index.html keeps `go build` working without Node; `npm run build` in
// web/ replaces the stub with the real console before release builds.
package web

import "embed"

// Dist holds the built SPA (vite output).
//
//go:embed all:dist
var Dist embed.FS
