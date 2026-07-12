package memory

import (
	"context"

	"github.com/zkrebbekx/flexitype/application/search"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// searchStore persists entity search documents in the store, powering FQL
// matches() without PostgreSQL.
type searchStore struct{ s *Store }

// SearchStore returns the in-memory search projection store.
func (s *Store) SearchStore() search.DocumentStore { return &searchStore{s} }

func (st *searchStore) Upsert(_ context.Context, doc search.EntityDocument) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	st.s.searchDocs[doc.TenantID.String()+"\x00"+doc.EntityID.String()] = searchDoc{
		tenant: doc.TenantID.String(),
		typeID: doc.TypeDefinitionID.String(),
		entity: doc.EntityID.String(),
		values: doc.Values,
		text:   doc.Text,
	}
	return nil
}

func (st *searchStore) Remove(_ context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) error {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	delete(st.s.searchDocs, tenant.String()+"\x00"+entityID.String())
	return nil
}

func (st *searchStore) PurgeTenant(_ context.Context, tenant valueobjects.TenantID) (int, error) {
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	count := 0
	for k, doc := range st.s.searchDocs {
		if doc.tenant == tenant.String() {
			delete(st.s.searchDocs, k)
			count++
		}
	}
	return count, nil
}
