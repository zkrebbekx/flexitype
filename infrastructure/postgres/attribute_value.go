package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/dataloader"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

const valueColumnList = `id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
	data_type, value_bool, value_int, value_float, value_text, value_time, value_json,
	definition_version, created_at, updated_at, archived_at`

type valueRow struct {
	ID                    ulid.ID `db:"id"`
	TenantID              string  `db:"tenant_id"`
	TypeDefinitionID      ulid.ID `db:"type_definition_id"`
	AttributeDefinitionID ulid.ID `db:"attribute_definition_id"`
	EntityID              string  `db:"entity_id"`
	DataType              string  `db:"data_type"`
	valueColumns
	DefinitionVersion int          `db:"definition_version"`
	CreatedAt         time.Time    `db:"created_at"`
	UpdatedAt         time.Time    `db:"updated_at"`
	ArchivedAt        sql.NullTime `db:"archived_at"`
}

func (r valueRow) snapshot() (domainvalue.Snapshot, error) {
	v, err := valueFromColumns(valueobjects.DataType(r.DataType), r.valueColumns)
	if err != nil {
		return domainvalue.Snapshot{}, fmt.Errorf("decode value %s: %w", r.ID, err)
	}
	return domainvalue.Snapshot{
		ID:                    valueobjects.AttributeValueID{ID: r.ID},
		TenantID:              valueobjects.TenantID(r.TenantID),
		TypeDefinitionID:      valueobjects.TypeDefinitionID{ID: r.TypeDefinitionID},
		AttributeDefinitionID: valueobjects.AttributeDefinitionID{ID: r.AttributeDefinitionID},
		EntityID:              valueobjects.EntityID(r.EntityID),
		Value:                 v,
		DefinitionVersion:     r.DefinitionVersion,
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
		ArchivedAt:            timePtr(r.ArchivedAt),
	}, nil
}

// entityLoaderKey is the comparable projection of domainvalue.EntityKey.
type entityLoaderKey struct {
	Tenant    string
	TypeDefID string
	EntityID  string
}

// valueListKey batches List queries.
type valueListKey struct {
	Tenant          string
	TypeDefID       string
	AttributeDefID  string
	EntityID        string
	IncludeArchived bool
	Limit           int
	Offset          int
}

type attributeValueRepository struct {
	q        db.QueryExecer
	inTx     bool
	byID     *dataloader.Loader[string, domainvalue.Snapshot]
	byEntity *dataloader.Loader[entityLoaderKey, []domainvalue.Snapshot]
	byDef    *dataloader.Loader[dataloader.PageKey[string], dataloader.PagedResult[domainvalue.Snapshot]]
	byList   *dataloader.Loader[valueListKey, dataloader.PagedResult[domainvalue.Snapshot]]
}

// NewAttributeValueRepository builds a dataloader-backed repository over
// the pool.
func NewAttributeValueRepository(q db.QueryExecer) domainvalue.Repository {
	r := &attributeValueRepository{q: q}
	r.byID = dataloader.NewZeroLoader(r.batchByID, loaderConfig())
	r.byEntity = dataloader.NewSliceLoader(r.batchByEntity, loaderConfig())
	r.byDef = dataloader.NewSliceLoader(r.batchByDefinitionPage, loaderConfig())
	r.byList = dataloader.NewSliceLoader(r.batchList, loaderConfig())
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *attributeValueRepository) WithTx(tx db.QueryExecer) domainvalue.Repository {
	return &attributeValueRepository{q: tx, inTx: true}
}

func (r *attributeValueRepository) batchByID(ctx context.Context, ids []string) (map[string]domainvalue.Snapshot, error) {
	var rows []valueRow
	query := fmt.Sprintf(`SELECT %s FROM flexitype_attribute_value WHERE id = ANY($1)`, valueColumnList)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("batch values by id: %w", err)
	}
	out := make(map[string]domainvalue.Snapshot, len(rows))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		out[row.ID.String()] = snap
	}
	return out, nil
}

// batchByEntity collapses entity hydrations into one tuple-IN query.
func (r *attributeValueRepository) batchByEntity(ctx context.Context, keys []entityLoaderKey) (map[entityLoaderKey][]domainvalue.Snapshot, error) {
	args := make([]any, 0, len(keys)*3)
	tuples := make([]string, 0, len(keys))
	for i, k := range keys {
		tuples = append(tuples, fmt.Sprintf("($%d, $%d, $%d)", i*3+1, i*3+2, i*3+3))
		args = append(args, k.Tenant, k.TypeDefID, k.EntityID)
	}
	query := fmt.Sprintf(
		`SELECT %s FROM flexitype_attribute_value
		 WHERE archived_at IS NULL
		   AND (tenant_id, type_definition_id, entity_id) IN (%s)
		 ORDER BY id`,
		valueColumnList, strings.Join(tuples, ", "),
	)

	var rows []valueRow
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("batch values by entity: %w", err)
	}
	out := make(map[entityLoaderKey][]domainvalue.Snapshot, len(keys))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		k := entityLoaderKey{Tenant: row.TenantID, TypeDefID: row.TypeDefinitionID.String(), EntityID: row.EntityID}
		out[k] = append(out[k], snap)
	}
	return out, nil
}

// batchByDefinitionPage collapses per-definition value pages into one
// windowed query per (limit, offset) group.
func (r *attributeValueRepository) batchByDefinitionPage(ctx context.Context, keys []dataloader.PageKey[string]) (map[dataloader.PageKey[string]]dataloader.PagedResult[domainvalue.Snapshot], error) {
	out := make(map[dataloader.PageKey[string]]dataloader.PagedResult[domainvalue.Snapshot], len(keys))

	for group, parents := range pageKeyGroups(keys) {
		limit, offset := group[0], group[1]
		query := fmt.Sprintf(
			`SELECT * FROM (
			   SELECT %s,
			          count(*)     OVER (PARTITION BY attribute_definition_id) AS total_count,
			          row_number() OVER (PARTITION BY attribute_definition_id ORDER BY id) AS rn
			   FROM flexitype_attribute_value
			   WHERE attribute_definition_id = ANY($1) AND archived_at IS NULL
			 ) w
			 WHERE rn > $2 AND rn <= $3
			 ORDER BY attribute_definition_id, rn`,
			valueColumnList,
		)

		var rows []struct {
			valueRow
			TotalCount int `db:"total_count"`
			RN         int `db:"rn"`
		}
		if err := r.q.SelectContext(ctx, &rows, query, pq.Array(parents), offset, offset+limit); err != nil {
			return nil, fmt.Errorf("batch values by definition: %w", err)
		}

		results := make(map[string]dataloader.PagedResult[domainvalue.Snapshot], len(parents))
		for _, row := range rows {
			snap, err := row.snapshot()
			if err != nil {
				return nil, err
			}
			parent := row.AttributeDefinitionID.String()
			pr := results[parent]
			pr.Items = append(pr.Items, snap)
			pr.Total = row.TotalCount
			results[parent] = pr
		}
		for _, parent := range parents {
			out[dataloader.PageKey[string]{Parent: parent, Limit: limit, Offset: offset}] = results[parent]
		}
	}
	return out, nil
}

func (r *attributeValueRepository) batchList(ctx context.Context, keys []valueListKey) (map[valueListKey]dataloader.PagedResult[domainvalue.Snapshot], error) {
	out := make(map[valueListKey]dataloader.PagedResult[domainvalue.Snapshot], len(keys))
	for _, key := range keys {
		items, total, err := r.queryList(ctx, key)
		if err != nil {
			return nil, err
		}
		out[key] = dataloader.PagedResult[domainvalue.Snapshot]{Items: items, Total: total}
	}
	return out, nil
}

func (r *attributeValueRepository) queryList(ctx context.Context, key valueListKey) ([]domainvalue.Snapshot, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{key.Tenant}
	if !key.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if key.TypeDefID != "" {
		args = append(args, key.TypeDefID)
		where = append(where, fmt.Sprintf("type_definition_id = $%d", len(args)))
	}
	if key.AttributeDefID != "" {
		args = append(args, key.AttributeDefID)
		where = append(where, fmt.Sprintf("attribute_definition_id = $%d", len(args)))
	}
	if key.EntityID != "" {
		args = append(args, key.EntityID)
		where = append(where, fmt.Sprintf("entity_id = $%d", len(args)))
	}
	args = append(args, key.Limit, key.Offset)

	query := fmt.Sprintf(
		`SELECT %s, count(*) OVER () AS total_count
		 FROM flexitype_attribute_value
		 WHERE %s
		 ORDER BY id
		 LIMIT $%d OFFSET $%d`,
		valueColumnList, strings.Join(where, " AND "), len(args)-1, len(args),
	)

	var rows []struct {
		valueRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, fmt.Errorf("list values: %w", err)
	}

	items := make([]domainvalue.Snapshot, 0, len(rows))
	total := 0
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, 0, err
		}
		items = append(items, snap)
		total = row.TotalCount
	}
	return items, total, nil
}

func (r *attributeValueRepository) Get(ctx context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := r.byID.Load(ctx, id.String())
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domainvalue.AggregateType, id.String())
	}
	return domainvalue.Rehydrate(snap), nil
}

func (r *attributeValueRepository) GetForUpdate(ctx context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	if !r.inTx {
		return nil, fmt.Errorf("attribute value repository: GetForUpdate requires a transaction")
	}
	return r.getDirect(ctx, id, true)
}

func (r *attributeValueRepository) getDirect(ctx context.Context, id valueobjects.AttributeValueID, forUpdate bool) (*domainvalue.AttributeValue, error) {
	query := fmt.Sprintf(`SELECT %s FROM flexitype_attribute_value WHERE id = $1`, valueColumnList)
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row valueRow
	if err := r.q.GetContext(ctx, &row, query, id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domainvalue.AggregateType, id.String())
		}
		return nil, fmt.Errorf("get value: %w", err)
	}
	snap, err := row.snapshot()
	if err != nil {
		return nil, err
	}
	return domainvalue.Rehydrate(snap), nil
}

func (r *attributeValueRepository) ListByEntity(ctx context.Context, key domainvalue.EntityKey) ([]*domainvalue.AttributeValue, error) {
	loaderKey := entityLoaderKey{
		Tenant:    key.TenantID.String(),
		TypeDefID: key.TypeDefinitionID.String(),
		EntityID:  key.EntityID.String(),
	}

	var snaps []domainvalue.Snapshot
	var err error
	if r.inTx {
		fetched, ferr := r.batchByEntity(ctx, []entityLoaderKey{loaderKey})
		if ferr != nil {
			return nil, ferr
		}
		snaps = fetched[loaderKey]
	} else {
		snaps, err = r.byEntity.Load(ctx, loaderKey)
		if err != nil {
			return nil, err
		}
	}

	out := make([]*domainvalue.AttributeValue, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, domainvalue.Rehydrate(snap))
	}
	return out, nil
}

func (r *attributeValueRepository) ListByDefinition(ctx context.Context, defID valueobjects.AttributeDefinitionID, page db.Page) ([]*domainvalue.AttributeValue, int, error) {
	key := dataloader.PageKey[string]{Parent: defID.String(), Limit: page.Limit, Offset: page.Offset}

	var result dataloader.PagedResult[domainvalue.Snapshot]
	if r.inTx {
		fetched, err := r.batchByDefinitionPage(ctx, []dataloader.PageKey[string]{key})
		if err != nil {
			return nil, 0, err
		}
		result = fetched[key]
	} else {
		var err error
		result, err = r.byDef.Load(ctx, key)
		if err != nil {
			return nil, 0, err
		}
	}

	out := make([]*domainvalue.AttributeValue, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domainvalue.Rehydrate(snap))
	}
	return out, result.Total, nil
}

func (r *attributeValueRepository) FindByDefinitionAndEntity(ctx context.Context, defID valueobjects.AttributeDefinitionID, entityID valueobjects.EntityID) ([]*domainvalue.AttributeValue, error) {
	query := fmt.Sprintf(
		`SELECT %s FROM flexitype_attribute_value
		 WHERE attribute_definition_id = $1 AND entity_id = $2 AND archived_at IS NULL
		 ORDER BY id`,
		valueColumnList,
	)
	var rows []valueRow
	if err := r.q.SelectContext(ctx, &rows, query, defID.String(), entityID.String()); err != nil {
		return nil, fmt.Errorf("find values by definition and entity: %w", err)
	}

	out := make([]*domainvalue.AttributeValue, 0, len(rows))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		out = append(out, domainvalue.Rehydrate(snap))
	}
	return out, nil
}

func (r *attributeValueRepository) CountByDefinitionAndValue(ctx context.Context, defID valueobjects.AttributeDefinitionID, v valueobjects.Value, excludeEntity valueobjects.EntityID) (int, error) {
	column := valueColumnName(v.DataType())
	query := fmt.Sprintf(
		`SELECT count(*) FROM flexitype_attribute_value
		 WHERE attribute_definition_id = $1 AND %s = $2 AND entity_id <> $3 AND archived_at IS NULL`,
		column,
	)
	var count int
	if err := r.q.GetContext(ctx, &count, query, defID.String(), valueArg(v), excludeEntity.String()); err != nil {
		return 0, fmt.Errorf("count values by definition and value: %w", err)
	}
	return count, nil
}

func (r *attributeValueRepository) List(ctx context.Context, filter domainvalue.Filter, page db.Page) ([]*domainvalue.AttributeValue, int, error) {
	key := valueListKey{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if !filter.TypeDefinitionID.IsZero() {
		key.TypeDefID = filter.TypeDefinitionID.String()
	}
	if !filter.AttributeDefinitionID.IsZero() {
		key.AttributeDefID = filter.AttributeDefinitionID.String()
	}
	if !filter.EntityID.IsZero() {
		key.EntityID = filter.EntityID.String()
	}

	var result dataloader.PagedResult[domainvalue.Snapshot]
	var err error
	if r.inTx {
		var items []domainvalue.Snapshot
		var total int
		items, total, err = r.queryList(ctx, key)
		result = dataloader.PagedResult[domainvalue.Snapshot]{Items: items, Total: total}
	} else {
		result, err = r.byList.Load(ctx, key)
	}
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domainvalue.AttributeValue, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domainvalue.Rehydrate(snap))
	}
	return out, result.Total, nil
}

func (r *attributeValueRepository) Save(ctx context.Context, av *domainvalue.AttributeValue) error {
	s := av.Snapshot()
	cols := columnsFromValue(s.Value)

	_, err := r.q.ExecContext(ctx,
		`INSERT INTO flexitype_attribute_value
		   (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
		    value_bool, value_int, value_float, value_text, value_time, value_json,
		    definition_version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		 ON CONFLICT (id) DO UPDATE SET
		   data_type          = EXCLUDED.data_type,
		   value_bool         = EXCLUDED.value_bool,
		   value_int          = EXCLUDED.value_int,
		   value_float        = EXCLUDED.value_float,
		   value_text         = EXCLUDED.value_text,
		   value_time         = EXCLUDED.value_time,
		   value_json         = EXCLUDED.value_json,
		   definition_version = EXCLUDED.definition_version,
		   updated_at         = EXCLUDED.updated_at,
		   archived_at        = EXCLUDED.archived_at`,
		s.ID.String(), s.TenantID.String(), s.TypeDefinitionID.String(),
		s.AttributeDefinitionID.String(), s.EntityID.String(), s.Value.DataType().String(),
		cols.Bool, cols.Int, cols.Float, cols.Text, cols.Time, jsonbParam(cols.JSON),
		s.DefinitionVersion, s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save value: %w", err)
	}
	return nil
}
