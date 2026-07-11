package application

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Features toggles optional capabilities per deployment. Zero value =
// everything enabled.
type Features struct {
	// DisableSearch turns the FQL query surface off.
	DisableSearch bool
	// DisableActivity turns the audit log off entirely: no pre-commit
	// writes, no read API.
	DisableActivity bool
}

// FactoryConfig carries the factory's composition-time dependencies.
type FactoryConfig struct {
	// Transactor is the pool-level transactor usecases begin transactions
	// on.
	Transactor db.Transactor

	// NewRepositories builds one request-scoped repository set (fresh
	// dataloader caches per request).
	NewRepositories func() Repositories

	// Dispatcher fans domain events out to registered client hooks
	// (pub/sub, webhooks, funcs) after commit.
	Dispatcher *events.Dispatcher

	// ActivityLog persists audit entries inside the business transaction
	// (pre-commit) and serves the audit read API.
	ActivityLog activity.Log

	// OnRollback observes rolled-back units of work (metrics/logging).
	// Optional.
	OnRollback func(ctx context.Context, err error)

	// Now overrides the clock. Optional; defaults to time.Now.
	Now func() time.Time

	// Features toggles optional capabilities.
	Features Features
}

// factory is the common usecase factory: every request gets fresh
// repositories and a unit of work with the standard commit handlers
// registered (pre-commit activity log, post-commit event dispatch,
// rollback observer).
type factory struct {
	cfg FactoryConfig
}

// NewFactory validates the config and builds the usecase factory.
func NewFactory(cfg FactoryConfig) Factory {
	if cfg.Transactor == nil {
		panic("application: FactoryConfig.Transactor is required")
	}
	if cfg.NewRepositories == nil {
		panic("application: FactoryConfig.NewRepositories is required")
	}
	if cfg.Dispatcher == nil {
		cfg.Dispatcher = events.NewDispatcher()
	}
	if cfg.ActivityLog == nil && !cfg.Features.DisableActivity {
		panic("application: FactoryConfig.ActivityLog is required unless activity is disabled")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &factory{cfg: cfg}
}

// New builds the request-scoped interactor set.
func (f *factory) New(context.Context) *Interactors {
	repos := f.cfg.NewRepositories()

	opts := []uow.Option{uow.WithNow(f.cfg.Now)}
	if f.cfg.OnRollback != nil {
		opts = append(opts, uow.WithRollbackObserver(f.cfg.OnRollback))
	}
	activityLog := f.cfg.ActivityLog
	if f.cfg.Features.DisableActivity {
		activityLog = nil // the unit of work skips audit writes entirely
	}
	unit := uow.New(f.cfg.Transactor, f.cfg.Dispatcher, activityLog, opts...)

	return &Interactors{
		typeDefs:      apptypedef.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes),
		attrs:         appattribute.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes),
		values:        appvalue.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes, repos.Values, repos.Dependencies),
		deps:          appdependency.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes, repos.Values, repos.Dependencies),
		relationships: apprelationship.NewInteractor(unit, repos.TypeDefinitions, repos.RelationshipDefinitions, repos.Relationships),
		query:         appquery.NewInteractor(repos.TypeDefinitions, repos.Attributes, repos.RelationshipDefinitions, repos.Query),
		activity:      &ActivityInteractor{log: activityLog},
		features:      f.cfg.Features,
	}
}
