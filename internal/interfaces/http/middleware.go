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
	"github.com/zkrebbekx/flexitype/pkg/ulid"
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

// consoleThemeScriptHash is the SHA-256 of the inline pre-paint theme <script>
// in web/index.html (which Vite copies verbatim into the served index.html).
// Pinning its hash in script-src lets the console keep that one inline script
// WITHOUT 'unsafe-inline', so an injected inline script is blocked. A test
// recomputes this from web.IndexHTML and fails if it drifts.
const consoleThemeScriptHash = "'sha256-s3Q8xUUXR+swI8/f9WrZoqSTuYLHw84CXgtIw0hXP2Q='"

// securityCSP is the Content-Security-Policy applied to every response. It
// keeps the console's script, data-fetching and framing pinned to its own
// origin. script-src allows only 'self', the pinned hash of the inline theme
// script, and 'wasm-unsafe-eval' (for the in-browser playground build) — no
// 'unsafe-inline', so injected inline scripts are refused. style-src keeps
// 'unsafe-inline' because Vue binds inline style attributes and index.html
// ships an inline boot <style> (styles are a far weaker injection vector).
// Handlers that serve raw uploads (media) override this with a stricter,
// content-free policy.
const securityCSP = "default-src 'self'; " +
	"script-src 'self' " + consoleThemeScriptHash + " 'wasm-unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders sets defensive response headers on every response — console
// and API alike: nosniff stops content-type guessing, DENY blocks framing
// (clickjacking), no-referrer keeps URLs (which carry media keys and ids) out
// of the Referer header, and the CSP is the origin lockdown above.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", securityCSP)
		next.ServeHTTP(w, r)
	})
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

// requireAdmin gates the provisioning endpoints on the admin scope. Note that
// `admin` is a platform-operator (global) privilege, not a tenant-admin one:
// the provisioning control plane takes the target tenant from the request, so
// an admin-scoped caller can provision in, and enumerate, any tenant. This is
// intentional and documented (docs/configuration.md) — issue only to trusted
// operators, never to per-tenant admins.
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
// accessFor derives the principal's field-level permissions. An admin (or
// an account with no field restrictions) gets full access; otherwise the
// declared per-attribute levels apply.
func accessFor(account serviceaccount.Account) uow.Access {
	if account.HasScope(serviceaccount.ScopeAdmin) || len(account.FieldPermissions) == 0 {
		return uow.Access{Admin: true}
	}
	attr := make(map[string]uow.Perm, len(account.FieldPermissions))
	for name, level := range account.FieldPermissions {
		attr[name] = uow.Perm(level)
	}
	return uow.Access{Attr: attr}
}

func authenticate(auth serviceaccount.Authenticator, log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth == nil {
				// Development mode (auth disabled). Stamp the admin principal
				// EXPLICITLY — scopes and field access — so no request relies
				// on the implicit context defaults; the trust boundary is set
				// here in one place rather than fanned out to every reader.
				if ri := reqInfoFromContext(r.Context()); ri != nil {
					ri.actor = "dev"
				}
				ctx := uow.WithActor(r.Context(), uow.Actor{Name: "dev", Kind: uow.ActorSystem})
				ctx = context.WithValue(ctx, scopesKey{}, []serviceaccount.Scope{serviceaccount.ScopeAdmin})
				ctx = uow.WithAccess(ctx, uow.Access{Admin: true})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				writeUnauthorized(w, "missing bearer token")
				return
			}
			// Prefer the context-aware form so the request's cancellation,
			// deadline and trace reach the credential lookup (a per-request SQL
			// query for the database-backed store); fall back for any
			// Authenticator that predates it.
			var account serviceaccount.Account
			var err error
			if ctxAuth, ok := auth.(serviceaccount.AuthenticatorCtx); ok {
				account, err = ctxAuth.AuthenticateCtx(r.Context(), token)
			} else {
				account, err = auth.Authenticate(token)
			}
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

			if ri := reqInfoFromContext(r.Context()); ri != nil {
				ri.actor = account.Name
				ri.tenant = account.Tenant().String()
			}
			ctx := uow.WithActor(r.Context(), uow.Actor{
				ID:   account.ID,
				Name: account.Name,
				Kind: uow.ActorServiceAccount,
			})
			ctx = uow.WithTenant(ctx, account.Tenant())
			ctx = context.WithValue(ctx, scopesKey{}, account.Scopes)
			ctx = uow.WithAccess(ctx, accessFor(account))
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

// reqInfo carries per-request correlation fields. The logger stamps a pointer
// on the context before authentication runs; authenticate fills in the tenant
// and actor so the single request log line can carry them (the authenticated
// context only flows downstream, not back up to the outer logger).
type reqInfo struct {
	id     string
	tenant string
	actor  string
}

type reqInfoKey struct{}

func reqInfoFromContext(ctx context.Context) *reqInfo {
	if ri, ok := ctx.Value(reqInfoKey{}).(*reqInfo); ok {
		return ri
	}
	return nil
}

// requestLogger emits one structured line per request, with a generated
// request id (also returned in the X-Request-Id header) and, once
// authenticated, the tenant and actor.
func requestLogger(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ri := &reqInfo{id: ulid.New().String()}
			w.Header().Set("X-Request-Id", ri.id)
			ctx := context.WithValue(r.Context(), reqInfoKey{}, ri)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))
			ev := log.Info().
				Str("request_id", ri.id).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start))
			if ri.tenant != "" {
				ev = ev.Str("tenant", ri.tenant)
			}
			if ri.actor != "" {
				ev = ev.Str("actor", ri.actor)
			}
			ev.Msg("request")
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
