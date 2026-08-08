package outbox

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// EntityName is the activity-log entity for outbox recovery actions.
const EntityName = "outbox_envelope"

// ParkedEnvelope is one parked outbox row as the recovery surface reports
// it: enough to identify the event, see why it parked, and decide whether
// to redrive it.
type ParkedEnvelope struct {
	ID            string    `json:"id"`
	EventType     string    `json:"event_type"`
	AggregateType string    `json:"aggregate_type"`
	AggregateID   string    `json:"aggregate_id"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error"`
	RecordedAt    time.Time `json:"recorded_at"`
	ParkedAt      time.Time `json:"parked_at"`
}

// ParkedFilter narrows a parked listing or a redrive. Zero values mean "no
// constraint". ID selects exactly one envelope — the surgical redrive that
// leaves a known-poisonous sibling parked.
type ParkedFilter struct {
	TenantID  valueobjects.TenantID
	EventType string
	ID        string
}

// OpsStore is the persistence port for parked-envelope recovery. The
// PostgreSQL outbox adapter implements it next to Store; it is a separate
// interface so the relay's Store contract (and every test double of it)
// stays untouched.
type OpsStore interface {
	// ListParked returns one keyset page of the tenant's parked envelopes in
	// id order, plus the filtered total when the page asks for it.
	ListParked(ctx context.Context, filter ParkedFilter, page db.Page) ([]ParkedEnvelope, int, error)

	// Redrive returns the matching parked envelopes to the retry queue
	// inside the caller's transaction: parked_at and the lease are cleared,
	// attempts resets to zero and the row is due immediately. It reports how
	// many rows it moved.
	Redrive(ctx context.Context, tx db.Transactor, filter ParkedFilter) (int, error)
}

// Ops implements the parked-envelope recovery usecases: the operator-facing
// counterpart of the relay. A parked envelope is a committed change that was
// never delivered; without this surface it was invisible (no feed_seq, so no
// feed entry), undeliverable (Claim skips it) and unprunable — silent loss.
type Ops struct {
	uow   uow.UnitOfWork
	store OpsStore
	// nudge wakes the relay so a redriven envelope is claimed within
	// milliseconds rather than on the next poll tick. Optional.
	nudge func()
}

// NewOps wires the outbox recovery usecases.
func NewOps(u uow.UnitOfWork, store OpsStore, nudge func()) *Ops {
	return &Ops{uow: u, store: store, nudge: nudge}
}

// ListParkedInput is one parked-listing page request.
type ListParkedInput struct {
	EventType string
	ID        string
	Page      db.PageArgs
}

// ListParkedOutput is one page of parked envelopes.
type ListParkedOutput struct {
	Items    []ParkedEnvelope
	PageInfo db.PageInfo
}

// ListParked pages the calling tenant's parked envelopes.
func (o *Ops) ListParked(ctx context.Context, in ListParkedInput) (*ListParkedOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	filter, err := o.filter(ctx, in.EventType, in.ID)
	if err != nil {
		return nil, err
	}
	items, total, err := o.store.ListParked(ctx, filter, page)
	if err != nil {
		return nil, err
	}
	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(p ParkedEnvelope) string {
		return db.EncodeKeyset(p.ID)
	})
	return &ListParkedOutput{Items: items, PageInfo: info}, nil
}

// RedriveInput narrows a redrive. Empty fields redrive every parked envelope
// of the calling tenant.
type RedriveInput struct {
	EventType string
	ID        string
}

// Redrive returns the matching parked envelopes to the retry queue and
// reports how many it moved. Each redriven envelope gets a fresh retry
// budget (attempts = 0), mirroring the dead-letter redrive: an outage that
// exhausted the budget must not exhaust it again on the first post-recovery
// failure. The action is recorded in the activity log — a redrive
// re-publishes committed events, so it must be attributable — and the relay
// is nudged so delivery starts at once.
func (o *Ops) Redrive(ctx context.Context, in RedriveInput) (int, error) {
	filter, err := o.filter(ctx, in.EventType, in.ID)
	if err != nil {
		return 0, err
	}
	moved := 0
	err = o.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		n, err := o.store.Redrive(ctx, tx, filter)
		if err != nil {
			return err
		}
		moved = n
		if n == 0 {
			return nil
		}
		c.RecordChange(activity.Change{
			Entity:   EntityName,
			EntityID: redriveEntityID(filter),
			Action:   activity.ActionRestored,
			After: map[string]any{
				"redriven":   n,
				"event_type": filter.EventType,
				"id":         filter.ID,
			},
		})
		return nil
	})
	if err != nil {
		return 0, err
	}
	if moved > 0 && o.nudge != nil {
		o.nudge()
	}
	return moved, nil
}

// filter validates the optional narrowing arguments and stamps the tenant.
func (o *Ops) filter(ctx context.Context, eventType, rawID string) (ParkedFilter, error) {
	filter := ParkedFilter{TenantID: uow.TenantFromContext(ctx), EventType: eventType}
	if rawID != "" {
		id, err := ulid.Parse(rawID)
		if err != nil {
			return ParkedFilter{}, domainerrors.NewValidation(err.Error())
		}
		filter.ID = id.String()
	}
	return filter, nil
}

// redriveEntityID names the audit entry's subject: the envelope when the
// redrive targeted one, otherwise the scope it swept.
func redriveEntityID(f ParkedFilter) string {
	if f.ID != "" {
		return f.ID
	}
	if f.EventType != "" {
		return f.EventType
	}
	return "all"
}
