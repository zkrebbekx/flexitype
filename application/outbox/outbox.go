// Package outbox implements at-least-once event delivery: the unit of
// work writes envelopes into an outbox table in the same transaction as
// the change, and a relay dispatches them to the registered hooks,
// retrying on failure. Without the outbox, a crash between commit and
// dispatch loses events; with it, every consumer (webhooks, pub/sub, the
// search indexer) sees each committed change at least once.
package outbox

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Store is the persistence port for the outbox.
type Store interface {
	// Write persists envelopes inside the caller's transaction — the unit
	// of work's pre-commit handler.
	Write(ctx context.Context, tx db.QueryExecer, envs []events.Envelope) error

	// Expand claims up to limit undispatched envelopes and hands them to
	// fn (in-process dispatch). For each success it assigns the next
	// feed_seq, fans out one webhook-delivery row per matching
	// subscription and marks the envelope dispatched — all in one
	// transaction with no network I/O. Failures stay pending for the next
	// pass. Implementations must serialize expansion (a single sequencer)
	// so feed_seq is assigned in commit order, and must not claim rows
	// another relay holds (SKIP LOCKED semantics).
	Expand(ctx context.Context, limit int, fn func(envs []events.Envelope) []Result) error
}

// Result records one dispatch attempt.
type Result struct {
	EnvelopeID string
	Err        error
}

// Relay drains the outbox: on a nudge (post-commit) or on the interval it
// fetches pending envelopes, dispatches them and records outcomes. Failed
// envelopes stay pending and retry on later passes.
type Relay struct {
	store       Store
	dispatcher  *events.Dispatcher
	interval    time.Duration
	batch       int
	nudge       chan struct{}
	onError     func(err error)
	afterExpand func()
}

// RelayOption customises a Relay.
type RelayOption func(*Relay)

// WithInterval sets the poll interval (default 2s).
func WithInterval(d time.Duration) RelayOption {
	return func(r *Relay) { r.interval = d }
}

// WithBatchSize sets how many envelopes one pass claims (default 100).
func WithBatchSize(n int) RelayOption {
	return func(r *Relay) { r.batch = n }
}

// WithErrorObserver receives relay-level failures (fetch errors — dispatch
// failures are recorded per envelope).
func WithErrorObserver(fn func(err error)) RelayOption {
	return func(r *Relay) { r.onError = fn }
}

// WithAfterExpand installs a hook invoked after a pass that expanded
// envelopes — the delivery worker's nudge, keeping webhook latency in
// milliseconds.
func WithAfterExpand(fn func()) RelayOption {
	return func(r *Relay) { r.afterExpand = fn }
}

// NewRelay builds a relay over the store and dispatcher.
func NewRelay(store Store, dispatcher *events.Dispatcher, opts ...RelayOption) *Relay {
	r := &Relay{
		store:      store,
		dispatcher: dispatcher,
		interval:   2 * time.Second,
		batch:      100,
		nudge:      make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Nudge wakes the relay immediately — called post-commit so delivery
// latency stays milliseconds in the happy path.
func (r *Relay) Nudge() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// Run drains the outbox until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-r.nudge:
		case <-ticker.C:
		}
	}
}

// drain processes batches until the outbox is empty or ctx ends.
func (r *Relay) drain(ctx context.Context) {
	expanded := false
	for ctx.Err() == nil {
		processed := 0
		err := r.store.Expand(ctx, r.batch, func(envs []events.Envelope) []Result {
			processed = len(envs)
			results := make([]Result, 0, len(envs))
			for _, env := range envs {
				results = append(results, Result{
					EnvelopeID: env.ID,
					Err:        r.dispatcher.DispatchEnvelopes(ctx, env),
				})
			}
			return results
		})
		if err != nil {
			if r.onError != nil {
				r.onError(err)
			}
			break
		}
		expanded = expanded || processed > 0
		if processed < r.batch {
			break
		}
	}
	if expanded && r.afterExpand != nil {
		r.afterExpand()
	}
}
