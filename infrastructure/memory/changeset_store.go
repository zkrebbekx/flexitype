package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application/changeset"
	"github.com/zkrebbekx/flexitype/application/erasure"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// changesetStore is the in-memory change-set store for the playground.
type changesetStore struct {
	mu   sync.RWMutex
	sets map[string]changeset.ChangeSet
}

// NewChangeSetStore builds an in-memory change-set store.
func NewChangeSetStore() changeset.Store {
	return &changesetStore{sets: map[string]changeset.ChangeSet{}}
}

// ChangeSetEraserFor exposes the residual eraser for a store built by
// NewChangeSetStore, whose concrete type is unexported.
func ChangeSetEraserFor(s changeset.Store) erasure.ResidualEraser {
	store, ok := s.(*changesetStore)
	if !ok {
		return nil
	}
	return store.ChangeSetEraser()
}

func (s *changesetStore) Create(_ context.Context, cs changeset.ChangeSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cs.Version == 0 {
		cs.Version = 1
	}
	s.sets[cs.ID.String()] = cs
	return nil
}

// Update is a compare-and-swap on version, mirroring the Postgres store: a
// caller holding a stale read gets a conflict rather than overwriting
// whatever landed in between.
func (s *changesetStore) Update(_ context.Context, cs changeset.ChangeSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.sets[cs.ID.String()]
	if !ok || stored.TenantID != cs.TenantID {
		return domainerrors.NewNotFound("changeset", cs.ID.String())
	}
	if stored.Version != cs.Version {
		return changeset.ErrStaleVersion(cs.ID.String(), cs.Version)
	}
	cs.Version = stored.Version + 1
	s.sets[cs.ID.String()] = cs
	return nil
}

func (s *changesetStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (changeset.ChangeSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cs, ok := s.sets[id.String()]
	if !ok || cs.TenantID != tenant {
		return changeset.ChangeSet{}, domainerrors.NewNotFound("changeset", id.String())
	}
	return cs, nil
}

func (s *changesetStore) List(_ context.Context, tenant valueobjects.TenantID) ([]changeset.ChangeSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []changeset.ChangeSet{}
	for _, cs := range s.sets {
		if cs.TenantID == tenant {
			out = append(out, cs)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out, nil
}

func (s *changesetStore) DueForPublish(_ context.Context, now time.Time) ([]changeset.ChangeSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []changeset.ChangeSet{}
	for _, cs := range s.sets {
		if cs.State == changeset.StateApproved && cs.PublishAt != nil && !cs.PublishAt.After(now) {
			out = append(out, cs)
		}
	}
	return out, nil
}

// StalePublishing returns change-sets whose publish claim is older than the
// cutoff, so the scheduler can retry a publish that never finished. See
// changeset.ClaimReclaimer.
func (s *changesetStore) StalePublishing(_ context.Context, before time.Time) ([]changeset.ChangeSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []changeset.ChangeSet{}
	for _, cs := range s.sets {
		if cs.State == changeset.StatePublishing && !cs.UpdatedAt.After(before) {
			out = append(out, cs)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].UpdatedAt.Before(out[b].UpdatedAt) })
	return out, nil
}

// ChangeSetEraser builds the in-memory change-set residual eraser, mirroring
// the Postgres one.
//
// Mutations embed the value verbatim, and a draft or rejected set is never
// pruned, so a purged value stayed readable there indefinitely while the
// erasure report said it was gone. The mutation skeleton survives — kind,
// attribute, entity and scope — so the set still reports what it contains;
// only the value goes.
func (s *changesetStore) ChangeSetEraser() erasure.ResidualEraser {
	return &changeSetEraser{s: s}
}

type changeSetEraser struct{ s *changesetStore }

func (e *changeSetEraser) Name() string { return "change-sets" }

func (e *changeSetEraser) RedactEntity(_ context.Context, _ db.Tx, tenant valueobjects.TenantID, entityID string) (int, error) {
	return e.redact(tenant, func(m appvalue.Mutation) bool { return m.EntityID == entityID }), nil
}

func (e *changeSetEraser) RedactTenant(_ context.Context, _ db.Tx, tenant valueobjects.TenantID) (int, error) {
	return e.redact(tenant, func(m appvalue.Mutation) bool { return m.EntityID != "" }), nil
}

// redact rewrites matching mutations and returns how many SETS changed, so
// the count means the same thing as the Postgres eraser's rows-affected.
func (e *changeSetEraser) redact(tenant valueobjects.TenantID, match func(appvalue.Mutation) bool) int {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	changed := 0
	for id, cs := range e.s.sets {
		if cs.TenantID != tenant {
			continue
		}
		touched := false
		muts := make([]appvalue.Mutation, len(cs.Mutations))
		copy(muts, cs.Mutations)
		for idx := range muts {
			if !match(muts[idx]) || muts[idx].Value == nil {
				continue
			}
			muts[idx].Value = nil
			muts[idx].Erased = true
			touched = true
		}
		if !touched {
			continue
		}
		cs.Mutations = muts
		e.s.sets[id] = cs
		changed++
	}
	return changed
}
