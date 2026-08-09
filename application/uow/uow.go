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
	// checks are the invariants to verify once the usecase has finished but
	// before anything commits.
	checks []func(ctx context.Context) error
	// checkKeys makes registration idempotent, so a usecase that touches a
	// thousand values registers its end-of-transaction check once.
	checkKeys map[string]bool
	// stash is per-unit-of-work scratch space for the usecases; see Stash.
	stash map[string]any
}

// CollectEvents queues domain events for post-commit dispatch.
func (c *Collector) CollectEvents(evts ...events.Event) {
	c.events = append(c.events, evts...)
}

// RecordChange queues an audit change for the pre-commit activity log.
func (c *Collector) RecordChange(ch activity.Change) {
	c.changes = append(c.changes, ch)
}

// Stash returns the value stored under key, creating it with create on first
// use. It gives a usecase somewhere to accumulate state for the life of one
// unit of work — such as the entities an end-of-transaction check must look at
// once every write has landed.
func (c *Collector) Stash(key string, create func() any) any {
	if c.stash == nil {
		c.stash = make(map[string]any, 1)
	}
	if v, ok := c.stash[key]; ok {
		return v
	}
	v := create()
	c.stash[key] = v
	return v
}

// RunChecks runs the registered end-of-transaction invariants and reports the
// first failure. It runs them once: a second call is a no-op, so a usecase
// that needs the verdict before Execute would reach it — a dry run, which
// rolls back deliberately — can ask for it without the checks running twice.
func (c *Collector) RunChecks(ctx context.Context) error {
	checks := c.checks
	c.checks = nil
	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}
	return nil
}

// CheckBeforeCommitOnce registers an invariant to run after the usecase
// succeeds and before the transaction commits. A second registration under
// the same key is ignored.
//
// It exists for rules that are properties of the FINISHED state rather than
// of one write. A dependency that demands a value cannot be checked as each
// value lands: an import row writes its cells one at a time, so the cell that
// makes the rule apply usually arrives before the cell that satisfies it, and
// checking early would refuse a row whose final state is complete. Running at
// the end makes the outcome independent of the order the writes happened in.
//
// A failing check returns its error from Execute, so the transaction rolls
// back exactly as a failing usecase does.
func (c *Collector) CheckBeforeCommitOnce(key string, fn func(ctx context.Context) error) {
	if c.checkKeys == nil {
		c.checkKeys = make(map[string]bool, 1)
	}
	if c.checkKeys[key] {
		return
	}
	c.checkKeys[key] = true
	c.checks = append(c.checks, fn)
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
	tx          db.Transactor
	dispatcher  *events.Dispatcher
	projections *events.Dispatcher // internal projections, maintained sync post-commit in BOTH modes
	log         activity.Log
	sink        EnvelopeSink // non-nil switches EXTERNAL post-commit dispatch to outbox writes
	afterWrite  func()       // relay nudge, called post-commit in outbox mode
	onRollback  func(ctx context.Context, err error)
	onDispatch  func(ctx context.Context, err error) // observes soft-failed sync dispatch + projection maintenance
	now         func() time.Time
}

// Option customises a unit of work.
type Option func(*unitOfWork)

// WithRollbackObserver installs a hook invoked whenever a unit of work
// rolls back — the factory's standard rollback commit handler.
func WithRollbackObserver(fn func(ctx context.Context, err error)) Option {
	return func(u *unitOfWork) { u.onRollback = fn }
}

// WithDispatchObserver installs a hook invoked when a post-commit maintenance
// step fails. It covers two soft-failure paths, both after the change is
// already durable: a synchronous external subscriber error (default,
// non-outbox mode) and an internal-projection maintenance error (computed
// attributes / search index / GraphQL cache, in either mode). Neither must
// turn a committed write into a request failure — a retry would double-apply
// side effects — so the error is surfaced here rather than swallowed silently
// or promoted to a request error.
func WithDispatchObserver(fn func(ctx context.Context, err error)) Option {
	return func(u *unitOfWork) { u.onDispatch = fn }
}

// WithProjections installs the internal-projection dispatcher. Unlike the
// external dispatcher (which delivers to consumer hooks and, under WithOutbox,
// is fed by the relay), this one is ALWAYS dispatched synchronously in the
// originating unit of work's post-commit — in both delivery modes — so the
// writing request observes its own computed attributes and search updates
// (read-your-writes) regardless of the outbox setting. See Execute's
// post-commit hook and issue #211.
func WithProjections(d *events.Dispatcher) Option {
	return func(u *unitOfWork) { u.projections = d }
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

	// Post-commit: maintain internal projections, then fan events out to
	// external subscribers — both only once the change is durable.
	tx.OnPostCommit(func(ctx context.Context) error {
		if len(collector.events) == 0 {
			return nil
		}
		meta := events.Metadata{TenantID: tenant.String(), Actor: actor.String()}

		// Internal projections (computed-attribute materialization, the search
		// index, the GraphQL schema-cache hint) are maintained here — in the
		// originating request, synchronously, in BOTH delivery modes. This is the
		// crux of #211: they ride their OWN dispatcher, never the external one the
		// relay drains, so their consistency does not depend on WithOutbox. Because
		// the work happens in the writing request's post-commit (never later on the
		// relay), the request always observes its own computed values and search
		// updates — read-your-writes, outbox on or off. A maintenance failure is
		// surfaced to the observer rather than silently swallowed into permanent
		// staleness; since the change is already durable it is reported, not turned
		// into a request error (a retry would double-apply the committed write).
		// Projections run under system access, not the writing principal's
		// field ACL. They read an entity's FULL value set to derive state every
		// reader shares — a computed value, a search document, a summary row.
		// Under the writer's ACL, a value the writer may not read is redacted
		// from those reads, so a least-privileged writer silently corrupted
		// derived state for everyone (the write side was already exempt via
		// Internal). The background sweeps stamp the same access; this makes
		// the per-write path consistent with them. Tenant, actor and deadline
		// are unchanged — only the field-permission value is replaced.
		if u.projections != nil {
			pctx := WithSystemAccess(ctx)
			if perr := u.projections.Dispatch(pctx, meta, collector.events...); perr != nil {
				if u.onDispatch != nil {
					u.onDispatch(pctx, perr)
				}
			}
		}

		// External delivery keeps its existing semantics. In outbox mode the
		// envelopes were queued pre-commit — just wake the relay for
		// at-least-once delivery.
		if u.sink != nil {
			if u.afterWrite != nil {
				u.afterWrite()
			}
			return nil
		}
		if derr := u.dispatcher.Dispatch(ctx, meta, collector.events...); derr != nil {
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

	if err = fn(tx, collector); err == nil {
		// End-of-transaction invariants: properties of the finished state,
		// which cannot be judged while the writes are still arriving. They run
		// inside the transaction, so a failure rolls the whole unit back.
		err = collector.RunChecks(ctx)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
