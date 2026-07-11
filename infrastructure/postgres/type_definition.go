package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/dataloader"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

const typeDefColumns = `id, tenant_id, internal_name, display_name, description, version, created_at, updated_at, archived_at`

type typeDefRow struct {
	ID           ulid.ID      `db:"id"`
	TenantID     string       `db:"tenant_id"`
	InternalName string       `db:"internal_name"`
	DisplayName  string       `db:"display_name"`
	Description  string       `db:"description"`
	Version      int          `db:"version"`
	CreatedAt    time.Time    `db:"created_at"`
	UpdatedAt    time.Time    `db:"updated_at"`
	ArchivedAt   sql.NullTime `db:"archived_at"`
}

func (r typeDefRow) snapshot() domaintypedef.Snapshot {
	return domaintypedef.Snapshot{
		ID:           valueobjects.TypeDefinitionID{ID: r.ID},
		TenantID:     valueobjects.TenantID(r.TenantID),
		InternalName: r.InternalName,
		DisplayName:  r.DisplayName,
		Description:  r.Description,
		Version:      r.Version,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		ArchivedAt:   timePtr(r.ArchivedAt),
	}
}

// nameKey batches GetByInternalName lookups.
type nameKey struct {
	Tenant string
	Name   string
}

// typeDefListKey batches List queries; InternalNames is the sorted,
// NUL-joined name filter so equal filters collide in the loader cache.
type typeDefListKey struct {
	Tenant          string
	InternalNames   string
	IncludeArchived bool
	Limit           int
	Offset          int
}

type typeDefinitionRepository struct {
	q      db.QueryExecer
	inTx   bool
	byID   *dataloader.Loader[string, domaintypedef.Snapshot]
	byName *dataloader.Loader[nameKey, domaintypedef.Snapshot]
	byList *dataloader.Loader[typeDefListKey, dataloader.PagedResult[domaintypedef.Snapshot]]
}

// NewTypeDefinitionRepository builds a dataloader-backed repository over
// the pool.
func NewTypeDefinitionRepository(q db.QueryExecer) domaintypedef.Repository {
	r := &typeDefinitionRepository{q: q}
	r.byID = dataloader.NewZeroLoader(r.batchByID, loaderConfig())
	r.byName = dataloader.NewZeroLoader(r.batchByName, loaderConfig())
	r.byList = dataloader.NewSliceLoader(r.batchList, loaderConfig())
	return r
}

// WithTx binds the repository to a transaction. Loader caches are bypassed
// so reads observe uncommitted writes.
func (r *typeDefinitionRepository) WithTx(tx db.QueryExecer) domaintypedef.Repository {
	return &typeDefinitionRepository{q: tx, inTx: true}
}

func (r *typeDefinitionRepository) batchByID(ctx context.Context, ids []string) (map[string]domaintypedef.Snapshot, error) {
	var rows []typeDefRow
	query := fmt.Sprintf(`SELECT %s FROM flexitype_type_definition WHERE id = ANY($1)`, typeDefColumns)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("batch type definitions by id: %w", err)
	}
	out := make(map[string]domaintypedef.Snapshot, len(rows))
	for _, row := range rows {
		out[row.ID.String()] = row.snapshot()
	}
	return out, nil
}

func (r *typeDefinitionRepository) batchByName(ctx context.Context, keys []nameKey) (map[nameKey]domaintypedef.Snapshot, error) {
	args := make([]any, 0, len(keys)*2)
	tuples := make([]string, 0, len(keys))
	for i, k := range keys {
		tuples = append(tuples, fmt.Sprintf("($%d, $%d)", i*2+1, i*2+2))
		args = append(args, k.Tenant, k.Name)
	}
	query := fmt.Sprintf(
		`SELECT %s FROM flexitype_type_definition
		 WHERE archived_at IS NULL AND (tenant_id, internal_name) IN (%s)`,
		typeDefColumns, strings.Join(tuples, ", "),
	)

	var rows []typeDefRow
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("batch type definitions by name: %w", err)
	}
	out := make(map[nameKey]domaintypedef.Snapshot, len(rows))
	for _, row := range rows {
		out[nameKey{Tenant: row.TenantID, Name: row.InternalName}] = row.snapshot()
	}
	return out, nil
}

func (r *typeDefinitionRepository) batchList(ctx context.Context, keys []typeDefListKey) (map[typeDefListKey]dataloader.PagedResult[domaintypedef.Snapshot], error) {
	out := make(map[typeDefListKey]dataloader.PagedResult[domaintypedef.Snapshot], len(keys))
	for _, key := range keys {
		items, total, err := r.queryList(ctx, key)
		if err != nil {
			return nil, err
		}
		out[key] = dataloader.PagedResult[domaintypedef.Snapshot]{Items: items, Total: total}
	}
	return out, nil
}

func (r *typeDefinitionRepository) queryList(ctx context.Context, key typeDefListKey) ([]domaintypedef.Snapshot, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{key.Tenant}
	if !key.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if key.InternalNames != "" {
		args = append(args, pq.Array(strings.Split(key.InternalNames, "\x00")))
		where = append(where, fmt.Sprintf("internal_name = ANY($%d)", len(args)))
	}
	args = append(args, key.Limit, key.Offset)

	query := fmt.Sprintf(
		`SELECT %s, count(*) OVER () AS total_count
		 FROM flexitype_type_definition
		 WHERE %s
		 ORDER BY id
		 LIMIT $%d OFFSET $%d`,
		typeDefColumns, strings.Join(where, " AND "), len(args)-1, len(args),
	)

	var rows []struct {
		typeDefRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, fmt.Errorf("list type definitions: %w", err)
	}

	items := make([]domaintypedef.Snapshot, 0, len(rows))
	total := 0
	for _, row := range rows {
		items = append(items, row.snapshot())
		total = row.TotalCount
	}
	return items, total, nil
}

func (r *typeDefinitionRepository) Get(ctx context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := r.byID.Load(ctx, id.String())
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, id.String())
	}
	return domaintypedef.Rehydrate(snap), nil
}

func (r *typeDefinitionRepository) GetForUpdate(ctx context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	if !r.inTx {
		return nil, fmt.Errorf("type definition repository: GetForUpdate requires a transaction")
	}
	return r.getDirect(ctx, id, true)
}

func (r *typeDefinitionRepository) getDirect(ctx context.Context, id valueobjects.TypeDefinitionID, forUpdate bool) (*domaintypedef.TypeDefinition, error) {
	query := fmt.Sprintf(`SELECT %s FROM flexitype_type_definition WHERE id = $1`, typeDefColumns)
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row typeDefRow
	if err := r.q.GetContext(ctx, &row, query, id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, id.String())
		}
		return nil, fmt.Errorf("get type definition: %w", err)
	}
	return domaintypedef.Rehydrate(row.snapshot()), nil
}

func (r *typeDefinitionRepository) GetByInternalName(ctx context.Context, tenant valueobjects.TenantID, internalName string) (*domaintypedef.TypeDefinition, error) {
	if r.inTx {
		query := fmt.Sprintf(
			`SELECT %s FROM flexitype_type_definition
			 WHERE tenant_id = $1 AND internal_name = $2 AND archived_at IS NULL`,
			typeDefColumns,
		)
		var row typeDefRow
		if err := r.q.GetContext(ctx, &row, query, tenant.String(), internalName); err != nil {
			if isNoRows(err) {
				return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, internalName)
			}
			return nil, fmt.Errorf("get type definition by name: %w", err)
		}
		return domaintypedef.Rehydrate(row.snapshot()), nil
	}

	snap, err := r.byName.Load(ctx, nameKey{Tenant: tenant.String(), Name: internalName})
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, internalName)
	}
	return domaintypedef.Rehydrate(snap), nil
}

func (r *typeDefinitionRepository) List(ctx context.Context, filter domaintypedef.Filter, page db.Page) ([]*domaintypedef.TypeDefinition, int, error) {
	key := typeDefListKey{
		Tenant:          filter.TenantID.String(),
		InternalNames:   joinSorted(filter.InternalNames),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}

	var result dataloader.PagedResult[domaintypedef.Snapshot]
	var err error
	if r.inTx {
		var items []domaintypedef.Snapshot
		var total int
		items, total, err = r.queryList(ctx, key)
		result = dataloader.PagedResult[domaintypedef.Snapshot]{Items: items, Total: total}
	} else {
		result, err = r.byList.Load(ctx, key)
	}
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domaintypedef.TypeDefinition, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domaintypedef.Rehydrate(snap))
	}
	return out, result.Total, nil
}

func (r *typeDefinitionRepository) Save(ctx context.Context, t *domaintypedef.TypeDefinition) error {
	s := t.Snapshot()
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO flexitype_type_definition
		   (id, tenant_id, internal_name, display_name, description, version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (id) DO UPDATE SET
		   display_name = EXCLUDED.display_name,
		   description  = EXCLUDED.description,
		   version      = EXCLUDED.version,
		   updated_at   = EXCLUDED.updated_at,
		   archived_at  = EXCLUDED.archived_at`,
		s.ID.String(), s.TenantID.String(), s.InternalName, s.DisplayName, s.Description,
		s.Version, s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save type definition: %w", err)
	}
	return nil
}
