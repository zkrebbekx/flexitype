package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lib/pq"

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
	return s.upsertAttrVectors(ctx, doc)
}

// upsertAttrVectors rewrites the entity's per-attribute vectors.
//
// It replaces rather than merges: an attribute whose last value was removed
// must lose its row, or a search would keep finding a value the entity no
// longer holds. The delete and the insert are two statements in the caller's
// transaction, so a reader never sees the entity half-indexed.
//
// The entity id goes in under the empty attribute name. It is not an
// attribute and no policy hides it, so every principal can still find an
// entity by id — which is what the flattened document allowed.
func (s *searchStore) upsertAttrVectors(ctx context.Context, doc search.EntityDocument) error {
	if _, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_search_attr WHERE tenant_id = ? AND entity_id = ?`),
		doc.TenantID.String(), doc.EntityID.String()); err != nil {
		return fmt.Errorf("clear search attribute vectors: %w", err)
	}

	// SearchableValues, not Values: the per-attribute vectors index exactly
	// what the entity-level vector does, so a restricted principal cannot
	// find an entity by something an admin cannot.
	names := make([]string, 0, len(doc.SearchableValues)+1)
	texts := make([]string, 0, len(doc.SearchableValues)+1)
	names = append(names, "")
	texts = append(texts, doc.EntityID.String())
	for name, values := range doc.SearchableValues {
		names = append(names, name)
		texts = append(texts, strings.Join(values, " "))
	}

	if _, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_entity_search_attr (tenant_id, entity_id, attribute_name, text_vector)
		 SELECT ?, ?, n.name, to_tsvector('simple', n.text)
		   FROM unnest(?::text[], ?::text[]) AS n(name, text)`),
		doc.TenantID.String(), doc.EntityID.String(), pq.Array(names), pq.Array(texts)); err != nil {
		return fmt.Errorf("upsert search attribute vectors: %w", err)
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
	if _, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_search_attr WHERE tenant_id = ? AND entity_id = ?`),
		tenant.String(), entityID.String()); err != nil {
		return fmt.Errorf("remove search attribute vectors: %w", err)
	}
	return nil
}

func (s *searchStore) PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	res, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_search WHERE tenant_id = ?`), tenant.String())
	if err != nil {
		return 0, fmt.Errorf("purge tenant search documents: %w", err)
	}
	if _, derr := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_search_attr WHERE tenant_id = ?`), tenant.String()); derr != nil {
		return 0, fmt.Errorf("purge tenant search attribute vectors: %w", derr)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
