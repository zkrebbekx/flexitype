package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/graph-gophers/dataloader/v7"
	"github.com/lib/pq"

	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
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

// attrListFilter is the cleansed JSON dataloader key for attribute List
// queries; unique keys become UNION ALL arms.
type attrListFilter struct {
	Tenant           string   `json:"tenant"`
	TypeDefinitionID string   `json:"type_definition_id,omitempty"`
	InternalNames    []string `json:"internal_names,omitempty"`
	DataTypes        []string `json:"data_types,omitempty"`
	IncludeArchived  bool     `json:"include_archived,omitempty"`
	Limit            int      `json:"limit"`
	Offset           int      `json:"offset"`
}

func (f attrListFilter) key() string {
	sort.Strings(f.InternalNames)
	sort.Strings(f.DataTypes)
	b, _ := json.Marshal(f)
	return string(b)
}

func (f attrListFilter) arm(key string) (string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{key, f.Tenant}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if f.TypeDefinitionID != "" {
		where = append(where, "type_definition_id = ?")
		args = append(args, f.TypeDefinitionID)
	}
	if len(f.InternalNames) > 0 {
		where = append(where, "internal_name = ANY(?)")
		args = append(args, pq.Array(f.InternalNames))
	}
	if len(f.DataTypes) > 0 {
		where = append(where, "data_type = ANY(?)")
		args = append(args, pq.Array(f.DataTypes))
	}
	args = append(args, f.Limit, f.Offset)

	query := `(SELECT ?::text AS loader_key, ` + attrColumns + `, count(*) OVER () AS total_count
	 FROM flexitype_attribute_definition
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ? OFFSET ?)`
	return query, args
}

type attributeDefinitionRepository struct {
	q          db.QueryExecer
	inTx       bool
	byID       *dataloader.Loader[string, domainattribute.Snapshot]
	byName     *dataloader.Loader[nameKey, domainattribute.Snapshot]
	byTypePage *dataloader.Loader[pageKey, pagedResult[domainattribute.Snapshot]]
	byList     *dataloader.Loader[string, pagedResult[domainattribute.Snapshot]]
}

// NewAttributeDefinitionRepository builds a dataloader-backed repository
// over the pool.
func NewAttributeDefinitionRepository(q db.QueryExecer) domainattribute.Repository {
	r := &attributeDefinitionRepository{q: q}
	r.byID = newLoader(r.batchByID)
	r.byName = newLoader(r.batchByName)
	r.byTypePage = newLoader(r.batchByTypePage)
	r.byList = newLoader(r.batchList)
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *attributeDefinitionRepository) WithTx(tx db.QueryExecer) domainattribute.Repository {
	return &attributeDefinitionRepository{q: tx, inTx: true}
}

func (r *attributeDefinitionRepository) batchByID(ctx context.Context, ids []string) (map[string]domainattribute.Snapshot, error) {
	var rows []attrRow
	query := bind(`SELECT ` + attrColumns + ` FROM flexitype_attribute_definition WHERE id = ANY(?)`)
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
	tuples := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		tuples = append(tuples, "(?, ?)")
		args = append(args, k.Scope, k.Name) // Scope carries the type definition ID here.
	}
	query := bind(`SELECT ` + attrColumns + ` FROM flexitype_attribute_definition
	 WHERE archived_at IS NULL AND (type_definition_id, internal_name) IN (` + strings.Join(tuples, ", ") + `)`)

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
		out[nameKey{Scope: row.TypeDefinitionID.String(), Name: row.InternalName}] = snap
	}
	return out, nil
}

// batchByTypePage collapses per-type-definition attribute pages into one
// windowed query per (limit, offset) group.
func (r *attributeDefinitionRepository) batchByTypePage(ctx context.Context, keys []pageKey) (map[pageKey]pagedResult[domainattribute.Snapshot], error) {
	out := make(map[pageKey]pagedResult[domainattribute.Snapshot], len(keys))

	for group, parents := range pageKeyGroups(keys) {
		limit, offset := group[0], group[1]
		query := bind(`SELECT * FROM (
		   SELECT ` + attrColumns + `,
		          count(*)     OVER (PARTITION BY type_definition_id) AS total_count,
		          row_number() OVER (PARTITION BY type_definition_id ORDER BY id) AS rn
		   FROM flexitype_attribute_definition
		   WHERE type_definition_id = ANY(?) AND archived_at IS NULL
		 ) w
		 WHERE rn > ? AND rn <= ?
		 ORDER BY type_definition_id, rn`)

		var rows []struct {
			attrRow
			TotalCount int `db:"total_count"`
			RN         int `db:"rn"`
		}
		if err := r.q.SelectContext(ctx, &rows, query, pq.Array(parents), offset, offset+limit); err != nil {
			return nil, fmt.Errorf("batch attributes by type definition: %w", err)
		}

		results := make(map[string]pagedResult[domainattribute.Snapshot], len(parents))
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
			out[pageKey{Parent: parent, Limit: limit, Offset: offset}] = results[parent]
		}
	}
	return out, nil
}

// batchList runs every unique filter key as one UNION ALL statement.
func (r *attributeDefinitionRepository) batchList(ctx context.Context, keys []string) (map[string]pagedResult[domainattribute.Snapshot], error) {
	arms := make([]string, 0, len(keys))
	var args []any
	for _, key := range keys {
		var f attrListFilter
		if err := json.Unmarshal([]byte(key), &f); err != nil {
			return nil, fmt.Errorf("decode list key: %w", err)
		}
		arm, armArgs := f.arm(key)
		arms = append(arms, arm)
		args = append(args, armArgs...)
	}

	var rows []struct {
		LoaderKey string `db:"loader_key"`
		attrRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, bind(strings.Join(arms, "\nUNION ALL\n")), args...); err != nil {
		return nil, fmt.Errorf("batch list attribute definitions: %w", err)
	}

	out := make(map[string]pagedResult[domainattribute.Snapshot], len(keys))
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

func (r *attributeDefinitionRepository) Get(ctx context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := load(ctx, r.byID, id.String())
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

	var snaps map[string]domainattribute.Snapshot
	var err error
	if r.inTx {
		snaps, err = r.batchByID(ctx, keys)
		if err != nil {
			return nil, err
		}
	} else {
		snaps = make(map[string]domainattribute.Snapshot, len(keys))
		for _, k := range keys {
			snap, lerr := load(ctx, r.byID, k)
			if lerr != nil {
				return nil, lerr
			}
			snaps[k] = snap
		}
	}

	out := make([]*domainattribute.Definition, 0, len(keys))
	for _, k := range keys {
		snap := snaps[k]
		if snap.ID.IsZero() {
			return nil, domainerrors.NewNotFound(domainattribute.AggregateType, k)
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
	query := `SELECT ` + attrColumns + ` FROM flexitype_attribute_definition WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row attrRow
	if err := r.q.GetContext(ctx, &row, bind(query), id.String()); err != nil {
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
		query := bind(`SELECT ` + attrColumns + ` FROM flexitype_attribute_definition
		 WHERE type_definition_id = ? AND internal_name = ? AND archived_at IS NULL`)
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

	snap, err := load(ctx, r.byName, nameKey{Scope: typeDefID.String(), Name: internalName})
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domainattribute.AggregateType, internalName)
	}
	return domainattribute.Rehydrate(snap), nil
}

func (r *attributeDefinitionRepository) ListByTypeDefinition(ctx context.Context, typeDefID valueobjects.TypeDefinitionID, page db.Page) ([]*domainattribute.Definition, int, error) {
	key := pageKey{Parent: typeDefID.String(), Limit: page.Limit, Offset: page.Offset}

	var result pagedResult[domainattribute.Snapshot]
	if r.inTx {
		fetched, err := r.batchByTypePage(ctx, []pageKey{key})
		if err != nil {
			return nil, 0, err
		}
		result = fetched[key]
	} else {
		var err error
		result, err = load(ctx, r.byTypePage, key)
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
	f := attrListFilter{
		Tenant:          filter.TenantID.String(),
		InternalNames:   filter.InternalNames,
		DataTypes:       dataTypes,
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if !filter.TypeDefinitionID.IsZero() {
		f.TypeDefinitionID = filter.TypeDefinitionID.String()
	}
	key := f.key()

	var result pagedResult[domainattribute.Snapshot]
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

	_, err = r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_attribute_definition
		   (id, tenant_id, type_definition_id, internal_name, display_name, description,
		    data_type, required, multi_valued, is_unique, constraints, default_value, version,
		    created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		   archived_at   = EXCLUDED.archived_at`),
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
