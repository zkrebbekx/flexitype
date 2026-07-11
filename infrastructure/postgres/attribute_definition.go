package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/dataloader"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

const attrColumns = `id, tenant_id, type_definition_id, internal_name, display_name, description,
	data_type, required, multi_valued, is_unique, constraints, default_value, version,
	created_at, updated_at, archived_at`

type attrRow struct {
	ID               ulid.ID      `db:"id"`
	TenantID         string       `db:"tenant_id"`
	TypeDefinitionID ulid.ID      `db:"type_definition_id"`
	InternalName     string       `db:"internal_name"`
	DisplayName      string       `db:"display_name"`
	Description      string       `db:"description"`
	DataType         string       `db:"data_type"`
	Required         bool         `db:"required"`
	MultiValued      bool         `db:"multi_valued"`
	IsUnique         bool         `db:"is_unique"`
	Constraints      []byte       `db:"constraints"`
	DefaultValue     []byte       `db:"default_value"`
	Version          int          `db:"version"`
	CreatedAt        time.Time    `db:"created_at"`
	UpdatedAt        time.Time    `db:"updated_at"`
	ArchivedAt       sql.NullTime `db:"archived_at"`
}

func (r attrRow) snapshot() (domainattribute.Snapshot, error) {
	var constraints domainattribute.Constraints
	if len(r.Constraints) > 0 {
		if err := json.Unmarshal(r.Constraints, &constraints); err != nil {
			return domainattribute.Snapshot{}, fmt.Errorf("decode constraints for %s: %w", r.ID, err)
		}
	}
	var defaultValue *valueobjects.Default
	if len(r.DefaultValue) > 0 && string(r.DefaultValue) != "null" {
		var d valueobjects.Default
		if err := json.Unmarshal(r.DefaultValue, &d); err != nil {
			return domainattribute.Snapshot{}, fmt.Errorf("decode default value for %s: %w", r.ID, err)
		}
		defaultValue = &d
	}
	return domainattribute.Snapshot{
		ID:               valueobjects.AttributeDefinitionID{ID: r.ID},
		TenantID:         valueobjects.TenantID(r.TenantID),
		TypeDefinitionID: valueobjects.TypeDefinitionID{ID: r.TypeDefinitionID},
		InternalName:     r.InternalName,
		DisplayName:      r.DisplayName,
		Description:      r.Description,
		DataType:         valueobjects.DataType(r.DataType),
		Required:         r.Required,
		MultiValued:      r.MultiValued,
		Unique:           r.IsUnique,
		Constraints:      constraints,
		DefaultValue:     defaultValue,
		Version:          r.Version,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
		ArchivedAt:       timePtr(r.ArchivedAt),
	}, nil
}

// attrListKey batches List queries.
type attrListKey struct {
	Tenant           string
	TypeDefinitionID string
	InternalNames    string
	DataTypes        string
	IncludeArchived  bool
	Limit            int
	Offset           int
}

type attributeDefinitionRepository struct {
	q          db.QueryExecer
	inTx       bool
	byID       *dataloader.Loader[string, domainattribute.Snapshot]
	byName     *dataloader.Loader[nameKey, domainattribute.Snapshot]
	byTypePage *dataloader.Loader[dataloader.PageKey[string], dataloader.PagedResult[domainattribute.Snapshot]]
	byList     *dataloader.Loader[attrListKey, dataloader.PagedResult[domainattribute.Snapshot]]
}

// NewAttributeDefinitionRepository builds a dataloader-backed repository
// over the pool.
func NewAttributeDefinitionRepository(q db.QueryExecer) domainattribute.Repository {
	r := &attributeDefinitionRepository{q: q}
	r.byID = dataloader.NewZeroLoader(r.batchByID, loaderConfig())
	r.byName = dataloader.NewZeroLoader(r.batchByName, loaderConfig())
	r.byTypePage = dataloader.NewSliceLoader(r.batchByTypePage, loaderConfig())
	r.byList = dataloader.NewSliceLoader(r.batchList, loaderConfig())
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *attributeDefinitionRepository) WithTx(tx db.QueryExecer) domainattribute.Repository {
	return &attributeDefinitionRepository{q: tx, inTx: true}
}

func (r *attributeDefinitionRepository) batchByID(ctx context.Context, ids []string) (map[string]domainattribute.Snapshot, error) {
	var rows []attrRow
	query := fmt.Sprintf(`SELECT %s FROM flexitype_attribute_definition WHERE id = ANY($1)`, attrColumns)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("batch attribute definitions by id: %w", err)
	}
	out := make(map[string]domainattribute.Snapshot, len(rows))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		out[row.ID.String()] = snap
	}
	return out, nil
}

func (r *attributeDefinitionRepository) batchByName(ctx context.Context, keys []nameKey) (map[nameKey]domainattribute.Snapshot, error) {
	args := make([]any, 0, len(keys)*2)
	tuples := make([]string, 0, len(keys))
	for i, k := range keys {
		tuples = append(tuples, fmt.Sprintf("($%d, $%d)", i*2+1, i*2+2))
		args = append(args, k.Tenant, k.Name) // Tenant carries the type definition ID here.
	}
	query := fmt.Sprintf(
		`SELECT %s FROM flexitype_attribute_definition
		 WHERE archived_at IS NULL AND (type_definition_id, internal_name) IN (%s)`,
		attrColumns, strings.Join(tuples, ", "),
	)

	var rows []attrRow
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("batch attribute definitions by name: %w", err)
	}
	out := make(map[nameKey]domainattribute.Snapshot, len(rows))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		out[nameKey{Tenant: row.TypeDefinitionID.String(), Name: row.InternalName}] = snap
	}
	return out, nil
}

// batchByTypePage collapses per-type-definition attribute pages into one
// windowed query per (limit, offset) group.
func (r *attributeDefinitionRepository) batchByTypePage(ctx context.Context, keys []dataloader.PageKey[string]) (map[dataloader.PageKey[string]]dataloader.PagedResult[domainattribute.Snapshot], error) {
	out := make(map[dataloader.PageKey[string]]dataloader.PagedResult[domainattribute.Snapshot], len(keys))

	for group, parents := range pageKeyGroups(keys) {
		limit, offset := group[0], group[1]
		query := fmt.Sprintf(
			`SELECT * FROM (
			   SELECT %s,
			          count(*)     OVER (PARTITION BY type_definition_id) AS total_count,
			          row_number() OVER (PARTITION BY type_definition_id ORDER BY id) AS rn
			   FROM flexitype_attribute_definition
			   WHERE type_definition_id = ANY($1) AND archived_at IS NULL
			 ) w
			 WHERE rn > $2 AND rn <= $3
			 ORDER BY type_definition_id, rn`,
			attrColumns,
		)

		var rows []struct {
			attrRow
			TotalCount int `db:"total_count"`
			RN         int `db:"rn"`
		}
		if err := r.q.SelectContext(ctx, &rows, query, pq.Array(parents), offset, offset+limit); err != nil {
			return nil, fmt.Errorf("batch attributes by type definition: %w", err)
		}

		results := make(map[string]dataloader.PagedResult[domainattribute.Snapshot], len(parents))
		for _, row := range rows {
			snap, err := row.snapshot()
			if err != nil {
				return nil, err
			}
			parent := row.TypeDefinitionID.String()
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

func (r *attributeDefinitionRepository) batchList(ctx context.Context, keys []attrListKey) (map[attrListKey]dataloader.PagedResult[domainattribute.Snapshot], error) {
	out := make(map[attrListKey]dataloader.PagedResult[domainattribute.Snapshot], len(keys))
	for _, key := range keys {
		items, total, err := r.queryList(ctx, key)
		if err != nil {
			return nil, err
		}
		out[key] = dataloader.PagedResult[domainattribute.Snapshot]{Items: items, Total: total}
	}
	return out, nil
}

func (r *attributeDefinitionRepository) queryList(ctx context.Context, key attrListKey) ([]domainattribute.Snapshot, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{key.Tenant}
	if !key.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if key.TypeDefinitionID != "" {
		args = append(args, key.TypeDefinitionID)
		where = append(where, fmt.Sprintf("type_definition_id = $%d", len(args)))
	}
	if key.InternalNames != "" {
		args = append(args, pq.Array(strings.Split(key.InternalNames, "\x00")))
		where = append(where, fmt.Sprintf("internal_name = ANY($%d)", len(args)))
	}
	if key.DataTypes != "" {
		args = append(args, pq.Array(strings.Split(key.DataTypes, "\x00")))
		where = append(where, fmt.Sprintf("data_type = ANY($%d)", len(args)))
	}
	args = append(args, key.Limit, key.Offset)

	query := fmt.Sprintf(
		`SELECT %s, count(*) OVER () AS total_count
		 FROM flexitype_attribute_definition
		 WHERE %s
		 ORDER BY id
		 LIMIT $%d OFFSET $%d`,
		attrColumns, strings.Join(where, " AND "), len(args)-1, len(args),
	)

	var rows []struct {
		attrRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, fmt.Errorf("list attribute definitions: %w", err)
	}

	items := make([]domainattribute.Snapshot, 0, len(rows))
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

func (r *attributeDefinitionRepository) Get(ctx context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := r.byID.Load(ctx, id.String())
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domainattribute.AggregateType, id.String())
	}
	return domainattribute.Rehydrate(snap), nil
}

func (r *attributeDefinitionRepository) GetMany(ctx context.Context, ids []valueobjects.AttributeDefinitionID) ([]*domainattribute.Definition, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, id.String())
	}
	if r.inTx {
		fetched, err := r.batchByID(ctx, keys)
		if err != nil {
			return nil, err
		}
		out := make([]*domainattribute.Definition, 0, len(keys))
		for _, k := range keys {
			snap, ok := fetched[k]
			if !ok {
				return nil, domainerrors.NewNotFound(domainattribute.AggregateType, k)
			}
			out = append(out, domainattribute.Rehydrate(snap))
		}
		return out, nil
	}

	snaps, err := r.byID.LoadMany(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make([]*domainattribute.Definition, 0, len(snaps))
	for i, snap := range snaps {
		if snap.ID.IsZero() {
			return nil, domainerrors.NewNotFound(domainattribute.AggregateType, keys[i])
		}
		out = append(out, domainattribute.Rehydrate(snap))
	}
	return out, nil
}

func (r *attributeDefinitionRepository) GetForUpdate(ctx context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	if !r.inTx {
		return nil, fmt.Errorf("attribute definition repository: GetForUpdate requires a transaction")
	}
	return r.getDirect(ctx, id, true)
}

func (r *attributeDefinitionRepository) getDirect(ctx context.Context, id valueobjects.AttributeDefinitionID, forUpdate bool) (*domainattribute.Definition, error) {
	query := fmt.Sprintf(`SELECT %s FROM flexitype_attribute_definition WHERE id = $1`, attrColumns)
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row attrRow
	if err := r.q.GetContext(ctx, &row, query, id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domainattribute.AggregateType, id.String())
		}
		return nil, fmt.Errorf("get attribute definition: %w", err)
	}
	snap, err := row.snapshot()
	if err != nil {
		return nil, err
	}
	return domainattribute.Rehydrate(snap), nil
}

func (r *attributeDefinitionRepository) GetByInternalName(ctx context.Context, typeDefID valueobjects.TypeDefinitionID, internalName string) (*domainattribute.Definition, error) {
	if r.inTx {
		query := fmt.Sprintf(
			`SELECT %s FROM flexitype_attribute_definition
			 WHERE type_definition_id = $1 AND internal_name = $2 AND archived_at IS NULL`,
			attrColumns,
		)
		var row attrRow
		if err := r.q.GetContext(ctx, &row, query, typeDefID.String(), internalName); err != nil {
			if isNoRows(err) {
				return nil, domainerrors.NewNotFound(domainattribute.AggregateType, internalName)
			}
			return nil, fmt.Errorf("get attribute definition by name: %w", err)
		}
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		return domainattribute.Rehydrate(snap), nil
	}

	snap, err := r.byName.Load(ctx, nameKey{Tenant: typeDefID.String(), Name: internalName})
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domainattribute.AggregateType, internalName)
	}
	return domainattribute.Rehydrate(snap), nil
}

func (r *attributeDefinitionRepository) ListByTypeDefinition(ctx context.Context, typeDefID valueobjects.TypeDefinitionID, page db.Page) ([]*domainattribute.Definition, int, error) {
	key := dataloader.PageKey[string]{Parent: typeDefID.String(), Limit: page.Limit, Offset: page.Offset}

	var result dataloader.PagedResult[domainattribute.Snapshot]
	var err error
	if r.inTx {
		fetched, ferr := r.batchByTypePage(ctx, []dataloader.PageKey[string]{key})
		if ferr != nil {
			return nil, 0, ferr
		}
		result = fetched[key]
	} else {
		result, err = r.byTypePage.Load(ctx, key)
		if err != nil {
			return nil, 0, err
		}
	}

	out := make([]*domainattribute.Definition, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domainattribute.Rehydrate(snap))
	}
	return out, result.Total, nil
}

func (r *attributeDefinitionRepository) List(ctx context.Context, filter domainattribute.Filter, page db.Page) ([]*domainattribute.Definition, int, error) {
	dataTypes := make([]string, 0, len(filter.DataTypes))
	for _, dt := range filter.DataTypes {
		dataTypes = append(dataTypes, dt.String())
	}
	key := attrListKey{
		Tenant:          filter.TenantID.String(),
		InternalNames:   joinSorted(filter.InternalNames),
		DataTypes:       joinSorted(dataTypes),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if !filter.TypeDefinitionID.IsZero() {
		key.TypeDefinitionID = filter.TypeDefinitionID.String()
	}

	var result dataloader.PagedResult[domainattribute.Snapshot]
	var err error
	if r.inTx {
		var items []domainattribute.Snapshot
		var total int
		items, total, err = r.queryList(ctx, key)
		result = dataloader.PagedResult[domainattribute.Snapshot]{Items: items, Total: total}
	} else {
		result, err = r.byList.Load(ctx, key)
	}
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domainattribute.Definition, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domainattribute.Rehydrate(snap))
	}
	return out, result.Total, nil
}

func (r *attributeDefinitionRepository) Save(ctx context.Context, a *domainattribute.Definition) error {
	s := a.Snapshot()

	constraints, err := json.Marshal(s.Constraints)
	if err != nil {
		return fmt.Errorf("encode constraints: %w", err)
	}
	var defaultValue []byte
	if s.DefaultValue != nil {
		if defaultValue, err = json.Marshal(s.DefaultValue); err != nil {
			return fmt.Errorf("encode default value: %w", err)
		}
	}

	_, err = r.q.ExecContext(ctx,
		`INSERT INTO flexitype_attribute_definition
		   (id, tenant_id, type_definition_id, internal_name, display_name, description,
		    data_type, required, multi_valued, is_unique, constraints, default_value, version,
		    created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		 ON CONFLICT (id) DO UPDATE SET
		   display_name  = EXCLUDED.display_name,
		   description   = EXCLUDED.description,
		   required      = EXCLUDED.required,
		   multi_valued  = EXCLUDED.multi_valued,
		   is_unique     = EXCLUDED.is_unique,
		   constraints   = EXCLUDED.constraints,
		   default_value = EXCLUDED.default_value,
		   version       = EXCLUDED.version,
		   updated_at    = EXCLUDED.updated_at,
		   archived_at   = EXCLUDED.archived_at`,
		s.ID.String(), s.TenantID.String(), s.TypeDefinitionID.String(), s.InternalName,
		s.DisplayName, s.Description, s.DataType.String(), s.Required, s.MultiValued,
		s.Unique, jsonbParam(constraints), jsonbParam(defaultValue), s.Version, s.CreatedAt,
		s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save attribute definition: %w", err)
	}
	return nil
}
