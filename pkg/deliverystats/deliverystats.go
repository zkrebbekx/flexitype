// Package deliverystats defines the event-delivery depth contract that the
// storage layer produces and the metrics layer consumes.
//
// It exists to keep those two apart. The interface used to live in
// pkg/metrics, so infrastructure/postgres imported pkg/metrics purely to name
// the type it satisfies — and pkg/metrics imports an HTTP router (chi, to
// resolve route patterns for its middleware) and a metrics library. A
// monorepo vendoring only the Postgres backend inherited both, and a change
// to the HTTP metrics middleware forced a rebuild of the storage layer. It
// also inverted the dependency rule this repository documents: the storage
// adapter must not know about the transport.
//
// This package depends on the standard library alone, so both sides can
// import it without either seeing the other.
package deliverystats

import (
	"context"
	"time"
)

// Source reports current event-delivery depth. The storage layer implements
// it; the metrics layer turns it into scrape-time gauges.
type Source interface {
	// Snapshot returns current counts. It is called on each scrape, so it
	// must stay cheap — indexed COUNT queries, no table scans.
	Snapshot(ctx context.Context) (Depth, error)
}

// Depth is a point-in-time view of the outbox and delivery queues.
type Depth struct {
	OutboxPending      int64
	DeliveriesByStatus map[string]int64 // pending / inflight / delivered / dead

	// OldestPendingAge is how long the oldest undispatched envelope has been
	// waiting. It is the metric that actually tells an operator whether the
	// relay is keeping up: a depth of 500 is healthy under load and alarming
	// if the oldest of them is an hour old, and a count alone cannot tell
	// those apart. Zero when nothing is pending.
	OldestPendingAge time.Duration
}
