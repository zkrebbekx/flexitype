// Package feed serves the ordered event log to pull consumers: cursor
// pages over expanded envelopes, an SSE-friendly tail, and named
// compare-and-swap cursors so a replicated consuming service reads as one
// logical consumer. Design: docs/design/event-delivery.md.
package feed

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// ErrCursorConflict is returned when a compare-and-swap commit loses the
// race — another consumer replica advanced the cursor first.
var ErrCursorConflict = errors.New("feed: cursor position changed since read")

// ErrGone is returned when a cursor points before the retention floor:
// events were pruned, so the consumer must re-baseline instead of
// silently missing history.
var ErrGone = errors.New("feed: cursor older than retention; re-baseline required")

var consumerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// Event is one feed entry.
type Event struct {
	Seq      int64           `json:"seq"`
	Envelope events.Envelope `json:"envelope"`
}

// Store reads the expanded event log.
type Store interface {
	// List returns events with feed_seq > after for the tenant, in feed
	// order, optionally filtered by event type.
	List(ctx context.Context, tenant valueobjects.TenantID, after int64, types []string, limit int) ([]Event, error)

	// Floor returns the smallest retained feed_seq for the tenant (0 when
	// no events are retained).
	Floor(ctx context.Context, tenant valueobjects.TenantID) (int64, error)

	// Prune deletes expanded envelopes recorded before the cutoff whose
	// deliveries have all settled. Returns rows removed.
	Prune(ctx context.Context, cutoff time.Time) (int, error)
}

// CursorStore persists named consumer cursors.
type CursorStore interface {
	Get(ctx context.Context, tenant valueobjects.TenantID, consumer string) (int64, error)
	// Commit advances the cursor with compare-and-swap: it fails with
	// ErrCursorConflict unless the stored position equals expected.
	Commit(ctx context.Context, tenant valueobjects.TenantID, consumer string, position, expected int64, now time.Time) error
}

// Interactor implements the feed usecases.
type Interactor struct {
	store   Store
	cursors CursorStore
	now     func() time.Time
}

// NewInteractor wires the feed usecases.
func NewInteractor(store Store, cursors CursorStore) *Interactor {
	return &Interactor{store: store, cursors: cursors, now: time.Now}
}

// ListInput is one feed page request.
type ListInput struct {
	After int64
	Types []string
	Limit int
}

// ListOutput is one feed page.
type ListOutput struct {
	Items []Event `json:"items"`
	// NextCursor feeds the following request's After; equals After when
	// the page is empty.
	NextCursor int64 `json:"next_cursor"`
}

// List pages the event log. Returns ErrGone when After points before the
// retention floor.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	if in.After < 0 {
		return nil, domainerrors.NewValidation("after must not be negative")
	}
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tenant := uow.TenantFromContext(ctx)

	if in.After > 0 {
		floor, err := i.store.Floor(ctx, tenant)
		if err != nil {
			return nil, err
		}
		// A cursor strictly below floor-1 skips pruned events.
		if floor > 0 && in.After < floor-1 {
			return nil, ErrGone
		}
	}

	items, err := i.store.List(ctx, tenant, in.After, in.Types, limit)
	if err != nil {
		return nil, err
	}
	out := &ListOutput{Items: items, NextCursor: in.After}
	if len(items) > 0 {
		out.NextCursor = items[len(items)-1].Seq
	}
	return out, nil
}

// Cursor returns a named consumer's committed position (0 = start).
func (i *Interactor) Cursor(ctx context.Context, consumer string) (int64, error) {
	if !consumerPattern.MatchString(consumer) {
		return 0, domainerrors.NewValidation("consumer name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	return i.cursors.Get(ctx, uow.TenantFromContext(ctx), consumer)
}

// CommitCursor advances a named cursor via compare-and-swap. Returns
// ErrCursorConflict when another replica advanced it first.
func (i *Interactor) CommitCursor(ctx context.Context, consumer string, position, expected int64) error {
	if !consumerPattern.MatchString(consumer) {
		return domainerrors.NewValidation("consumer name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	if position < 0 || expected < 0 {
		return domainerrors.NewValidation("cursor positions must not be negative")
	}
	if position < expected {
		return domainerrors.NewValidation("cursor cannot move backwards")
	}
	return i.cursors.Commit(ctx, uow.TenantFromContext(ctx), consumer, position, expected, i.now().UTC())
}

// Pruner deletes events past retention on an interval.
type Pruner struct {
	store     Store
	retention time.Duration
	interval  time.Duration
	onError   func(error)
	now       func() time.Time
}

// NewPruner builds a retention pruner (interval defaults to hourly).
func NewPruner(store Store, retention time.Duration, onError func(error)) *Pruner {
	return &Pruner{
		store:     store,
		retention: retention,
		interval:  time.Hour,
		onError:   onError,
		now:       time.Now,
	}
}

// Run prunes until ctx ends.
func (p *Pruner) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		if _, err := p.store.Prune(ctx, p.now().UTC().Add(-p.retention)); err != nil && p.onError != nil {
			p.onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
