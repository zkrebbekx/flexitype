package http

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/web"
)

// spaHandler serves the embedded admin console: real files by path, and
// index.html for every client-side route so deep links survive refresh.
func spaHandler(log *logger.Logger) http.HandlerFunc {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Error().Err(err).Msg("admin console assets unavailable")
		return func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "admin console unavailable", http.StatusInternalServerError)
		}
	}
	fileServer := http.FileServerFS(dist)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if f, err := dist.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Client-side route: hand the app shell to the SPA router.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}
