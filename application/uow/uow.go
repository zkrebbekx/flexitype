package uow

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// EnvelopeSink persists envelopes inside the business transaction — the
// outbox's write side.
type EnvelopeSink interface {
	Write(ctx context.Context, tx db.Tx, envs []events.Envelope) error
}

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
	sink       EnvelopeSink // non-nil switches post-commit dispatch to outbox writes
	afterWrite func()       // relay nudge, called post-commit in outbox mode
	onRollback func(ctx context.Context, err error)
	onDispatch func(ctx context.Context, err error) // observes soft-failed sync dispatch
	now        func() time.Time
}

// Option customises a unit of work.
type Option func(*unitOfWork)

// WithRollbackObserver installs a hook invoked whenever a unit of work
// rolls back — the factory's standard rollback commit handler.
func WithRollbackObserver(fn func(ctx context.Context, err error)) Option {
	return func(u *unitOfWork) { u.onRollback = fn }
}

// WithDispatchObserver installs a hook invoked when synchronous post-commit
// event dispatch fails. In the default (non-outbox) mode the change is already
// durable when subscribers run, so a subscriber error must not turn a
// committed write into a request failure — it is observed here and swallowed.
func WithDispatchObserver(fn func(ctx context.Context, err error)) Option {
	return func(u *unitOfWork) { u.onDispatch = fn }
}

// UTCNow is the canonical clock for every usecase constructor: wall-clock in
// UTC, with the monotonic reading stripped. Timestamps produced here are
// stored in aggregates and compared across the app/DB boundary, so they must
// be UTC — a raw time.Now() carries the local zone (and a monotonic reading),
// which reads as maybe-a-bug and can flip ordering-sensitive tests in non-UTC
// timezones. Duration/TTL clocks (rate limiting, auth cache, backoff) keep
// time.Now() instead, since they need the monotonic reading.
func UTCNow() time.Time { return time.Now().UTC() }

// WithNow overrides the clock (tests).
func WithNow(now func() time.Time) Option {
	return func(u *unitOfWork) { u.now = now }
}

// WithOutbox switches event delivery to at-least-once: envelopes are
// written through sink inside the business transaction (pre-commit), and
// afterWrite (the relay nudge) fires post-commit instead of direct
// dispatch.
func WithOutbox(sink EnvelopeSink, afterWrite func()) Option {
	return func(u *unitOfWork) {
		u.sink = sink
		u.afterWrite = afterWrite
	}
}

// New builds a UnitOfWork around a pool-level transactor with the standard
// commit handlers.
func New(tx db.Transactor, dispatcher *events.Dispatcher, log activity.Log, opts ...Option) UnitOfWork {
	u := &unitOfWork{
		tx:         tx,
		dispatcher: dispatcher,
		log:        log,
		now:        UTCNow,
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

	// Pre-commit (outbox mode): persist the event envelopes in the same
	// transaction, so a committed change implies its events are queued.
	if u.sink != nil {
		tx.OnPreCommit(func(ctx context.Context) error {
			if len(collector.events) == 0 {
				return nil
			}
			meta := events.Metadata{TenantID: tenant.String(), Actor: actor.String()}
			envs := make([]events.Envelope, 0, len(collector.events))
			for _, evt := range collector.events {
				env, err := events.NewEnvelope(evt, meta, u.now())
				if err != nil {
					return err
				}
				envs = append(envs, env)
			}
			return u.sink.Write(ctx, tx, envs)
		})
	}

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
	// durable. In outbox mode the envelopes are already queued — just wake
	// the relay.
	tx.OnPostCommit(func(ctx context.Context) error {
		if len(collector.events) == 0 {
			return nil
		}
		if u.sink != nil {
			if u.afterWrite != nil {
				u.afterWrite()
			}
			return nil
		}
		if derr := u.dispatcher.Dispatch(ctx, events.Metadata{
			TenantID: tenant.String(),
			Actor:    actor.String(),
		}, collector.events...); derr != nil {
			// The change has committed; a failing subscriber must not surface
			// as a request error (that would prompt a retry and double-apply
			// side effects). Observe it and move on — at-least-once delivery is
			// the outbox's job, not the synchronous path's.
			if u.onDispatch != nil {
				u.onDispatch(ctx, derr)
			}
		}
		return nil
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
