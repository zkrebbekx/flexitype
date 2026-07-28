package http

import (
	"context"
	"net/http"
	"strings"

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
			if strings.HasPrefix(r.URL.Path, "/api/") {
				notFoundJSON(w, r)
				return
			}
			spa(w, r)
		}
	}
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)

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
}

// orDefault returns v when it is a positive ceiling, and fallback otherwise,
// so a zero-valued config keeps the documented default.
func orDefault(v, fallback int64) int64 {
	if v > 0 {
		return v
	}
	return fallback
}
