package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/application/revision"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
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

// WithTx binds the store to a memory transaction so an erasure's revision purge
// joins the value write's unit of work: the removed revisions are captured and
// a rollback hook re-inserts them, so an aborted value transaction also un-does
// the revision purge — matching the Postgres backend, where the DELETE runs
// inside the same SQL transaction and rolls back with it. Reads and Create pass
// straight through. Outside a transaction (the executer is not a memory
// transaction) the base store is returned unchanged.
func (s *revisionStore) WithTx(tx db.Tx) revision.Store {
	if t, ok := tx.(db.Transactor); ok {
		return &txRevisionStore{revisionStore: s, tx: t}
	}
	return s
}

// txRevisionStore is a revision store bound to a memory transaction; its purges
// register a rollback hook restoring the removed revisions, giving the same
// atomicity the SQL DELETE has inside its transaction.
type txRevisionStore struct {
	*revisionStore
	tx db.Transactor
}

func (s *txRevisionStore) PurgeEntity(_ context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error) {
	s.mu.Lock()
	var removed []revision.Revision
	for id, r := range s.revs {
		if r.TenantID == tenant && r.TypeDefinitionID == typeDefID && r.EntityID == entityID {
			removed = append(removed, r)
			delete(s.revs, id)
		}
	}
	s.mu.Unlock()
	s.restoreOnRollback(removed)
	return len(removed), nil
}

func (s *txRevisionStore) PurgeTenant(_ context.Context, tenant valueobjects.TenantID) (int, error) {
	s.mu.Lock()
	var removed []revision.Revision
	for id, r := range s.revs {
		if r.TenantID == tenant {
			removed = append(removed, r)
			delete(s.revs, id)
		}
	}
	s.mu.Unlock()
	s.restoreOnRollback(removed)
	return len(removed), nil
}

// restoreOnRollback re-inserts the removed revisions if the bound transaction
// rolls back, so the revision purge is atomic with the value write it joined.
func (s *txRevisionStore) restoreOnRollback(removed []revision.Revision) {
	if len(removed) == 0 {
		return
	}
	s.tx.OnRollback(func(context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, r := range removed {
			s.revs[r.ID.String()] = r
		}
		return nil
	})
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

func (s *revisionStore) PurgeEntity(_ context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for id, r := range s.revs {
		if r.TenantID == tenant && r.TypeDefinitionID == typeDefID && r.EntityID == entityID {
			delete(s.revs, id)
			count++
		}
	}
	return count, nil
}

func (s *revisionStore) PurgeTenant(_ context.Context, tenant valueobjects.TenantID) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for id, r := range s.revs {
		if r.TenantID == tenant {
			delete(s.revs, id)
			count++
		}
	}
	return count, nil
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
