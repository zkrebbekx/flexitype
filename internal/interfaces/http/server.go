package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// ServerConfig carries the API server's dependencies.
type ServerConfig struct {
	Factory  application.Factory
	Logger   *logger.Logger
	Health   *health.Service
	Accounts *serviceaccount.Store // nil disables auth (development)
}

// NewHandler builds the service's HTTP handler: versioned API plus
// operational endpoints, instrumented with OpenTelemetry.
func NewHandler(cfg ServerConfig) http.Handler {
	s := &server{factory: cfg.Factory, log: cfg.Logger}

	r := chi.NewRouter()
	r.Use(recoverer(cfg.Logger))
	r.Use(requestLogger(cfg.Logger))

	r.Get("/healthz", cfg.Health.LiveHandler())
	r.Get("/readyz", cfg.Health.ReadyHandler())

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(authenticate(cfg.Accounts, cfg.Logger))

		api.Route("/type-definitions", func(r chi.Router) {
			r.Get("/", s.listTypeDefinitions)
			r.Post("/", s.createTypeDefinition)
			r.Get("/{id}", s.getTypeDefinition)
			r.Patch("/{id}", s.updateTypeDefinition)
			r.Post("/{id}/archive", s.archiveTypeDefinition)
			r.Post("/{id}/restore", s.restoreTypeDefinition)
			r.Get("/{id}/attributes", s.listAttributesByTypeDefinition)
		})

		api.Route("/attributes", func(r chi.Router) {
			r.Get("/", s.listAttributes)
			r.Post("/", s.createAttribute)
			r.Get("/{id}", s.getAttribute)
			r.Patch("/{id}", s.updateAttribute)
			r.Post("/{id}/archive", s.archiveAttribute)
			r.Post("/{id}/restore", s.restoreAttribute)
		})

		api.Route("/values", func(r chi.Router) {
			r.Get("/", s.listValues)
			r.Post("/", s.setValue)
			r.Get("/{id}", s.getValue)
			r.Delete("/{id}", s.removeValue)
		})

		api.Route("/entities/{typeDefinitionID}/{entityID}", func(r chi.Router) {
			r.Get("/values", s.listEntityValues)
			r.Get("/attributes/{attributeID}/effective-schema", s.effectiveSchema)
		})

		api.Route("/dependencies", func(r chi.Router) {
			r.Get("/", s.listDependencies)
			r.Post("/", s.createDependency)
			r.Get("/{id}", s.getDependency)
			r.Patch("/{id}", s.updateDependency)
			r.Delete("/{id}", s.archiveDependency)
		})

		api.Get("/activity", s.listActivity)
	})

	return otelhttp.NewHandler(r, "flexitype.http")
}

// server holds per-handler dependencies.
type server struct {
	factory application.Factory
	log     *logger.Logger
}
