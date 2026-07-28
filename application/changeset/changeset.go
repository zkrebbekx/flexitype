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

	"github.com/zkrebbekx/flexitype/application/fieldacl"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// State is a change-set's lifecycle stage.
type State string

// The change-set lifecycle states.
const (
	StateDraft    State = "draft"
	StateInReview State = "in_review"
	StateApproved State = "approved"
	// StatePublishing is held while the mutations are being applied.
	//
	// It exists so the claim can be taken BEFORE the side effects. Publish
	// used to apply the mutations and only then compare-and-swap the record:
	// once optimistic locking made that second call able to fail, any
	// concurrent touch of the set — a reviewer rejecting it, a second
	// publish, the scheduler tick — left the data committed and the record
	// saying something else. Through PublishDue it compounded: the set stayed
	// approved with publish_at in the past, so every tick re-applied the same
	// mutations over whatever had been written in between.
	//
	// A set left in this state means a publish began and did not finish. The
	// scheduler does not pick it up (it selects approved), so it is visible
	// and inert rather than silently repeating.
	StatePublishing State = "publishing"
	StatePublished  State = "published"
	StateRejected   State = "rejected"
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
	// Version increments on every mutation and guards against a lost update.
	// Without it, two reviewers editing one set overwrote each other's
	// mutations, and an edit that raced an approval wrote the pre-approval
	// state back — silently reverting the approval. Every other aggregate in
	// this repository takes a row lock; change-sets take this instead,
	// because a review artifact is edited over minutes, not milliseconds, and
	// a conflict should be reported to the reviewer rather than serialized
	// behind a lock they cannot see.
	Version int `json:"version"`
}

// Store persists change-sets, scoped by tenant.
type Store interface {
	Create(ctx context.Context, cs ChangeSet) error
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (ChangeSet, error)
	List(ctx context.Context, tenant valueobjects.TenantID) ([]ChangeSet, error)
	// Update persists the set only if the stored version still matches
	// cs.Version, and returns a conflict otherwise. It increments the stored
	// version on success.
	Update(ctx context.Context, cs ChangeSet) error
	// DueForPublish returns approved change-sets whose publish_at has
	// arrived, across all tenants (the scheduler runs outside a request).
	DueForPublish(ctx context.Context, now time.Time) ([]ChangeSet, error)
}

// Interactor implements the change-management usecases.
type Interactor struct {
	// attrs resolves attribute definitions for the field ACL. A change-set
	// mutation embeds a value verbatim, so a set read without the ACL served
	// the very values every other surface redacts.
	attrs domainattribute.Repository
	// onPublishFailure observes a scheduled publish that failed. nil drops
	// the report, which is the pre-existing behaviour for an embedder that
	// wires no observer.
	onPublishFailure func(cs ChangeSet, err error)
	store            Store
	values           *appvalue.Interactor
	now              func() time.Time
}

// NewInteractor wires the change-set usecases.
func NewInteractor(store Store, values *appvalue.Interactor, attrs domainattribute.Repository, now func() time.Time) *Interactor {
	if now == nil {
		now = uow.UTCNow
	}
	return &Interactor{store: store, values: values, attrs: attrs, now: now}
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
		// A new set starts at 1, so the first read a client takes already
		// carries a version it can compare-and-swap against.
		Version: 1,
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
		// Staging a value is writing it, only later. Without this check a
		// principal barred from an attribute could stage a change to it and
		// have an approver publish it — the write path's ACL never sees the
		// author.
		if err := i.assertWritable(ctx, m); err != nil {
			return err
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
		if cs.State == StatePublishing {
			return domainerrors.NewValidation("a publishing change-set cannot be rejected")
		}
		cs.State = StateRejected
		return nil
	})
}

// Publish applies every mutation atomically and marks the set published. It
// requires approval when the set demands it.
//
// The set is CLAIMED first (state publishing, version bumped), then the
// mutations are applied, then the claim is finalised. A constraint failure
// rolls the whole batch back and the claim is handed back, so the set is
// publishable again once the cause is fixed. A concurrent edit loses the race
// while the data is still untouched, rather than after it has been written.
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
	// CLAIM FIRST. The compare-and-swap runs before the mutations, so a
	// concurrent edit loses the race while the data is still untouched.
	// Applying first meant a failed swap left the values written and the
	// record disagreeing with them.
	claimed := *cs
	claimed.State = StatePublishing
	claimed.UpdatedAt = i.now()
	if err := i.store.Update(ctx, claimed); err != nil {
		return err
	}
	claimed.Version++ // the store bumped it on success

	if err := i.values.ApplyMutations(ctx, claimed.Mutations); err != nil {
		// The batch is atomic, so nothing was written. Hand the claim back so
		// the set is publishable again once the cause is fixed; the state is
		// ours to release, because nobody else can transition out of
		// publishing.
		release := claimed
		release.State = cs.State
		release.UpdatedAt = i.now()
		if rerr := i.store.Update(ctx, release); rerr == nil {
			*cs = release
			cs.Version++
		}
		return err
	}

	now := i.now()
	claimed.State = StatePublished
	claimed.PublishedAt = &now
	claimed.UpdatedAt = now
	if err := i.store.Update(ctx, claimed); err != nil {
		// The data is committed and the record cannot be advanced. Leaving it
		// in publishing is the honest outcome: the scheduler will not re-run
		// it, and the state names what happened.
		return err
	}
	claimed.Version++
	*cs = claimed
	return nil
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
	if err := i.redact(ctx, &cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// List returns the tenant's change-sets, newest first, with the field ACL
// applied to every mutation.
func (i *Interactor) List(ctx context.Context) ([]ChangeSet, error) {
	sets, err := i.store.List(ctx, uow.TenantFromContext(ctx))
	if err != nil {
		return nil, err
	}
	for idx := range sets {
		if err := i.redact(ctx, &sets[idx]); err != nil {
			return nil, err
		}
	}
	return sets, nil
}

// redact masks the value of every mutation the caller may not read.
//
// The skeleton survives — kind, attribute, entity, scope — so a reviewer sees
// that a change exists and can count it, exactly as the feed and the activity
// log do. Only the value goes. Removing the whole mutation would misreport
// what a set contains, and leaving it meant a principal with `salary: none`
// read the salary from another user's staged pay review, while the same value
// was filtered from every other surface.
func (i *Interactor) redact(ctx context.Context, cs *ChangeSet) error {
	if len(cs.Mutations) == 0 {
		return nil
	}
	ids := make([]valueobjects.AttributeDefinitionID, 0, len(cs.Mutations))
	for _, m := range cs.Mutations {
		id, err := valueobjects.ParseAttributeDefinitionID(m.AttributeDefinitionID)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	readable, err := fieldacl.New(i.attrs).Readable(ctx, ids)
	if err != nil {
		return err
	}
	for idx := range cs.Mutations {
		if readable[cs.Mutations[idx].AttributeDefinitionID] {
			continue
		}
		cs.Mutations[idx].Value = nil
		cs.Mutations[idx].Redacted = true
	}
	return nil
}

// assertWritable refuses a mutation on an attribute the caller may not write.
func (i *Interactor) assertWritable(ctx context.Context, m appvalue.Mutation) error {
	id, err := valueobjects.ParseAttributeDefinitionID(m.AttributeDefinitionID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	ok, err := fieldacl.New(i.attrs).CanWrite(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		// The same shape the write path uses for an unwritable attribute.
		return domainerrors.NewForbidden("the attribute is not writable")
	}
	return nil
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
			// A failed set stays approved for the next tick, so it retries —
			// but the failure has to be visible. This used to be a bare
			// `continue`: a set that could never publish retried for ever
			// with no log line, no metric and no observer callback, and the
			// only symptom was a scheduled change that never arrived.
			i.reportPublishFailure(cs, err)
			continue
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
	cs.Version++
	return &cs, nil
}

// ErrStaleVersion is the conflict a store returns when a change-set was
// modified between the caller's read and its write. The caller re-reads and
// re-applies; nothing is written from a stale view.
func ErrStaleVersion(id string, version int) error {
	return domainerrors.NewConflict(
		"the change-set was modified by someone else; re-read it and retry",
		"change_set", id, "version", version)
}

// OnPublishFailure registers the observer for a scheduled publish failure.
// Wire it during composition.
func (i *Interactor) OnPublishFailure(fn func(cs ChangeSet, err error)) {
	i.onPublishFailure = fn
}

// reportPublishFailure surfaces one set's failure without stopping the tick:
// one bad set must not block the others, and it must not be silent either.
func (i *Interactor) reportPublishFailure(cs ChangeSet, err error) {
	if i.onPublishFailure == nil {
		return
	}
	i.onPublishFailure(cs, err)
}
