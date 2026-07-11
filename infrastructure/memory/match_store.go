package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/zkrebbekx/flexitype/application/dedup"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// matchStore is the in-memory duplicate-detection store for the playground,
// isolated by tenant.
type matchStore struct {
	mu         sync.RWMutex
	rules      map[string]dedup.Rule      // keyed by rule id
	dismissals map[string]dedup.Dismissal // keyed by ruleID\x00a\x00b
}

// NewMatchStore builds an in-memory matching-rule store.
func NewMatchStore() dedup.Store {
	return &matchStore{rules: map[string]dedup.Rule{}, dismissals: map[string]dedup.Dismissal{}}
}

func (s *matchStore) CreateRule(_ context.Context, r dedup.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID.String()] = r
	return nil
}

func (s *matchStore) GetRule(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (dedup.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rules[id.String()]
	if !ok || r.TenantID != tenant {
		return dedup.Rule{}, domainerrors.NewNotFound("match_rule", id.String())
	}
	return r, nil
}

func (s *matchStore) ListRules(_ context.Context, tenant valueobjects.TenantID, typeDefID string) ([]dedup.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []dedup.Rule{}
	for _, r := range s.rules {
		if r.TenantID == tenant && r.TypeDefinitionID == typeDefID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.Before(out[b].CreatedAt) })
	return out, nil
}

func (s *matchStore) DeleteRule(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rules[id.String()]; ok && r.TenantID == tenant {
		delete(s.rules, id.String())
		for k, d := range s.dismissals {
			if d.RuleID == id {
				delete(s.dismissals, k)
			}
		}
	}
	return nil
}

func (s *matchStore) Dismiss(_ context.Context, d dedup.Dismissal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dismissals[d.RuleID.String()+"\x00"+d.EntityA+"\x00"+d.EntityB] = d
	return nil
}

func (s *matchStore) ListDismissals(_ context.Context, tenant valueobjects.TenantID, ruleID ulid.ID) ([]dedup.Dismissal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []dedup.Dismissal{}
	for _, d := range s.dismissals {
		if d.RuleID == ruleID && d.TenantID == tenant {
			out = append(out, d)
		}
	}
	return out, nil
}
