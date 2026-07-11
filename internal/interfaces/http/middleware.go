package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
	"github.com/zkrebbekx/flexitype/pkg/ratelimit"
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

type scopesKey struct{}

// scopesFromContext returns the authenticated account's scopes. In
// development mode (auth disabled) it returns admin so every surface is
// reachable.
func scopesFromContext(ctx context.Context) []serviceaccount.Scope {
	if s, ok := ctx.Value(scopesKey{}).([]serviceaccount.Scope); ok {
		return s
	}
	return []serviceaccount.Scope{serviceaccount.ScopeAdmin}
}

// hasScope reports whether the request's account holds a scope (admin
// implies all).
func hasScope(ctx context.Context, want serviceaccount.Scope) bool {
	for _, s := range scopesFromContext(ctx) {
		if s == want || s == serviceaccount.ScopeAdmin {
			return true
		}
	}
	return false
}

// requireAdmin gates the provisioning endpoints on the admin scope.
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !hasScope(r.Context(), serviceaccount.ScopeAdmin) {
		writeForbidden(w, "missing scope admin")
		return false
	}
	return true
}

// authenticate resolves the bearer token to a service account, stamping
// actor, tenant and scopes onto the request context. A nil authenticator
// disables authentication (development mode) and runs as the system actor
// on the default tenant with admin scope.
func authenticate(auth serviceaccount.Authenticator, log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth == nil {
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
			account, err := auth.Authenticate(token)
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
			ctx = context.WithValue(ctx, scopesKey{}, account.Scopes)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// rateLimit throttles requests per service account using a token bucket.
// It runs after authenticate, so the account and tenant are on the context;
// unauthenticated (development) requests key on a single shared bucket. A
// rejected request gets 429 with a Retry-After header and is counted per
// tenant. A nil limiter disables throttling.
func rateLimit(limiter *ratelimit.Limiter, m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}
			tenant := uow.TenantFromContext(r.Context()).String()
			m.CountTenantRequest(tenant)

			// Key on the account when authenticated; otherwise per tenant.
			key := uow.ActorFromContext(r.Context()).ID
			if key == "" {
				key = tenant
			}
			if ok, retry := limiter.Allow(key); !ok {
				m.CountRateLimitReject(tenant)
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
				var body errorBody
				body.Error.Code = "RATE_LIMITED"
				body.Error.Message = "rate limit exceeded; retry later"
				writeJSON(w, http.StatusTooManyRequests, body)
				return
			}
			next.ServeHTTP(w, r)
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
