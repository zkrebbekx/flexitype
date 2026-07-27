package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application/changeset"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
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
