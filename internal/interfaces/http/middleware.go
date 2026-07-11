package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// withInteractors builds one interactor set per request — one dataloader
// generation shared by everything the request touches — and stows it on the
// context for handlers to pull via application.FromContext.
func withInteractors(factory application.Factory) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := application.WithInteractors(r.Context(), factory.New(r.Context()))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authenticate resolves the bearer token to a service account, stamping
// actor and tenant onto the request context. A nil store disables
// authentication (development mode) and runs as the system actor on the
// default tenant.
func authenticate(store *serviceaccount.Store, log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store == nil {
				next.ServeHTTP(w, r.WithContext(uow.WithActor(r.Context(), uow.Actor{
					Name: "dev",
					Kind: uow.ActorSystem,
				})))
				return
			}

			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				writeUnauthorized(w, "missing bearer token")
				return
			}
			account, err := store.Authenticate(token)
			if err != nil {
				log.Warn().Err(err).Msg("authentication failed")
				writeUnauthorized(w, "invalid credentials")
				return
			}

			required := serviceaccount.ScopeWrite
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				required = serviceaccount.ScopeRead
			}
			if !account.HasScope(required) {
				writeForbidden(w, "missing scope "+string(required))
				return
			}

			ctx := uow.WithActor(r.Context(), uow.Actor{
				ID:   account.ID,
				Name: account.Name,
				Kind: uow.ActorServiceAccount,
			})
			ctx = uow.WithTenant(ctx, account.Tenant())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	var body errorBody
	body.Error.Code = "UNAUTHENTICATED"
	body.Error.Message = msg
	writeJSON(w, http.StatusUnauthorized, body)
}

func writeForbidden(w http.ResponseWriter, msg string) {
	var body errorBody
	body.Error.Code = "FORBIDDEN"
	body.Error.Message = msg
	writeJSON(w, http.StatusForbidden, body)
}

// requestLogger emits one structured line per request.
func requestLogger(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start)).
				Msg("request")
		})
	}
}

// recoverer converts handler panics into 500s instead of dropping the
// connection.
func recoverer(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error().Any("panic", rec).Str("path", r.URL.Path).Msg("handler panic")
					var body errorBody
					body.Error.Code = "INTERNAL"
					body.Error.Message = "internal error"
					writeJSON(w, http.StatusInternalServerError, body)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so streaming responses (the
// SSE events tail) work through the request logger.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
