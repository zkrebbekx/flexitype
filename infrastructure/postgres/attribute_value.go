package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/graph-gophers/dataloader/v7"
	"github.com/lib/pq"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
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

// valueListFilter is the cleansed JSON dataloader key for value List
// queries; unique keys become UNION ALL arms.
type valueListFilter struct {
	Tenant          string `json:"tenant"`
	TypeDefID       string `json:"type_definition_id,omitempty"`
	AttributeDefID  string `json:"attribute_definition_id,omitempty"`
	EntityID        string `json:"entity_id,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit"`
	Offset          int    `json:"offset"`
}

func (f valueListFilter) key() string {
	b, _ := json.Marshal(f)
	return string(b)
}

func (f valueListFilter) arm(key string) (string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{key, f.Tenant}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if f.TypeDefID != "" {
		where = append(where, "type_definition_id = ?")
		args = append(args, f.TypeDefID)
	}
	if f.AttributeDefID != "" {
		where = append(where, "attribute_definition_id = ?")
		args = append(args, f.AttributeDefID)
	}
	if f.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, f.EntityID)
	}
	args = append(args, f.Limit, f.Offset)

	query := `(SELECT ?::text AS loader_key, ` + valueColumnList + `, count(*) OVER () AS total_count
	 FROM flexitype_attribute_value
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ? OFFSET ?)`
	return query, args
}

type attributeValueRepository struct {
	q        db.QueryExecer
	inTx     bool
	byID     *dataloader.Loader[string, domainvalue.Snapshot]
	byEntity *dataloader.Loader[entityLoaderKey, []domainvalue.Snapshot]
	byDef    *dataloader.Loader[pageKey, pagedResult[domainvalue.Snapshot]]
	byList   *dataloader.Loader[string, pagedResult[domainvalue.Snapshot]]
}

// NewAttributeValueRepository builds a dataloader-backed repository over
// the pool.
func NewAttributeValueRepository(q db.QueryExecer) domainvalue.Repository {
	r := &attributeValueRepository{q: q}
	r.byID = newLoader(r.batchByID)
	r.byEntity = newLoader(r.batchByEntity)
	r.byDef = newLoader(r.batchByDefinitionPage)
	r.byList = newLoader(r.batchList)
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *attributeValueRepository) WithTx(tx db.QueryExecer) domainvalue.Repository {
	return &attributeValueRepository{q: tx, inTx: true}
}

func (r *attributeValueRepository) batchByID(ctx context.Context, ids []string) (map[string]domainvalue.Snapshot, error) {
	var rows []valueRow
	query := bind(`SELECT ` + valueColumnList + ` FROM flexitype_attribute_value WHERE id = ANY(?)`)
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
	tuples := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*3)
	for _, k := range keys {
		tuples = append(tuples, "(?, ?, ?)")
		args = append(args, k.Tenant, k.TypeDefID, k.EntityID)
	}
	query := bind(`SELECT ` + valueColumnList + ` FROM flexitype_attribute_value
	 WHERE archived_at IS NULL
	   AND (tenant_id, type_definition_id, entity_id) IN (` + strings.Join(tuples, ", ") + `)
	 ORDER BY id`)

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
func (r *attributeValueRepository) batchByDefinitionPage(ctx context.Context, keys []pageKey) (map[pageKey]pagedResult[domainvalue.Snapshot], error) {
	out := make(map[pageKey]pagedResult[domainvalue.Snapshot], len(keys))

	for group, parents := range pageKeyGroups(keys) {
		limit, offset := group[0], group[1]
		query := bind(`SELECT * FROM (
		   SELECT ` + valueColumnList + `,
		          count(*)     OVER (PARTITION BY attribute_definition_id) AS total_count,
		          row_number() OVER (PARTITION BY attribute_definition_id ORDER BY id) AS rn
		   FROM flexitype_attribute_value
		   WHERE attribute_definition_id = ANY(?) AND archived_at IS NULL
		 ) w
		 WHERE rn > ? AND rn <= ?
		 ORDER BY attribute_definition_id, rn`)

		var rows []struct {
			valueRow
			TotalCount int `db:"total_count"`
			RN         int `db:"rn"`
		}
		if err := r.q.SelectContext(ctx, &rows, query, pq.Array(parents), offset, offset+limit); err != nil {
			return nil, fmt.Errorf("batch values by definition: %w", err)
		}

		results := make(map[string]pagedResult[domainvalue.Snapshot], len(parents))
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
			out[pageKey{Parent: parent, Limit: limit, Offset: offset}] = results[parent]
		}
	}
	return out, nil
}

// batchList runs every unique filter key as one UNION ALL statement.
func (r *attributeValueRepository) batchList(ctx context.Context, keys []string) (map[string]pagedResult[domainvalue.Snapshot], error) {
	arms := make([]string, 0, len(keys))
	var args []any
	for _, key := range keys {
		var f valueListFilter
		if err := json.Unmarshal([]byte(key), &f); err != nil {
			return nil, fmt.Errorf("decode list key: %w", err)
		}
		arm, armArgs := f.arm(key)
		arms = append(arms, arm)
		args = append(args, armArgs...)
	}

	var rows []struct {
		LoaderKey string `db:"loader_key"`
		valueRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, bind(strings.Join(arms, "\nUNION ALL\n")), args...); err != nil {
		return nil, fmt.Errorf("batch list values: %w", err)
	}

	out := make(map[string]pagedResult[domainvalue.Snapshot], len(keys))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		pr := out[row.LoaderKey]
		pr.Items = append(pr.Items, snap)
		pr.Total = row.TotalCount
		out[row.LoaderKey] = pr
	}
	return out, nil
}

func (r *attributeValueRepository) Get(ctx context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := load(ctx, r.byID, id.String())
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
	query := `SELECT ` + valueColumnList + ` FROM flexitype_attribute_value WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row valueRow
	if err := r.q.GetContext(ctx, &row, bind(query), id.String()); err != nil {
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
	if r.inTx {
		fetched, err := r.batchByEntity(ctx, []entityLoaderKey{loaderKey})
		if err != nil {
			return nil, err
		}
		snaps = fetched[loaderKey]
	} else {
		var err error
		snaps, err = load(ctx, r.byEntity, loaderKey)
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
	key := pageKey{Parent: defID.String(), Limit: page.Limit, Offset: page.Offset}

	var result pagedResult[domainvalue.Snapshot]
	if r.inTx {
		fetched, err := r.batchByDefinitionPage(ctx, []pageKey{key})
		if err != nil {
			return nil, 0, err
		}
		result = fetched[key]
	} else {
		var err error
		result, err = load(ctx, r.byDef, key)
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
	query := bind(`SELECT ` + valueColumnList + ` FROM flexitype_attribute_value
	 WHERE attribute_definition_id = ? AND entity_id = ? AND archived_at IS NULL
	 ORDER BY id`)
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
	// The value column is an identifier chosen by data type; arguments stay
	// bound placeholders.
	query := bind(`SELECT count(*) FROM flexitype_attribute_value
	 WHERE attribute_definition_id = ? AND ` + valueColumnName(v.DataType()) + ` = ? AND entity_id <> ? AND archived_at IS NULL`)
	var count int
	if err := r.q.GetContext(ctx, &count, query, defID.String(), valueArg(v), excludeEntity.String()); err != nil {
		return 0, fmt.Errorf("count values by definition and value: %w", err)
	}
	return count, nil
}

func (r *attributeValueRepository) List(ctx context.Context, filter domainvalue.Filter, page db.Page) ([]*domainvalue.AttributeValue, int, error) {
	f := valueListFilter{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if !filter.TypeDefinitionID.IsZero() {
		f.TypeDefID = filter.TypeDefinitionID.String()
	}
	if !filter.AttributeDefinitionID.IsZero() {
		f.AttributeDefID = filter.AttributeDefinitionID.String()
	}
	if !filter.EntityID.IsZero() {
		f.EntityID = filter.EntityID.String()
	}
	key := f.key()

	var result pagedResult[domainvalue.Snapshot]
	var err error
	if r.inTx {
		fetched, ferr := r.batchList(ctx, []string{key})
		if ferr != nil {
			return nil, 0, ferr
		}
		result = fetched[key]
	} else {
		result, err = load(ctx, r.byList, key)
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

func (r *attributeValueRepository) ListEntities(ctx context.Context, tenant valueobjects.TenantID, typeDefID valueobjects.TypeDefinitionID, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	var rows []struct {
		EntityID      string    `db:"entity_id"`
		ValueCount    int       `db:"value_count"`
		LastUpdatedAt time.Time `db:"last_updated_at"`
		TotalCount    int       `db:"total_count"`
	}
	err := r.q.SelectContext(ctx, &rows, bind(
		`SELECT entity_id,
		        count(*)        AS value_count,
		        max(updated_at) AS last_updated_at,
		        count(*) OVER () AS total_count
		 FROM flexitype_attribute_value
		 WHERE tenant_id = ? AND type_definition_id = ? AND archived_at IS NULL
		 GROUP BY entity_id
		 ORDER BY max(updated_at) DESC, entity_id
		 LIMIT ? OFFSET ?`),
		tenant.String(), typeDefID.String(), page.Limit, page.Offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}

	out := make([]domainvalue.EntitySummary, 0, len(rows))
	total := 0
	for _, row := range rows {
		out = append(out, domainvalue.EntitySummary{
			EntityID:      valueobjects.EntityID(row.EntityID),
			ValueCount:    row.ValueCount,
			LastUpdatedAt: row.LastUpdatedAt,
		})
		total = row.TotalCount
	}
	return out, total, nil
}

func (r *attributeValueRepository) Save(ctx context.Context, av *domainvalue.AttributeValue) error {
	s := av.Snapshot()
	cols := columnsFromValue(s.Value)

	_, err := r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_attribute_value
		   (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
		    value_bool, value_int, value_float, value_text, value_time, value_json,
		    definition_version, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		   archived_at        = EXCLUDED.archived_at`),
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
