package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/zkrebbekx/flexitype/application/savedview"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// savedViewStore is the in-memory saved-view store for the playground,
// isolated by tenant.
type savedViewStore struct {
	mu    sync.RWMutex
	views map[string]savedview.View // keyed by id
}

// NewSavedViewStore builds an in-memory saved-view store.
func NewSavedViewStore() savedview.Store {
	return &savedViewStore{views: map[string]savedview.View{}}
}

func (s *savedViewStore) Create(_ context.Context, v savedview.View) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.views {
		if existing.TenantID == v.TenantID && existing.Name == v.Name {
			return domainerrors.NewConflict("a view with this name already exists", "name", v.Name)
		}
	}
	s.views[v.ID.String()] = v
	return nil
}

func (s *savedViewStore) Update(_ context.Context, v savedview.View) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.views[v.ID.String()] = v
	return nil
}

func (s *savedViewStore) Delete(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.views[id.String()]; ok && v.TenantID == tenant {
		delete(s.views, id.String())
	}
	return nil
}

func (s *savedViewStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (savedview.View, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.views[id.String()]
	if !ok || v.TenantID != tenant {
		return savedview.View{}, domainerrors.NewNotFound("saved_view", id.String())
	}
	return v, nil
}

func (s *savedViewStore) List(_ context.Context, tenant valueobjects.TenantID) ([]savedview.View, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []savedview.View
	for _, v := range s.views {
		if v.TenantID == tenant {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
