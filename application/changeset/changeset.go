// Package changeset batches value mutations into a reviewable draft that
// leaves live data untouched until it is published. A change-set moves
// draft → in_review → approved → published (or rejected); publish applies
// every mutation in one unit of work — atomic, with full events and
// activity, exactly like direct writes. An optional publish_at defers the
// apply to a scheduler.
package changeset

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// State is a change-set's lifecycle stage.
type State string

// The change-set lifecycle states.
const (
	StateDraft     State = "draft"
	StateInReview  State = "in_review"
	StateApproved  State = "approved"
	StatePublished State = "published"
	StateRejected  State = "rejected"
)

// ChangeSet is a named, reviewable batch of value mutations.
type ChangeSet struct {
	ID       ulid.ID               `json:"id"`
	TenantID valueobjects.TenantID `json:"tenant_id"`
	Name     string                `json:"name"`
	State    State                 `json:"state"`
	// RequireApproval demands an approver distinct from the author before
	// the set may publish.
	RequireApproval bool                `json:"require_approval"`
	Author          string              `json:"author,omitempty"`
	Approver        string              `json:"approver,omitempty"`
	Mutations       []appvalue.Mutation `json:"mutations"`
	PublishAt       *time.Time          `json:"publish_at,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	PublishedAt     *time.Time          `json:"published_at,omitempty"`
}

// Store persists change-sets, scoped by tenant.
type Store interface {
	Create(ctx context.Context, cs ChangeSet) error
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (ChangeSet, error)
	List(ctx context.Context, tenant valueobjects.TenantID) ([]ChangeSet, error)
	Update(ctx context.Context, cs ChangeSet) error
	// DueForPublish returns approved change-sets whose publish_at has
	// arrived, across all tenants (the scheduler runs outside a request).
	DueForPublish(ctx context.Context, now time.Time) ([]ChangeSet, error)
}

// Interactor implements the change-management usecases.
type Interactor struct {
	store  Store
	values *appvalue.Interactor
	now    func() time.Time
}

// NewInteractor wires the change-set usecases.
func NewInteractor(store Store, values *appvalue.Interactor, now func() time.Time) *Interactor {
	if now == nil {
		now = uow.UTCNow
	}
	return &Interactor{store: store, values: values, now: now}
}

// CreateInput carries a new change-set's fields.
type CreateInput struct {
	Name            string
	RequireApproval bool
	PublishAt       *time.Time
}

// Create opens a draft change-set authored by the calling actor.
func (i *Interactor) Create(ctx context.Context, in CreateInput) (*ChangeSet, error) {
	if in.Name == "" {
		return nil, domainerrors.NewValidation("change-set name is required")
	}
	now := i.now()
	cs := ChangeSet{
		ID:              ulid.New(),
		TenantID:        uow.TenantFromContext(ctx),
		Name:            in.Name,
		State:           StateDraft,
		RequireApproval: in.RequireApproval,
		Author:          uow.ActorFromContext(ctx).ID,
		Mutations:       []appvalue.Mutation{},
		PublishAt:       in.PublishAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := i.store.Create(ctx, cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// AddMutation appends a value mutation to a draft change-set.
func (i *Interactor) AddMutation(ctx context.Context, rawID string, m appvalue.Mutation) (*ChangeSet, error) {
	return i.mutate(ctx, rawID, func(cs *ChangeSet) error {
		if cs.State != StateDraft {
			return domainerrors.NewValidation("change-set is not a draft", "state", string(cs.State))
		}
		if m.Kind != appvalue.MutationSet && m.Kind != appvalue.MutationRemove {
			return domainerrors.NewValidation("unknown mutation kind", "kind", m.Kind)
		}
		cs.Mutations = append(cs.Mutations, m)
		return nil
	})
}

// Submit moves a draft into review.
func (i *Interactor) Submit(ctx context.Context, rawID string) (*ChangeSet, error) {
	return i.mutate(ctx, rawID, func(cs *ChangeSet) error {
		if cs.State != StateDraft {
			return domainerrors.NewValidation("only a draft can be submitted", "state", string(cs.State))
		}
		if len(cs.Mutations) == 0 {
			return domainerrors.NewValidation("change-set has no mutations")
		}
		cs.State = StateInReview
		return nil
	})
}

// Approve marks an in-review change-set approved. When approval is
// required, the approver must differ from the author.
func (i *Interactor) Approve(ctx context.Context, rawID string) (*ChangeSet, error) {
	actor := uow.ActorFromContext(ctx).ID
	return i.mutate(ctx, rawID, func(cs *ChangeSet) error {
		if cs.State != StateInReview {
			return domainerrors.NewValidation("only an in-review change-set can be approved", "state", string(cs.State))
		}
		if cs.RequireApproval {
			// Separation of duties: an unidentified principal (e.g. the
			// unauthenticated dev actor, id "") cannot satisfy the
			// distinct-approver rule, and must not fall through it.
			if actor == "" {
				return domainerrors.NewForbidden("approval requires an authenticated account distinct from the author")
			}
			if actor == cs.Author {
				return domainerrors.NewForbidden("approval requires a different account than the author")
			}
		}
		cs.State = StateApproved
		cs.Approver = actor
		return nil
	})
}

// Reject closes a change-set without publishing; live data is untouched.
func (i *Interactor) Reject(ctx context.Context, rawID string) (*ChangeSet, error) {
	return i.mutate(ctx, rawID, func(cs *ChangeSet) error {
		if cs.State == StatePublished {
			return domainerrors.NewValidation("a published change-set cannot be rejected")
		}
		cs.State = StateRejected
		return nil
	})
}

// Publish applies every mutation atomically and marks the set published. It
// requires approval when the set demands it; a constraint failure rolls the
// whole batch back and the set stays as it was.
func (i *Interactor) Publish(ctx context.Context, rawID string) (*ChangeSet, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)
	cs, err := i.store.Get(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if err := i.publish(ctx, &cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// publish applies the mutations and persists the published state. The caller
// supplies a change-set already loaded under the right tenant context.
func (i *Interactor) publish(ctx context.Context, cs *ChangeSet) error {
	switch cs.State {
	case StateApproved:
	case StateDraft, StateInReview:
		if cs.RequireApproval {
			return domainerrors.NewValidation("change-set must be approved before publishing", "state", string(cs.State))
		}
	default:
		return domainerrors.NewValidation("change-set cannot be published", "state", string(cs.State))
	}
	if err := i.values.ApplyMutations(ctx, cs.Mutations); err != nil {
		return err // atomic: nothing applied, the set is unchanged
	}
	now := i.now()
	cs.State = StatePublished
	cs.PublishedAt = &now
	cs.UpdatedAt = now
	return i.store.Update(ctx, *cs)
}

// Get returns one change-set.
func (i *Interactor) Get(ctx context.Context, rawID string) (*ChangeSet, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	cs, err := i.store.Get(ctx, uow.TenantFromContext(ctx), id)
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

// List returns the tenant's change-sets, newest first.
func (i *Interactor) List(ctx context.Context) ([]ChangeSet, error) {
	return i.store.List(ctx, uow.TenantFromContext(ctx))
}

// PublishDue publishes every approved change-set whose publish_at has
// arrived. It is the scheduler's tick; each set publishes in its own tenant
// context so events and activity attribute correctly. Returns how many
// published.
func (i *Interactor) PublishDue(ctx context.Context) (int, error) {
	due, err := i.store.DueForPublish(ctx, i.now())
	if err != nil {
		return 0, err
	}
	published := 0
	for idx := range due {
		cs := due[idx]
		tctx := uow.WithTenant(ctx, cs.TenantID)
		if err := i.publish(tctx, &cs); err != nil {
			continue // a failed set stays approved for the next tick / manual retry
		}
		published++
	}
	return published, nil
}

// mutate loads, applies fn, and persists a change-set under the tenant.
func (i *Interactor) mutate(ctx context.Context, rawID string, fn func(*ChangeSet) error) (*ChangeSet, error) {
	id, err := ulid.Parse(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	cs, err := i.store.Get(ctx, uow.TenantFromContext(ctx), id)
	if err != nil {
		return nil, err
	}
	if err := fn(&cs); err != nil {
		return nil, err
	}
	cs.UpdatedAt = i.now()
	if err := i.store.Update(ctx, cs); err != nil {
		return nil, err
	}
	return &cs, nil
}
