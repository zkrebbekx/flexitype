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
func (r *attributeValueRepository) WithTx(tx db.Tx) domainvalue.Repository {
	return &attributeValueRepository{q: txExecer(tx), inTx: true}
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
	extra := ""
	if v.DataType() == valueobjects.DataTypeDecimal {
		col += "::numeric"
		cast = "::numeric"
		// data_type='decimal' is a no-op filter for a decimal attribute, but it
		// lets the planner use the partial expression index on
		// (attribute_definition_id, (value_text::numeric)) scoped to decimal rows.
		extra = " AND data_type = 'decimal'"
	}
	query := bind(`SELECT count(*) FROM flexitype_attribute_value
	 WHERE attribute_definition_id = ? AND ` + col + ` = ?` + cast + extra + `
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
	// The summary projection (flexitype_entity_summary, maintained by a trigger
	// on flexitype_attribute_value) already holds one row per live entity with
	// its value_count and last_updated_at, so a page is a keyset window over it
	// — no per-page aggregation of the value table. Keyset on the ordered
	// (last update, entity id): newest-first with entity_id as the unique
	// tiebreaker. The predicate is now a plain WHERE, not a HAVING.
	//
	// The single-type case (the entity browser without descendants, facet-set
	// resolution and Reindex) is emitted as an equality on type_definition_id,
	// not `= ANY`: with tenant_id + type_definition_id fixed, the ordering index
	// idx_flexitype_entity_summary_order (…, last_updated_at DESC, entity_id)
	// satisfies the ORDER BY directly, so a page is a bounded index scan of
	// `limit` rows. A ScalarArrayOp (`= ANY`) over the leading index column
	// cannot preserve that ordering in one scan, so the multi-type (descendants)
	// case scans the — still small, one-row-per-entity — summary and sorts,
	// which remains far cheaper than aggregating the value table.
	typePred, typeArg := typeDefPredicate(ids)
	// A full sweep pages on the IMMUTABLE key. last_updated_at is rewritten
	// by the summary trigger on every value write, so an entity the sweep has
	// not reached yet, written mid-sweep, jumps ahead of the newest-first
	// cursor and can never satisfy the "strictly older" predicate again — it
	// is skipped, silently. entity_id never changes, and it is the trailing
	// column of the table's primary key, so this ordering is an index scan
	// too.
	entityKeyset := []db.KeysetColumn{{Expr: "last_updated_at", Desc: true, Cast: "::timestamptz"}, {Expr: "entity_id"}}
	orderBy := "last_updated_at DESC, entity_id"
	if page.Stable {
		entityKeyset = []db.KeysetColumn{{Expr: "entity_id"}}
		orderBy = "entity_id"
	}
	where := []string{"tenant_id = ?", typePred}
	args := []any{tenant.String(), typeArg}
	where, args = keysetWhere(where, args, entityKeyset, page.Cursor)
	args = append(args, page.FetchLimit())
	err := r.q.SelectContext(ctx, &rows, bind(
		`SELECT entity_id, type_definition_id, value_count, last_updated_at
		 FROM flexitype_entity_summary
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY `+orderBy+`
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
		// The projection holds exactly one row per live entity, so the total is
		// a plain count of the matching summary rows.
		if err := r.q.GetContext(ctx, &total, bind(
			`SELECT count(*) FROM flexitype_entity_summary
			 WHERE tenant_id = ? AND `+typePred),
			tenant.String(), typeArg); err != nil {
			return nil, 0, fmt.Errorf("count entities: %w", err)
		}
	}
	return out, total, nil
}

// typeDefPredicate renders the type_definition_id filter for the entity-summary
// reads. A single type becomes an equality so the ordering index can serve the
// page as a bounded index scan; multiple types (subtype browsing) fall back to
// `= ANY`. It returns the `?`-placeholdered predicate and its bound argument.
func typeDefPredicate(ids []string) (string, any) {
	if len(ids) == 1 {
		return "type_definition_id = ?", ids[0]
	}
	return "type_definition_id = ANY(?)", pq.Array(ids)
}

// EntityAnchor returns the type an entity's values are anchored to. Served by
// the non-partial (tenant_id, entity_id) index, because it deliberately counts
// archived rows.
func (r *attributeValueRepository) EntityAnchor(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) (valueobjects.TypeDefinitionID, bool, error) {
	var raw []string
	if err := r.q.SelectContext(ctx, &raw, bind(
		// id breaks the tie. Ordering on created_at alone left the anchor
		// undefined for rows written in one batch, which share a timestamp —
		// so the same entity could resolve to different anchors on different
		// reads and each write would rewrite every row to "correct" it.
		`SELECT type_definition_id FROM flexitype_attribute_value
		  WHERE tenant_id = ? AND entity_id = ?
		  ORDER BY created_at, id
		  LIMIT 1`),
		tenant.String(), entityID.String()); err != nil {
		return valueobjects.TypeDefinitionID{}, false, fmt.Errorf("entity anchor: %w", err)
	}
	if len(raw) == 0 {
		return valueobjects.TypeDefinitionID{}, false, nil
	}
	id, err := valueobjects.ParseTypeDefinitionID(raw[0])
	if err != nil {
		return valueobjects.TypeDefinitionID{}, false, fmt.Errorf("entity anchor: %w", err)
	}
	return id, true, nil
}

// ReanchorEntity moves an entity's rows onto a narrower type. Keyed on
// (tenant_id, entity_id), so it is served by the non-partial index.
func (r *attributeValueRepository) ReanchorEntity(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID, to valueobjects.TypeDefinitionID) (int, error) {
	if !r.inTx {
		return 0, fmt.Errorf("attribute value repository: ReanchorEntity requires a transaction")
	}
	res, err := r.q.ExecContext(ctx, bind(
		`UPDATE flexitype_attribute_value
		    SET type_definition_id = ?
		  WHERE tenant_id = ? AND entity_id = ? AND type_definition_id <> ?`),
		to.String(), tenant.String(), entityID.String(), to.String())
	if err != nil {
		return 0, fmt.Errorf("reanchor entity: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// MediaValueForKey returns the media value the tenant stores for an object key.
// It is served by the same (tenant_id, object_key) expression index the
// download-authorization probe uses.
func (r *attributeValueRepository) MediaValueForKey(ctx context.Context, tenant valueobjects.TenantID, objectKey string) (domainvalue.Snapshot, bool, error) {
	var rows []valueRow
	if err := r.q.SelectContext(ctx, &rows, bind(
		`SELECT `+valueColumnList+`
		   FROM flexitype_attribute_value
		  WHERE tenant_id = ? AND data_type = ? AND value_json->>'object_key' = ?
		  ORDER BY created_at, id
		  LIMIT 1`),
		tenant.String(), valueobjects.DataTypeMedia.String(), objectKey); err != nil {
		return domainvalue.Snapshot{}, false, fmt.Errorf("media value for key: %w", err)
	}
	if len(rows) == 0 {
		return domainvalue.Snapshot{}, false, nil
	}
	snap, err := rows[0].snapshot()
	if err != nil {
		return domainvalue.Snapshot{}, false, err
	}
	return snap, true, nil
}

// MediaKeyRefCount counts live rows referencing an object key, across tenants,
// excluding one value id. Served by the cross-tenant partial expression index
// on (value_json->>'object_key') from migration 000034; the tenant-leading
// index from 000021 cannot seek for a cross-tenant count.
func (r *attributeValueRepository) MediaKeyRefCount(ctx context.Context, objectKey string, exclude valueobjects.AttributeValueID) (int, error) {
	var n int
	if err := r.q.GetContext(ctx, &n, bind(
		`SELECT count(*) FROM flexitype_attribute_value
		  WHERE data_type = ? AND value_json->>'object_key' = ? AND id <> ?
		    AND archived_at IS NULL`),
		valueobjects.DataTypeMedia.String(), objectKey, exclude.String()); err != nil {
		return 0, fmt.Errorf("media key reference count: %w", err)
	}
	return n, nil
}

// MediaKeyRefCounts counts live rows per object key, across tenants, in one
// grouped query. Keys with no live rows are absent from the result. Served by
// the same 000034 partial expression index as MediaKeyRefCount.
func (r *attributeValueRepository) MediaKeyRefCounts(ctx context.Context, objectKeys []string) (map[string]int, error) {
	out := make(map[string]int, len(objectKeys))
	if len(objectKeys) == 0 {
		return out, nil
	}
	rows, err := r.q.QueryContext(ctx, bind(
		`SELECT value_json->>'object_key', count(*)
		   FROM flexitype_attribute_value
		  WHERE data_type = ? AND value_json->>'object_key' = ANY(?)
		    AND archived_at IS NULL
		  GROUP BY 1`),
		valueobjects.DataTypeMedia.String(), pq.Array(objectKeys))
	if err != nil {
		return nil, fmt.Errorf("media key reference counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("media key reference counts: %w", err)
		}
		out[key] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media key reference counts: %w", err)
	}
	return out, nil
}

// LockMediaKey takes the transaction-scoped advisory lock that serializes
// adoption and blob GC of one object key. pg_advisory_xact_lock blocks until
// the lock is free, is re-entrant inside the transaction, and releases at
// commit or rollback — exactly the hold-until-commit contract the domain port
// asks for. The 64-bit hash keys the lock; a hash collision only means two
// unrelated keys briefly serialize, which is safe.
func (r *attributeValueRepository) LockMediaKey(ctx context.Context, objectKey string) error {
	if !r.inTx {
		return fmt.Errorf("attribute value repository: LockMediaKey requires a transaction")
	}
	if _, err := r.q.ExecContext(ctx, bind(
		`SELECT pg_advisory_xact_lock(hashtextextended('flexitype:media-key:' || ?, 0))`),
		objectKey); err != nil {
		return fmt.Errorf("lock media key: %w", err)
	}
	return nil
}

// MediaKeyAttributes returns the distinct attributes of the tenant's value rows
// that reference objectKey. The object key lives inside the media metadata
// JSON; the tenant + data_type filter narrows the scan to the tenant's media
// rows, which is ample for the infrequent download-authorization path. Archived
// rows are included: an archived media value's blob may still exist and is
// still the tenant's.
func (r *attributeValueRepository) MediaKeyAttributes(ctx context.Context, tenant valueobjects.TenantID, objectKey string) ([]valueobjects.AttributeDefinitionID, error) {
	var raw []string
	if err := r.q.SelectContext(ctx, &raw, bind(
		`SELECT DISTINCT attribute_definition_id
		   FROM flexitype_attribute_value
		  WHERE tenant_id = ? AND data_type = ? AND value_json->>'object_key' = ?
		  ORDER BY attribute_definition_id`),
		tenant.String(), valueobjects.DataTypeMedia.String(), objectKey); err != nil {
		return nil, fmt.Errorf("media key attributes: %w", err)
	}
	out := make([]valueobjects.AttributeDefinitionID, 0, len(raw))
	for _, s := range raw {
		id, err := valueobjects.ParseAttributeDefinitionID(s)
		if err != nil {
			return nil, fmt.Errorf("media key attributes: %w", err)
		}
		out = append(out, id)
	}
	return out, nil
}

// AttributeDataShape answers, in one pass, every question a structural
// schema change has to ask of the data already stored.
//
// It is an aggregate over one attribute's live rows, so it rides the
// (tenant_id, attribute_definition_id) index and never scans the table.
func (r *attributeValueRepository) AttributeDataShape(ctx context.Context, tenant valueobjects.TenantID, attrID valueobjects.AttributeDefinitionID) (domainvalue.DataShape, error) {
	var row struct {
		LiveValues       int `db:"live_values"`
		EntitiesWithMany int `db:"entities_with_many"`
		ScopedValues     int `db:"scoped_values"`
		DuplicateValues  int `db:"duplicate_values"`
	}
	// The value comparison mirrors what a unique constraint compares: the
	// rendered text of the value, whichever typed column holds it.
	const q = `
WITH live AS (
    SELECT entity_id, locale, channel,
           COALESCE(value_text, value_json::text, value_int::text,
                    value_float::text, value_bool::text, value_time::text) AS v
      FROM flexitype_attribute_value
     WHERE tenant_id = ? AND attribute_definition_id = ? AND archived_at IS NULL
),
-- Grouped by SCOPE as well as entity. Grouping on the entity alone counted a
-- localizable attribute holding one value per locale as "more than one
-- value", so making it single-valued was refused for data the new schema can
-- express perfectly — and the only way through was deleting real data.
per_entity AS (SELECT entity_id, COUNT(*) AS n FROM live
                GROUP BY entity_id, COALESCE(locale, ''), COALESCE(channel, '')),
per_value  AS (SELECT v, COUNT(DISTINCT entity_id) AS n FROM live WHERE v IS NOT NULL GROUP BY v)
SELECT
  (SELECT COUNT(*) FROM live)                                        AS live_values,
  (SELECT COUNT(DISTINCT entity_id) FROM per_entity WHERE n > 1)     AS entities_with_many,
  (SELECT COUNT(*) FROM live WHERE COALESCE(locale, '') <> ''
                                OR COALESCE(channel, '') <> '')      AS scoped_values,
  (SELECT COALESCE(SUM(n), 0) FROM per_value WHERE n > 1)            AS duplicate_values`
	if err := r.q.GetContext(ctx, &row, bind(q), tenant.String(), attrID.String()); err != nil {
		return domainvalue.DataShape{}, fmt.Errorf("attribute data shape: %w", err)
	}
	return domainvalue.DataShape{
		LiveValues:       row.LiveValues,
		EntitiesWithMany: row.EntitiesWithMany,
		ScopedValues:     row.ScopedValues,
		DuplicateValues:  row.DuplicateValues,
	}, nil
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

// purgedValueRow is the projection returned by a purge DELETE: enough to
// recover the object key of any media value so the blobs can be collected.
type purgedValueRow struct {
	DataType string `db:"data_type"`
	JSON     []byte `db:"value_json"`
}

// purgedMediaKeys extracts the backing object keys of the media rows a purge
// removed, so the caller can garbage-collect the blobs after commit.
func purgedMediaKeys(rows []purgedValueRow) []string {
	var keys []string
	for _, row := range rows {
		if row.DataType != valueobjects.DataTypeMedia.String() {
			continue
		}
		v, err := valueobjects.NewMediaValue(json.RawMessage(row.JSON))
		if err != nil {
			continue // malformed metadata: nothing recoverable to collect
		}
		if key := v.Media().ObjectKey; key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// purgeChunk bounds one purge DELETE.
//
// The entity-summary trigger is FOR EACH STATEMENT with REFERENCING OLD
// TABLE, so a bulk delete materialises every removed row into a tuplestore.
// One unbounded statement therefore spills the whole purge to temp disk:
// measured on Postgres 16 with 300k value rows and work_mem=4MB, the purge
// took 17.2x longer than with the triggers dropped and wrote ~42MB of temp
// blocks that did not exist before. Migration 000022's own comment cites 10^8
// value rows as the target scale, where that is on the order of 14GB of temp
// files inside a single uninterruptible statement.
//
// Chunking keeps each transition table proportional to the chunk rather than
// to the tenant. The chunks run in the caller's transaction, so the purge is
// still atomic.
const purgeChunk = 5000

// purgeStallLimit bounds the consecutive empty chunks tolerated while rows
// still match, so a purge racing a continuous writer terminates.
const purgeStallLimit = 3

func (r *attributeValueRepository) PurgeEntity(ctx context.Context, key domainvalue.EntityKey) ([]string, int, error) {
	// DELETE ... RETURNING removes every row (archived included) and hands back
	// the media metadata so the interactor can GC the blobs.
	const where = `tenant_id = ? AND type_definition_id = ? AND entity_id = ?`
	return r.purgeChunked(ctx, "purge entity values", where,
		key.TenantID.String(), key.TypeDefinitionID.String(), key.EntityID.String())
}

func (r *attributeValueRepository) PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) ([]string, int, error) {
	return r.purgeChunked(ctx, "purge tenant values", `tenant_id = ?`, tenant.String())
}

// purgeChunked deletes every row matching the predicate, in bounded chunks,
// and confirms the predicate is empty before reporting success.
//
// The chunk key is the PRIMARY KEY, not the ctid. A ctid is not stable across
// an UPDATE: Postgres re-checks the qualification against the new tuple
// version under EvalPlanQual, and that version's ctid is not in the hashed
// set, so a row updated by a committed concurrent transaction was silently
// SKIPPED. Measured on Postgres 16 — three rows, one concurrent committed
// UPDATE — the ctid form deleted 0 and left all three, while the same
// interleaving against a direct predicate deleted all three. An id survives
// an UPDATE, which is why the dead-letter pruner in the same wave was already
// correct.
//
// ORDER BY entity_id, id is the canonical write order (see
// application/value/lockorder.go). Every value write refreshes a shared
// entity-summary row, so a purge that took those rows in an arbitrary order
// deadlocked against a batch write that took them in entity order — 4 rounds
// out of 4. The purge is a writer like any other and follows the same rule.
//
// FOR UPDATE inside the CTE is what makes the ordering effective. An ORDER BY
// in a plain `id IN (subquery)` decides only WHICH rows the chunk takes: the
// DELETE joins them back and locks them in its own scan order, so the
// deadlock survived the ORDER BY alone (measured — still 1 round in 3). The
// LockRows node sits above the Sort, so the CTE takes the row locks in
// entity order, and the DELETE then touches rows this transaction already
// holds.
//
// The final COUNT is what makes a zero-row chunk trustworthy. Reporting
// success on an empty chunk is how a purge came to return a receipt with the
// data still present, and erasure is the one operation where a false success
// is a compliance failure rather than a bug.
func (r *attributeValueRepository) purgeChunked(ctx context.Context, what, where string, args ...any) ([]string, int, error) {
	del := bind(`WITH victim AS (
	   SELECT id FROM flexitype_attribute_value
	    WHERE ` + where + `
	    ORDER BY entity_id, id
	    LIMIT ?
	    FOR UPDATE
	 )
	 DELETE FROM flexitype_attribute_value v
	  USING victim
	  WHERE v.id = victim.id
	  RETURNING v.data_type, v.value_json`)
	count := bind(`SELECT count(*) FROM flexitype_attribute_value WHERE ` + where)

	var keys []string
	total, stalled := 0, 0
	for {
		var rows []purgedValueRow
		if err := r.q.SelectContext(ctx, &rows, del, append(append([]any{}, args...), purgeChunk)...); err != nil {
			return nil, 0, fmt.Errorf("%s: %w", what, err)
		}
		keys = append(keys, purgedMediaKeys(rows)...)
		total += len(rows)
		if len(rows) > 0 {
			stalled = 0
		} else {
			var left int
			if err := r.q.GetContext(ctx, &left, count, args...); err != nil {
				return nil, 0, fmt.Errorf("%s: confirm empty: %w", what, err)
			}
			if left == 0 {
				return keys, total, nil
			}
			// Rows match but the chunk removed none. A writer that committed
			// after the chunk's snapshot explains one round, and the next
			// chunk takes those rows. A run of them means the purge cannot
			// make progress, and a purge that cannot make progress must
			// report that rather than answer success.
			stalled++
			if stalled >= purgeStallLimit {
				return nil, 0, fmt.Errorf("%s: %d rows still match after %d chunks removed none", what, left, stalled)
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
	}
}
