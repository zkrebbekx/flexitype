package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/zkrebbekx/flexitype/application/unit"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// unitStore is the in-memory unit-family store for the playground.
type unitStore struct {
	mu       sync.RWMutex
	families map[string]unit.Family
}

// NewUnitFamilyStore builds an in-memory unit-family store.
func NewUnitFamilyStore() unit.Store {
	return &unitStore{families: map[string]unit.Family{}}
}

func (s *unitStore) Create(_ context.Context, f unit.Family) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.families[f.ID.String()] = f
	return nil
}

func (s *unitStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (unit.Family, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.families[id.String()]
	if !ok || f.TenantID != tenant {
		return unit.Family{}, domainerrors.NewNotFound("unit_family", id.String())
	}
	return f, nil
}

func (s *unitStore) List(_ context.Context, tenant valueobjects.TenantID) ([]unit.Family, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []unit.Family{}
	for _, f := range s.families {
		if f.TenantID == tenant {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func (s *unitStore) Delete(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.families[id.String()]; ok && f.TenantID == tenant {
		delete(s.families, id.String())
	}
	return nil
}
