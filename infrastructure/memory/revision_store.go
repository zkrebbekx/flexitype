package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application/revision"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// revisionStore is the in-memory entity-revision store for the playground,
// isolated by tenant.
type revisionStore struct {
	mu   sync.RWMutex
	revs map[string]revision.Revision // keyed by id
}

// NewRevisionStore builds an in-memory entity-revision store.
func NewRevisionStore() revision.Store {
	return &revisionStore{revs: map[string]revision.Revision{}}
}

func (s *revisionStore) Create(_ context.Context, r revision.Revision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revs[r.ID.String()] = r
	return nil
}

func (s *revisionStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (revision.Revision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.revs[id.String()]
	if !ok || r.TenantID != tenant {
		return revision.Revision{}, domainerrors.NewNotFound("entity_revision", id.String())
	}
	return r, nil
}

// forEntity returns an entity's revisions sorted newest first (highest seq).
func (s *revisionStore) forEntity(tenant valueobjects.TenantID, typeDefID, entityID string) []revision.Revision {
	var out []revision.Revision
	for _, r := range s.revs {
		if r.TenantID == tenant && r.TypeDefinitionID == typeDefID && r.EntityID == entityID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Seq > out[b].Seq })
	return out
}

func (s *revisionStore) List(_ context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) ([]revision.Revision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.forEntity(tenant, typeDefID, entityID)
	if out == nil {
		out = []revision.Revision{}
	}
	return out, nil
}

func (s *revisionStore) AsOf(_ context.Context, tenant valueobjects.TenantID, typeDefID, entityID string, at time.Time) (revision.Revision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// forEntity is newest-first; the first revision at or before `at` wins.
	for _, r := range s.forEntity(tenant, typeDefID, entityID) {
		if !r.CreatedAt.After(at) {
			return r, nil
		}
	}
	return revision.Revision{}, domainerrors.NewNotFound("entity_revision", "as-of "+at.Format(time.RFC3339))
}

func (s *revisionStore) LastSeq(_ context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	revs := s.forEntity(tenant, typeDefID, entityID)
	if len(revs) == 0 {
		return 0, nil
	}
	return revs[0].Seq, nil // newest-first, so [0] is the highest seq
}
