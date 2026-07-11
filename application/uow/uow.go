package uow

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Collector accumulates the domain events and audit changes a usecase
// produces inside one unit of work. The registered commit handlers drain it:
// pre-commit writes the activity log, post-commit dispatches the events.
type Collector struct {
	events  []events.Event
	changes []activity.Change
}

// CollectEvents queues domain events for post-commit dispatch.
func (c *Collector) CollectEvents(evts ...events.Event) {
	c.events = append(c.events, evts...)
}

// RecordChange queues an audit change for the pre-commit activity log.
func (c *Collector) RecordChange(ch activity.Change) {
	c.changes = append(c.changes, ch)
}

// UnitOfWork runs one business transaction. Execute wraps fn in
// begin/commit, wiring the standard commit handlers around it.
type UnitOfWork interface {
	Execute(ctx context.Context, fn func(tx db.Transactor, c *Collector) error) error
}

// unitOfWork is the common implementation every usecase shares; the
// application factory constructs one per request with the standard
// pre-commit (activity log), post-commit (event dispatch) and rollback
// handlers registered.
type unitOfWork struct {
	tx         db.Transactor
	dispatcher *events.Dispatcher
	log        activity.Log
	onRollback func(ctx context.Context, err error)
	now        func() time.Time
}

// Option customises a unit of work.
type Option func(*unitOfWork)

// WithRollbackObserver installs a hook invoked whenever a unit of work
// rolls back — the factory's standard rollback commit handler.
func WithRollbackObserver(fn func(ctx context.Context, err error)) Option {
	return func(u *unitOfWork) { u.onRollback = fn }
}

// WithNow overrides the clock (tests).
func WithNow(now func() time.Time) Option {
	return func(u *unitOfWork) { u.now = now }
}

// New builds a UnitOfWork around a pool-level transactor with the standard
// commit handlers.
func New(tx db.Transactor, dispatcher *events.Dispatcher, log activity.Log, opts ...Option) UnitOfWork {
	u := &unitOfWork{
		tx:         tx,
		dispatcher: dispatcher,
		log:        log,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

func (u *unitOfWork) Execute(ctx context.Context, fn func(tx db.Transactor, c *Collector) error) error {
	tx, err := u.tx.Begin(ctx)
	if err != nil {
		return err
	}

	collector := &Collector{}
	actor := ActorFromContext(ctx)
	tenant := TenantFromContext(ctx)

	// Pre-commit: persist the activity log in the same transaction, so an
	// audit row exists if and only if the change committed.
	tx.OnPreCommit(func(ctx context.Context) error {
		if u.log == nil || len(collector.changes) == 0 {
			return nil
		}
		entries := make([]activity.Entry, 0, len(collector.changes))
		for _, ch := range collector.changes {
			entry, err := activity.NewEntry(ch, tenant, actor.String(), u.now())
			if err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		return u.log.Write(ctx, tx, entries)
	})

	// Post-commit: fan events out to subscribers only once the change is
	// durable.
	tx.OnPostCommit(func(ctx context.Context) error {
		if len(collector.events) == 0 {
			return nil
		}
		return u.dispatcher.Dispatch(ctx, events.Metadata{
			TenantID: tenant.String(),
			Actor:    actor.String(),
		}, collector.events...)
	})

	// Rollback: surface abandoned work to the observability hook.
	tx.OnRollback(func(ctx context.Context) error {
		if u.onRollback != nil {
			u.onRollback(ctx, err)
		}
		return nil
	})

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(ctx)
			panic(r)
		}
	}()

	if err = fn(tx, collector); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
