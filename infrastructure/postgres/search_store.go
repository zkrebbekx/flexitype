package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/search"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// searchStore persists the entity search projection.
type searchStore struct {
	q db.QueryExecer
}

// NewSearchStore builds the search projection adapter.
func NewSearchStore(q db.QueryExecer) search.DocumentStore {
	return &searchStore{q: q}
}

func (s *searchStore) Upsert(ctx context.Context, doc search.EntityDocument) error {
	document, err := json.Marshal(doc.Values)
	if err != nil {
		return fmt.Errorf("encode search document: %w", err)
	}

	_, err = s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_entity_search
		   (tenant_id, type_definition_id, entity_id, document, text_vector, updated_at)
		 VALUES (?, ?, ?, ?, to_tsvector('simple', ?), ?)
		 ON CONFLICT (tenant_id, entity_id) DO UPDATE SET
		   type_definition_id = EXCLUDED.type_definition_id,
		   document           = EXCLUDED.document,
		   text_vector        = EXCLUDED.text_vector,
		   updated_at         = EXCLUDED.updated_at`),
		doc.TenantID.String(), doc.TypeDefinitionID.String(), doc.EntityID.String(),
		jsonbParam(document), doc.Text, doc.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert search document: %w", err)
	}
	return nil
}

func (s *searchStore) Remove(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) error {
	_, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_search WHERE tenant_id = ? AND entity_id = ?`),
		tenant.String(), entityID.String())
	if err != nil {
		return fmt.Errorf("remove search document: %w", err)
	}
	return nil
}

func (s *searchStore) PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	res, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_search WHERE tenant_id = ?`), tenant.String())
	if err != nil {
		return 0, fmt.Errorf("purge tenant search documents: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
