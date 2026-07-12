package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
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
	locale, channel, data_type, value_bool, value_int, value_float, value_text, value_time, value_json,
	definition_version, created_at, updated_at, archived_at`

type valueRow struct {
	ID                    ulid.ID `db:"id"`
	TenantID              string  `db:"tenant_id"`
	TypeDefinitionID      ulid.ID `db:"type_definition_id"`
	AttributeDefinitionID ulid.ID `db:"attribute_definition_id"`
	EntityID              string  `db:"entity_id"`
	Locale                string  `db:"locale"`
	Channel               string  `db:"channel"`
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
		Locale:                r.Locale,
		Channel:               r.Channel,
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
	Cursor          string `json:"cursor,omitempty"`
}

func (f valueListFilter) key() string {
	b, _ := json.Marshal(f)
	return string(b)
}

func (f valueListFilter) where() ([]string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{f.Tenant}
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
	return where, args
}

func (f valueListFilter) arm(key string) (string, []any) {
	where, filterArgs := f.where()
	args := append([]any{key}, filterArgs...)
	where, args = keysetWhere(where, args, idKeyset, f.Cursor)
	args = append(args, f.Limit+1)

	query := `(SELECT ?::text AS loader_key, ` + valueColumnList + `
	 FROM flexitype_attribute_value
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ?)`
	return query, args
}

func (f valueListFilter) countQuery() (string, []any) {
	where, args := f.where()
	return `SELECT count(*) FROM flexitype_attribute_value WHERE ` + strings.Join(where, " AND "), args
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

// batchByDefinitionPage collapses per-definition value pages into one keyset
// windowed query per (limit, cursor) group. It over-fetches one row per
// partition so the caller can detect a next page.
func (r *attributeValueRepository) batchByDefinitionPage(ctx context.Context, keys []pageKey) (map[pageKey]pagedResult[domainvalue.Snapshot], error) {
	out := make(map[pageKey]pagedResult[domainvalue.Snapshot], len(keys))

	for group, parents := range pageKeyGroups(keys) {
		limit, _ := strconv.Atoi(group[0])
		cursor := group[1]
		inner := []string{"attribute_definition_id = ANY(?)", "archived_at IS NULL"}
		qargs := []any{pq.Array(parents)}
		inner, qargs = keysetWhere(inner, qargs, idKeyset, cursor)
		query := bind(`SELECT * FROM (
		   SELECT ` + valueColumnList + `,
		          row_number() OVER (PARTITION BY attribute_definition_id ORDER BY id) AS rn
		   FROM flexitype_attribute_value
		   WHERE ` + strings.Join(inner, " AND ") + `
		 ) w
		 WHERE rn <= ?
		 ORDER BY attribute_definition_id, rn`)
		qargs = append(qargs, limit+1)

		var rows []struct {
			valueRow
			RN int `db:"rn"`
		}
		if err := r.q.SelectContext(ctx, &rows, query, qargs...); err != nil {
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
			results[parent] = pr
		}
		for _, parent := range parents {
			out[pageKey{Parent: parent, Limit: limit, Cursor: cursor}] = results[parent]
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
	key := pageKey{Parent: defID.String(), Limit: page.Limit, Cursor: page.Cursor}

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
	// ListByDefinition backs an internal full-scan (dedup) that follows the
	// cursor and stops on a short page, so it does not need a total.
	return out, 0, nil
}

func (r *attributeValueRepository) ListByEntities(ctx context.Context, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) ([]*domainvalue.AttributeValue, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	tuples := make([]string, 0, len(entityIDs))
	args := make([]any, 0, len(entityIDs)*2)
	for _, id := range entityIDs {
		tuples = append(tuples, "(?, ?)")
		args = append(args, tenant.String(), id.String())
	}
	query := bind(`SELECT ` + valueColumnList + ` FROM flexitype_attribute_value
	 WHERE archived_at IS NULL
	   AND (tenant_id, entity_id) IN (` + strings.Join(tuples, ", ") + `)
	 ORDER BY id`)
	var rows []valueRow
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list values by entities: %w", err)
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

func (r *attributeValueRepository) CountByDefinitionAndValue(ctx context.Context, defID valueobjects.AttributeDefinitionID, scope valueobjects.Scope, v valueobjects.Value, excludeEntity valueobjects.EntityID) (int, error) {
	// The value column is an identifier chosen by data type; arguments stay
	// bound placeholders. Uniqueness is scoped by locale/channel.
	//
	// Decimals are stored as text but must compare NUMERICALLY, so "1.5" and
	// "1.50" collide — matching the in-memory backend (Value.Equal) and the
	// FQL ::numeric comparison. Without the cast the two backends disagree and
	// Postgres silently admits a logical duplicate. JSON lives in a jsonb
	// column, whose `=` is already structural (key-order insensitive).
	col := valueColumnName(v.DataType())
	cast := ""
	if v.DataType() == valueobjects.DataTypeDecimal {
		col += "::numeric"
		cast = "::numeric"
	}
	query := bind(`SELECT count(*) FROM flexitype_attribute_value
	 WHERE attribute_definition_id = ? AND ` + col + ` = ?` + cast + `
	   AND entity_id <> ? AND locale = ? AND channel = ? AND archived_at IS NULL`)
	var count int
	if err := r.q.GetContext(ctx, &count, query, defID.String(), valueArg(v), excludeEntity.String(), scope.Locale, scope.Channel); err != nil {
		return 0, fmt.Errorf("count values by definition and value: %w", err)
	}
	return count, nil
}

func (r *attributeValueRepository) List(ctx context.Context, filter domainvalue.Filter, page db.Page) ([]*domainvalue.AttributeValue, int, error) {
	f := valueListFilter{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Cursor:          page.Cursor,
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
	total, err := countIf(ctx, r.q, page.WantTotal, f.countQuery)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *attributeValueRepository) ListEntities(ctx context.Context, tenant valueobjects.TenantID, typeDefIDs []valueobjects.TypeDefinitionID, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	ids := make([]string, 0, len(typeDefIDs))
	for _, id := range typeDefIDs {
		ids = append(ids, id.String())
	}

	var rows []struct {
		EntityID         string    `db:"entity_id"`
		TypeDefinitionID ulid.ID   `db:"type_definition_id"`
		ValueCount       int       `db:"value_count"`
		LastUpdatedAt    time.Time `db:"last_updated_at"`
	}
	// Keyset on the ordered aggregate (last update) with entity_id as the
	// unique tiebreaker; the predicate is a HAVING clause because it compares
	// an aggregate.
	entityKeyset := []db.KeysetColumn{{Expr: "max(updated_at)", Desc: true, Cast: "::timestamptz"}, {Expr: "entity_id"}}
	pred, pargs, _ := db.KeysetPredicate(entityKeyset, page.Cursor)
	having := ""
	if pred != "" {
		having = "\n\t\t HAVING " + pred
	}
	args := []any{tenant.String(), pq.Array(ids)}
	args = append(args, pargs...)
	args = append(args, page.FetchLimit())
	// An entity's rows all carry its declared type, so grouping by both keeps
	// one row per entity while surfacing the subtype.
	err := r.q.SelectContext(ctx, &rows, bind(
		`SELECT entity_id,
		        type_definition_id,
		        count(*)        AS value_count,
		        max(updated_at) AS last_updated_at
		 FROM flexitype_attribute_value
		 WHERE tenant_id = ? AND type_definition_id = ANY(?) AND archived_at IS NULL
		 GROUP BY entity_id, type_definition_id`+having+`
		 ORDER BY max(updated_at) DESC, entity_id
		 LIMIT ?`),
		args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}

	out := make([]domainvalue.EntitySummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, domainvalue.EntitySummary{
			EntityID:         valueobjects.EntityID(row.EntityID),
			TypeDefinitionID: valueobjects.TypeDefinitionID{ID: row.TypeDefinitionID},
			ValueCount:       row.ValueCount,
			LastUpdatedAt:    row.LastUpdatedAt,
		})
	}
	total := 0
	if page.WantTotal {
		if err := r.q.GetContext(ctx, &total, bind(
			`SELECT count(*) FROM (
			   SELECT 1 FROM flexitype_attribute_value
			   WHERE tenant_id = ? AND type_definition_id = ANY(?) AND archived_at IS NULL
			   GROUP BY entity_id, type_definition_id
			 ) g`), tenant.String(), pq.Array(ids)); err != nil {
			return nil, 0, fmt.Errorf("count entities: %w", err)
		}
	}
	return out, total, nil
}

func (r *attributeValueRepository) Save(ctx context.Context, av *domainvalue.AttributeValue) error {
	s := av.Snapshot()
	cols := columnsFromValue(s.Value)

	_, err := r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_attribute_value
		   (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, locale, channel, data_type,
		    value_bool, value_int, value_float, value_text, value_time, value_json,
		    definition_version, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		s.AttributeDefinitionID.String(), s.EntityID.String(), s.Locale, s.Channel, s.Value.DataType().String(),
		cols.Bool, cols.Int, cols.Float, cols.Text, cols.Time, jsonbParam(cols.JSON),
		s.DefinitionVersion, s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save value: %w", err)
	}
	return nil
}
