package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/application/revision"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

type revisionStore struct {
	q db.QueryExecer
}

// NewRevisionStore builds the entity-revision store over the pool.
func NewRevisionStore(q db.QueryExecer) revision.Store {
	return &revisionStore{q: q}
}

// WithTx binds the store to a transaction so a revision purge runs inside the
// value write's transaction and rolls back with it — mirroring the value
// repository's WithTx.
func (s *revisionStore) WithTx(tx db.Tx) revision.Store {
	return &revisionStore{q: txExecer(tx)}
}

type revisionRow struct {
	ID               ulid.ID   `db:"id"`
	TenantID         string    `db:"tenant_id"`
	TypeDefinitionID string    `db:"type_definition_id"`
	EntityID         string    `db:"entity_id"`
	Seq              int       `db:"seq"`
	Label            string    `db:"label"`
	CreatedAt        time.Time `db:"created_at"`
	Values           []byte    `db:"values"`
}

func (r revisionRow) toRevision() (revision.Revision, error) {
	vals := []revision.Value{}
	if len(r.Values) > 0 {
		if err := json.Unmarshal(r.Values, &vals); err != nil {
			return revision.Revision{}, fmt.Errorf("decode revision values for %s: %w", r.ID, err)
		}
	}
	return revision.Revision{
		ID:               r.ID,
		TenantID:         valueobjects.TenantID(r.TenantID),
		TypeDefinitionID: r.TypeDefinitionID,
		EntityID:         r.EntityID,
		Seq:              r.Seq,
		Label:            r.Label,
		CreatedAt:        r.CreatedAt,
		Values:           vals,
	}, nil
}

func (s *revisionStore) Create(ctx context.Context, r revision.Revision) error {
	vals, _ := json.Marshal(r.Values)
	_, err := s.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_entity_revision
		   (id, tenant_id, type_definition_id, entity_id, seq, label, created_at, values)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		r.ID, r.TenantID.String(), r.TypeDefinitionID, r.EntityID, r.Seq, r.Label, r.CreatedAt, jsonbParam(vals))
	if err != nil {
		return fmt.Errorf("insert entity revision: %w", err)
	}
	return nil
}

func (s *revisionStore) Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (revision.Revision, error) {
	var row revisionRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, type_definition_id, entity_id, seq, label, created_at, values
		 FROM flexitype_entity_revision WHERE id = ? AND tenant_id = ?`), id, tenant.String())
	if isNoRows(err) {
		return revision.Revision{}, domainerrors.NewNotFound("entity_revision", id.String())
	}
	if err != nil {
		return revision.Revision{}, fmt.Errorf("get entity revision: %w", err)
	}
	return row.toRevision()
}

func (s *revisionStore) List(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) ([]revision.Revision, error) {
	var rows []revisionRow
	if err := s.q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, type_definition_id, entity_id, seq, label, created_at, values
		 FROM flexitype_entity_revision
		 WHERE tenant_id = ? AND type_definition_id = ? AND entity_id = ?
		 ORDER BY seq DESC`), tenant.String(), typeDefID, entityID); err != nil {
		return nil, fmt.Errorf("list entity revisions: %w", err)
	}
	out := make([]revision.Revision, 0, len(rows))
	for _, row := range rows {
		rev, err := row.toRevision()
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, nil
}

func (s *revisionStore) AsOf(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string, at time.Time) (revision.Revision, error) {
	var row revisionRow
	err := s.q.GetContext(ctx, &row, bind(
		`SELECT id, tenant_id, type_definition_id, entity_id, seq, label, created_at, values
		 FROM flexitype_entity_revision
		 WHERE tenant_id = ? AND type_definition_id = ? AND entity_id = ? AND created_at <= ?
		 ORDER BY seq DESC LIMIT 1`), tenant.String(), typeDefID, entityID, at)
	if isNoRows(err) {
		return revision.Revision{}, domainerrors.NewNotFound("entity_revision", "as-of "+at.Format(time.RFC3339))
	}
	if err != nil {
		return revision.Revision{}, fmt.Errorf("get entity revision as-of: %w", err)
	}
	return row.toRevision()
}

func (s *revisionStore) PurgeEntity(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error) {
	res, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_revision
		 WHERE tenant_id = ? AND type_definition_id = ? AND entity_id = ?`),
		tenant.String(), typeDefID, entityID)
	if err != nil {
		return 0, fmt.Errorf("purge entity revisions: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *revisionStore) PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	res, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_entity_revision WHERE tenant_id = ?`), tenant.String())
	if err != nil {
		return 0, fmt.Errorf("purge tenant revisions: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *revisionStore) LastSeq(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error) {
	var seq int
	err := s.q.GetContext(ctx, &seq, bind(
		`SELECT COALESCE(MAX(seq), 0) FROM flexitype_entity_revision
		 WHERE tenant_id = ? AND type_definition_id = ? AND entity_id = ?`),
		tenant.String(), typeDefID, entityID)
	if err != nil {
		return 0, fmt.Errorf("last revision seq: %w", err)
	}
	return seq, nil
}
