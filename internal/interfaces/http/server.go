package http

import (
	"context"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/gql"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/mediaurl"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
	"github.com/zkrebbekx/flexitype/pkg/ratelimit"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// ServerConfig carries the API server's dependencies.
type ServerConfig struct {
	Factory  application.Factory
	Logger   *logger.Logger
	Health   *health.Service
	Accounts serviceaccount.Authenticator // nil disables auth (development)
	// Admin, when set, enables the tenant/service-account provisioning API.
	Admin *admin.Interactor
	// Reindex rebuilds the search projection; nil when the index is off.
	Reindex func(ctx context.Context, tenant valueobjects.TenantID) (int, error)
	// RecomputeComputed rebuilds every computed attribute for a tenant — the
	// recovery path for internal-projection staleness after a crash.
	RecomputeComputed func(ctx context.Context, tenant valueobjects.TenantID) (int, error)
	// Metrics, when set, records HTTP SLIs and serves /metrics.
	Metrics *metrics.Metrics
	// RateLimiter, when set, throttles API requests per service account.
	RateLimiter *ratelimit.Limiter
	// TenantRateLimiter, when set, caps a tenant's aggregate request rate
	// across all of its service accounts. Without it, a tenant multiplies its
	// effective rate by the number of accounts it creates.
	TenantRateLimiter *ratelimit.Limiter
	// AuthRateLimiter throttles by client address BEFORE authentication, so a
	// failed credential is throttled at all. Nil disables it.
	AuthRateLimiter *ratelimit.Limiter
	// BlobStore serves media downloads; nil when media is disabled.
	BlobStore blob.Store
	// GraphQL serves the read-only GraphQL API; nil disables the endpoint.
	GraphQL *gql.Engine
	// DisableConsole omits the admin-console SPA, for an API-only deployment.
	// An unmatched path then returns a JSON 404 like any other API error.
	DisableConsole bool
	// MaxImportBytes caps a CSV import upload. 0 uses DefaultMaxImportBytes.
	// The ceiling is reported by GET /features so a client can chunk a bulk
	// load against the real limit rather than guessing at it.
	MaxImportBytes int64
	// MaxMediaBytes caps a media upload. 0 uses DefaultMaxMediaBytes.
	MaxMediaBytes int64
	// MediaSigner, when set, turns on signed, expiring media links: an
	// authenticated caller mints one, and anybody holding it can fetch that
	// one object until it expires. Without it the signing endpoint reports
	// the capability as disabled.
	MediaSigner *mediaurl.Signer
	// TimeZone is the deployment's calendar zone for `today` and `now`. It is
	// stamped on the REQUEST context, because that is the context every
	// handler passes to the interactor methods. Stamping it where the
	// interactor set is built did nothing: the set carries no context, and
	// each method takes one from its own caller — so FLEXITYPE_TIMEZONE never
	// reached rule evaluation and every date rule resolved in UTC.
	TimeZone *time.Location
	// Clock pins the instant `today` and `now` resolve against, for tests
	// and simulations. It is stamped on the request context exactly as
	// TimeZone is, and for the same reason. Nil uses the wall clock.
	Clock func() time.Time
}

// Upload ceilings. They were compile-time constants, absent from the
// configuration document, the OpenAPI spec and the client — so a team sizing
// a bulk CSV load had no way to discover the limit, and hit it part-way
// through a migration. Both are now configurable and reported by /features.
const (
	DefaultMaxImportBytes int64 = 16 << 20 // 16 MiB
	DefaultMaxMediaBytes  int64 = 32 << 20 // 32 MiB
)

// NewHandler builds the service's HTTP handler: versioned API plus
// operational endpoints, instrumented with OpenTelemetry.
func NewHandler(cfg ServerConfig) http.Handler {
	return otelhttp.NewHandler(buildRouter(cfg), "flexitype.http")
}

// buildRouter constructs the raw chi router (before OpenTelemetry wrapping) so
// tests can walk the registered routes.
func buildRouter(cfg ServerConfig) *chi.Mux {
	s := &server{
		factory: cfg.Factory, log: cfg.Logger, reindex: cfg.Reindex,
		recompute: cfg.RecomputeComputed, admin: cfg.Admin, blobs: cfg.BlobStore,
		graphql:        cfg.GraphQL,
		maxImportBytes: orDefault(cfg.MaxImportBytes, DefaultMaxImportBytes),
		maxMediaBytes:  orDefault(cfg.MaxMediaBytes, DefaultMaxMediaBytes),
		mediaSigner:    cfg.MediaSigner,
		clock:          cfg.Clock,
	}

	r := chi.NewRouter()
	r.Use(recoverer(cfg.Logger))
	// Defensive response headers on everything, including the SPA shell and
	// the NotFound-served client routes.
	r.Use(securityHeaders)
	if cfg.Metrics != nil {
		// Before the request logger so the route pattern is resolved and
		// /metrics itself is measured.
		r.Use(cfg.Metrics.Middleware)
	}
	r.Use(requestLogger(cfg.Logger))

	r.Get("/healthz", cfg.Health.LiveHandler())
	r.Get("/readyz", cfg.Health.ReadyHandler())
	if cfg.Metrics != nil {
		r.Handle("/metrics", cfg.Metrics.Handler())
	}

	// Redeeming a signed media link is deliberately OUTSIDE the API's
	// authentication: the signature IS the credential, and the whole reason
	// the link exists is that a public surface holds no tenant token. It
	// carries its own tenant, expiry and object key, all covered by the
	// signature.
	r.Get("/media/signed/{token}", s.downloadSignedMedia)

	// The OpenAPI document is public (before auth) so client generators
	// and mock servers can fetch the contract without credentials.
	r.Get("/api/v1/openapi.json", s.openAPIJSON)
	r.Get("/api/v1/openapi.yaml", s.openAPIYAML)

	r.Route("/api/v1", func(api chi.Router) {
		// Before auth: a failed authentication must be throttled too, and it
		// cannot be keyed on a principal that does not exist yet.
		api.Use(preAuthRateLimit(cfg.AuthRateLimiter, cfg.Metrics))
		api.Use(authenticate(cfg.Accounts, cfg.Logger))
		// Throttle after auth so the limiter keys on the resolved account
		// and counts usage per tenant.
		api.Use(rateLimit(cfg.RateLimiter, cfg.TenantRateLimiter, cfg.Metrics))
		// The zone goes on before the interactors, so a handler's ctx carries
		// it into every rule evaluation.
		api.Use(withTimeZone(cfg.TimeZone))
		api.Use(withClock(cfg.Clock))
		// Interactors after auth: the set is built with the request's actor
		// and tenant already on the context.
		api.Use(withInteractors(cfg.Factory))

		api.Route("/type-definitions", func(r chi.Router) {
			r.Get("/", s.listTypeDefinitions)
			r.Post("/", s.createTypeDefinition)
			r.Get("/{id}", s.getTypeDefinition)
			r.Patch("/{id}", s.updateTypeDefinition)
			r.Post("/{id}/archive", s.archiveTypeDefinition)
			r.Post("/{id}/restore", s.restoreTypeDefinition)
			r.Get("/{id}/attributes", s.listAttributesByTypeDefinition)
			r.Get("/{id}/effective-attributes", s.effectiveAttributes)
			r.Get("/{id}/children", s.typeChildren)
			r.Get("/{id}/completeness", s.typeCompleteness)
			r.Get("/{id}/match-rules", s.listMatchRules)
			r.Post("/{id}/match-rules", s.createMatchRule)
			r.Post("/{id}/clone", s.cloneType)
		})

		api.Route("/match-rules", func(r chi.Router) {
			r.Delete("/{ruleID}", s.deleteMatchRule)
			r.Get("/{ruleID}/scan", s.scanMatchRule)
			r.Post("/{ruleID}/dismiss", s.dismissMatch)
		})

		api.Route("/attributes", func(r chi.Router) {
			r.Get("/", s.listAttributes)
			r.Post("/", s.createAttribute)
			r.Get("/{id}", s.getAttribute)
			r.Patch("/{id}", s.updateAttribute)
			r.Post("/{id}/archive", s.archiveAttribute)
			r.Post("/{id}/restore", s.restoreAttribute)
			r.Post("/{id}/validate-value", s.validateAttributeValue)
		})

		api.Route("/values", func(r chi.Router) {
			r.Get("/", s.listValues)
			r.Post("/", s.setValue)
			r.Post("/batch", s.setValuesBatch)
			r.Get("/{id}", s.getValue)
			r.Delete("/{id}", s.removeValue)
		})

		api.Route("/schema", func(r chi.Router) {
			r.Get("/export", s.exportSchema)
			r.Post("/import", s.importSchema)
			r.Get("/templates", s.listTemplates)
			r.Get("/templates/{name}", s.getTemplate)
			r.Post("/templates/{name}/apply", s.applyTemplate)
		})

		api.Route("/saved-views", func(r chi.Router) {
			r.Get("/", s.listSavedViews)
			r.Post("/", s.createSavedView)
			r.Get("/{id}", s.getSavedView)
			r.Patch("/{id}", s.updateSavedView)
			r.Delete("/{id}", s.deleteSavedView)
		})

		api.Get("/entities/{typeDefinitionID}", s.listEntitiesOfType)
		api.Get("/entities/{typeDefinitionID}/grid", s.gridEntities)
		api.Get("/entities/{typeDefinitionID}/facets", s.entityFacets)
		api.Post("/entities/{typeDefinitionID}/import", s.importEntities)
		api.Get("/entities/{typeDefinitionID}/export", s.exportEntities)
		api.Route("/entities/{typeDefinitionID}/{entityID}", func(r chi.Router) {
			r.Get("/values", s.listEntityValues)
			r.Get("/relationships", s.listEntityRelationships)
			r.Get("/attributes/{attributeID}/effective-schema", s.effectiveSchema)
			r.Get("/relationship-requirements", s.relationshipRequirements)
			r.Get("/completeness", s.entityCompleteness)
			r.Post("/apply-defaults", s.applyEntityDefaults)
			r.Get("/revisions", s.listRevisions)
			r.Post("/revisions", s.createRevision)
			r.Get("/as-of", s.entityAsOf)
			r.Post("/attributes/{attributeID}/media", s.uploadMedia)
			// Cascade: archive the entity's values and unlink its relationships.
			r.Delete("/", s.removeEntity)
			// Erasure (admin-scoped): permanently hard-delete every trace of
			// the entity. Irreversible, distinct from the soft-delete DELETE.
			r.Post("/purge", s.purgeEntity)
		})

		api.Route("/revisions", func(r chi.Router) {
			r.Get("/{id}", s.getRevision)
			r.Get("/{id}/diff", s.diffRevisions)
			r.Post("/{id}/restore", s.restoreRevision)
		})

		api.Get("/media/{objectKey}", s.downloadMedia)
		// Mint a signed, expiring link to one object. Authenticated, and
		// gated on the same ownership and field-permission check the
		// authenticated download makes.
		// A GET: minting a link CHANGES NOTHING. It hands back a capability to
		// read an object the caller can already read, so it is a read — and
		// making it a POST locked it away from exactly the credential that
		// needs it most, a read-only cross-tenant reader.
		api.Get("/media/{objectKey}/signed-url", s.signMediaURL)

		api.Route("/unit-families", func(r chi.Router) {
			r.Get("/", s.listUnitFamilies)
			r.Post("/", s.createUnitFamily)
			r.Get("/{id}", s.getUnitFamily)
			r.Delete("/{id}", s.deleteUnitFamily)
		})

		api.Route("/changesets", func(r chi.Router) {
			r.Get("/", s.listChangeSets)
			r.Post("/", s.createChangeSet)
			r.Get("/{id}", s.getChangeSet)
			r.Post("/{id}/mutations", s.addChangeSetMutation)
			r.Post("/{id}/submit", s.submitChangeSet)
			r.Post("/{id}/approve", s.approveChangeSet)
			r.Post("/{id}/reject", s.rejectChangeSet)
			r.Post("/{id}/publish", s.publishChangeSet)
		})

		api.Route("/dependencies", func(r chi.Router) {
			r.Get("/", s.listDependencies)
			r.Post("/", s.createDependency)
			r.Get("/{id}", s.getDependency)
			r.Patch("/{id}", s.updateDependency)
			r.Delete("/{id}", s.archiveDependency)
		})

		api.Route("/relationship-definitions", func(r chi.Router) {
			r.Get("/", s.listRelationshipDefinitions)
			r.Post("/", s.createRelationshipDefinition)
			r.Get("/{id}", s.getRelationshipDefinition)
			r.Patch("/{id}", s.updateRelationshipDefinition)
			r.Post("/{id}/archive", s.archiveRelationshipDefinition)
			r.Post("/{id}/restore", s.restoreRelationshipDefinition)
			r.Get("/{id}/attribute-sets", s.relationshipAttributeSets)
		})

		api.Route("/relationships", func(r chi.Router) {
			r.Get("/", s.listRelationships)
			r.Post("/", s.createRelationship)
			r.Get("/{id}", s.getRelationship)
			r.Delete("/{id}", s.unlinkRelationship)
		})

		api.Get("/features", s.features)
		api.Get("/query", s.runQuery)
		api.Post("/query/validate", s.validateQuery)
		api.Get("/graphql", s.graphqlQuery)
		api.Post("/graphql", s.graphqlQuery)
		api.Post("/search/reindex", s.reindexSearch)
		api.Post("/computed/recompute", s.recomputeComputed)

		api.Get("/activity", s.listActivity)

		api.Route("/webhook-subscriptions", func(r chi.Router) {
			r.Get("/", s.listSubscriptions)
			r.Post("/", s.createSubscription)
			r.Get("/{id}", s.getSubscription)
			r.Patch("/{id}", s.updateSubscription)
			r.Delete("/{id}", s.deleteSubscription)
			r.Get("/{id}/deliveries", s.listSubscriptionDeliveries)
		})
		api.Post("/webhook-deliveries/{id}/redeliver", s.redeliverWebhook)
		api.Post("/webhook-deliveries/redeliver-dead", s.redeliverDeadWebhooks)

		// Parked-outbox recovery (admin-scoped): envelopes that exhausted
		// their retry budget wait here for an operator redrive. Without
		// these routes a parked envelope was a silently lost committed
		// change (issue #478).
		api.Get("/admin/outbox/parked", s.listParkedOutbox)
		api.Post("/admin/outbox/redrive", s.redriveOutbox)

		api.Get("/events", s.listEvents)
		api.Get("/events/stream", s.streamEvents)
		api.Route("/event-cursors/{consumer}", func(r chi.Router) {
			r.Get("/", s.getCursor)
			r.Put("/", s.commitCursor)
		})

		// Data erasure (admin-scoped): permanently hard-delete the calling
		// tenant's entity data (right-to-erasure). Irreversible.
		api.Post("/admin/purge", s.purgeTenant)

		// Provisioning control plane (admin-scoped; each handler checks).
		api.Route("/tenants", func(r chi.Router) {
			r.Get("/", s.listTenants)
			r.Post("/", s.createTenant)
			r.Patch("/{name}", s.setTenantActive)
		})
		api.Route("/service-accounts", func(r chi.Router) {
			r.Get("/", s.listServiceAccounts)
			r.Post("/", s.createServiceAccount)
			r.Post("/{id}/rotate", s.rotateServiceAccount)
			r.Delete("/{id}", s.revokeServiceAccount)
			r.Put("/{id}/roles", s.assignRoles)
			r.Get("/{id}/effective", s.effectiveAccount)
		})
		api.Route("/roles", func(r chi.Router) {
			r.Get("/", s.listRoles)
			r.Put("/", s.upsertRole)
			r.Delete("/{name}", s.deleteRole)
		})
	})

	// An unknown path under the API namespace is a JSON 404, never the app
	// shell. Mounting the console as the global NotFound handler meant a typo'd
	// or removed endpoint answered 200 with text/html, so "endpoint absent" and
	// "endpoint succeeded" were the same response: version negotiation was
	// impossible, a client that checks the status before parsing reported an
	// HTML parse error instead of the real cause, and a misconfigured caller
	// never appeared on an error-rate dashboard.
	//
	// The decision is by prefix rather than on the /api/v1 subrouter, so an
	// unversioned or future-versioned path — /api/foo, /api/v2/... — is JSON
	// too, instead of falling through to the console.
	notFoundJSON := func(w http.ResponseWriter, r *http.Request) {
		writeError(w, cfg.Logger, domainerrors.NewNotFound("route", r.URL.Path))
	}
	notFound := notFoundJSON
	if !cfg.DisableConsole {
		// Everything that is not the API or an operational endpoint is the
		// admin console SPA.
		spa := spaHandler(cfg.Logger)
		notFound = func(w http.ResponseWriter, r *http.Request) {
			if isAPIPath(r.URL.Path) {
				notFoundJSON(w, r)
				return
			}
			spa(w, r)
		}
	}
	r.NotFound(notFound)
	// A KNOWN route reached with an unsupported method answers 405 with an
	// Allow header, not 404. Registering the not-found handler for both put
	// "this endpoint does not exist" and "this endpoint does not take that
	// verb" behind the same status — which is the endpoint-absent /
	// endpoint-present ambiguity the JSON-404 change set out to remove.
	allowed := allowedMethods(r)
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		if methods := allowed.forPath(req.URL.Path); methods != "" {
			w.Header().Set("Allow", methods)
		}
		var body errorBody
		body.Error.Code = "NOT_FOUND"
		body.Error.Message = "the method is not allowed for this route"
		writeJSON(w, http.StatusMethodNotAllowed, body)
	})

	return r
}

// server holds per-handler dependencies.
type server struct {
	factory   application.Factory
	log       *logger.Logger
	reindex   func(ctx context.Context, tenant valueobjects.TenantID) (int, error)
	recompute func(ctx context.Context, tenant valueobjects.TenantID) (int, error)
	admin     *admin.Interactor
	blobs     blob.Store
	graphql   *gql.Engine

	maxImportBytes int64
	maxMediaBytes  int64
	mediaSigner    *mediaurl.Signer
	clock          func() time.Time
}

// now reads the server's clock, which a test can pin.
func (s *server) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

// orDefault returns v when it is a positive ceiling, and fallback otherwise,
// so a zero-valued config keeps the documented default.
func orDefault(v, fallback int64) int64 {
	if v > 0 {
		return v
	}
	return fallback
}

// isAPIPath reports whether a path addresses the API namespace, normalising
// the forms a naive base-URL join produces.
//
// The test was a raw HasPrefix("/api/"), so `/api`, `//api/v1/...` and
// `/API/v1/...` all fell through to the console and answered 200 with the app
// shell — and `//api/...` is exactly what joining a base URL ending in "/"
// with a path beginning with "/" produces. A client that checks the status
// before parsing then reported an HTML parse error instead of the real cause.
func isAPIPath(p string) bool {
	clean := path.Clean("/" + strings.TrimLeft(p, "/"))
	lower := strings.ToLower(clean)
	return lower == "/api" || strings.HasPrefix(lower, "/api/")
}

// methodTable maps a route pattern to the methods registered for it.
//
// chi answers 405 with no Allow header and does not expose the allowed set,
// so the table is built once by walking the routes at construction time.
type methodTable map[string][]string

// allowedMethods walks a router and records the methods each pattern serves.
func allowedMethods(r chi.Routes) methodTable {
	out := methodTable{}
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/*")
		if route == "" {
			route = "/"
		}
		for _, have := range out[route] {
			if have == method {
				return nil
			}
		}
		out[route] = append(out[route], method)
		return nil
	})
	return out
}

// forPath returns the Allow header value for a request path, or "" when no
// pattern matches. A path segment matches a {param} placeholder.
func (t methodTable) forPath(p string) string {
	want := splitPath(p)
	for route, methods := range t {
		if !patternMatches(splitPath(route), want) {
			continue
		}
		sorted := append([]string(nil), methods...)
		sort.Strings(sorted)
		return strings.Join(sorted, ", ")
	}
	return ""
}

func splitPath(p string) []string {
	trimmed := strings.Trim(path.Clean("/"+strings.TrimLeft(p, "/")), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func patternMatches(pattern, actual []string) bool {
	if len(pattern) != len(actual) {
		return false
	}
	for i, seg := range pattern {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg != actual[i] {
			return false
		}
	}
	return true
}
