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

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

const typeDefColumns = `id, tenant_id, kind, internal_name, display_name, description, extends_id, version, created_at, updated_at, archived_at`

type typeDefRow struct {
	ID           ulid.ID      `db:"id"`
	TenantID     string       `db:"tenant_id"`
	Kind         string       `db:"kind"`
	InternalName string       `db:"internal_name"`
	DisplayName  string       `db:"display_name"`
	Description  string       `db:"description"`
	ExtendsID    ulid.ID      `db:"extends_id"`
	Version      int          `db:"version"`
	CreatedAt    time.Time    `db:"created_at"`
	UpdatedAt    time.Time    `db:"updated_at"`
	ArchivedAt   sql.NullTime `db:"archived_at"`
}

func (r typeDefRow) snapshot() domaintypedef.Snapshot {
	var extends *valueobjects.TypeDefinitionID
	if !r.ExtendsID.IsZero() {
		id := valueobjects.TypeDefinitionID{ID: r.ExtendsID}
		extends = &id
	}
	return domaintypedef.Snapshot{
		ID:           valueobjects.TypeDefinitionID{ID: r.ID},
		TenantID:     valueobjects.TenantID(r.TenantID),
		Kind:         domaintypedef.Kind(r.Kind),
		InternalName: r.InternalName,
		DisplayName:  r.DisplayName,
		Description:  r.Description,
		ExtendsID:    extends,
		Version:      r.Version,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		ArchivedAt:   timePtr(r.ArchivedAt),
	}
}

// nameKey batches by-internal-name lookups. Scope is the tenant for type
// definitions and the type definition ID for attributes.
type nameKey struct {
	Scope string
	Name  string
}

// typeDefListFilter is the cleansed, JSON-marshalled dataloader key for
// List queries: identical filters collapse to one key, and each unique key
// becomes one UNION ALL arm of the batch statement.
type typeDefListFilter struct {
	Tenant               string   `json:"tenant"`
	InternalNames        []string `json:"internal_names,omitempty"`
	IncludeArchived      bool     `json:"include_archived,omitempty"`
	IncludeAttributeSets bool     `json:"include_attribute_sets,omitempty"`
	Limit                int      `json:"limit"`
	Cursor               string   `json:"cursor,omitempty"`
}

func (f typeDefListFilter) key() string {
	sort.Strings(f.InternalNames)
	b, _ := json.Marshal(f)
	return string(b)
}

// where builds the filter predicates and args shared by the list arm and the
// count query.
func (f typeDefListFilter) where() ([]string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{f.Tenant}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if !f.IncludeAttributeSets {
		where = append(where, "kind = 'entity'")
	}
	if len(f.InternalNames) > 0 {
		where = append(where, "internal_name = ANY(?)")
		args = append(args, pq.Array(f.InternalNames))
	}
	return where, args
}

// arm renders this filter as one keyset UNION ALL arm with ? placeholders; the
// loader key rides along as a column so rows demultiplex after one round trip.
// It over-fetches one row so the caller can detect a next page.
func (f typeDefListFilter) arm(key string) (string, []any) {
	where, filterArgs := f.where()
	args := append([]any{key}, filterArgs...)
	where, args = keysetWhere(where, args, idKeyset, f.Cursor)
	args = append(args, f.Limit+1)

	query := `(SELECT ?::text AS loader_key, ` + typeDefColumns + `
	 FROM flexitype_type_definition
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ?)`
	return query, args
}

// countQuery counts the full filtered set, ignoring the keyset cursor.
func (f typeDefListFilter) countQuery() (string, []any) {
	where, args := f.where()
	return `SELECT count(*) FROM flexitype_type_definition WHERE ` + strings.Join(where, " AND "), args
}

type typeDefinitionRepository struct {
	q          db.QueryExecer
	inTx       bool
	byID       *dataloader.Loader[string, domaintypedef.Snapshot]
	byName     *dataloader.Loader[nameKey, domaintypedef.Snapshot]
	byList     *dataloader.Loader[string, pagedResult[domaintypedef.Snapshot]]
	byChildren *dataloader.Loader[string, []domaintypedef.Snapshot]
}

// NewTypeDefinitionRepository builds a dataloader-backed repository over
// the pool.
func NewTypeDefinitionRepository(q db.QueryExecer) domaintypedef.Repository {
	r := &typeDefinitionRepository{q: q}
	r.byID = newLoader(r.batchByID)
	r.byName = newLoader(r.batchByName)
	r.byList = newLoader(r.batchList)
	r.byChildren = newLoader(r.batchChildren)
	return r
}

// WithTx binds the repository to a transaction. Loader caches are bypassed
// so reads observe uncommitted writes.
func (r *typeDefinitionRepository) WithTx(tx db.Tx) domaintypedef.Repository {
	return &typeDefinitionRepository{q: txExecer(tx), inTx: true}
}

func (r *typeDefinitionRepository) batchByID(ctx context.Context, ids []string) (map[string]domaintypedef.Snapshot, error) {
	var rows []typeDefRow
	query := bind(`SELECT ` + typeDefColumns + ` FROM flexitype_type_definition WHERE id = ANY(?)`)
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
	tuples := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		tuples = append(tuples, "(?, ?)")
		args = append(args, k.Scope, k.Name)
	}
	query := bind(`SELECT ` + typeDefColumns + ` FROM flexitype_type_definition
	 WHERE archived_at IS NULL AND (tenant_id, internal_name) IN (` + strings.Join(tuples, ", ") + `)`)

	var rows []typeDefRow
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("batch type definitions by name: %w", err)
	}
	out := make(map[nameKey]domaintypedef.Snapshot, len(rows))
	for _, row := range rows {
		out[nameKey{Scope: row.TenantID, Name: row.InternalName}] = row.snapshot()
	}
	return out, nil
}

// batchList runs every unique filter key as one UNION ALL statement.
func (r *typeDefinitionRepository) batchList(ctx context.Context, keys []string) (map[string]pagedResult[domaintypedef.Snapshot], error) {
	arms := make([]string, 0, len(keys))
	var args []any
	for _, key := range keys {
		var f typeDefListFilter
		if err := json.Unmarshal([]byte(key), &f); err != nil {
			return nil, fmt.Errorf("decode list key: %w", err)
		}
		arm, armArgs := f.arm(key)
		arms = append(arms, arm)
		args = append(args, armArgs...)
	}

	var rows []struct {
		LoaderKey string `db:"loader_key"`
		typeDefRow
	}
	if err := r.q.SelectContext(ctx, &rows, bind(strings.Join(arms, "\nUNION ALL\n")), args...); err != nil {
		return nil, fmt.Errorf("batch list type definitions: %w", err)
	}

	out := make(map[string]pagedResult[domaintypedef.Snapshot], len(keys))
	for _, row := range rows {
		pr := out[row.LoaderKey]
		pr.Items = append(pr.Items, row.snapshot())
		out[row.LoaderKey] = pr
	}
	return out, nil
}

// batchChildren collapses direct-subtype loads into one ANY() query.
func (r *typeDefinitionRepository) batchChildren(ctx context.Context, parents []string) (map[string][]domaintypedef.Snapshot, error) {
	var rows []typeDefRow
	query := bind(`SELECT ` + typeDefColumns + ` FROM flexitype_type_definition
	 WHERE extends_id = ANY(?) AND archived_at IS NULL
	 ORDER BY internal_name`)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(parents)); err != nil {
		return nil, fmt.Errorf("batch type definition children: %w", err)
	}
	out := make(map[string][]domaintypedef.Snapshot, len(parents))
	for _, row := range rows {
		out[row.ExtendsID.String()] = append(out[row.ExtendsID.String()], row.snapshot())
	}
	return out, nil
}

// ListChildren loads the direct subtypes of a type.
func (r *typeDefinitionRepository) ListChildren(ctx context.Context, parentID valueobjects.TypeDefinitionID) ([]*domaintypedef.TypeDefinition, error) {
	var snaps []domaintypedef.Snapshot
	if r.inTx {
		fetched, err := r.batchChildren(ctx, []string{parentID.String()})
		if err != nil {
			return nil, err
		}
		snaps = fetched[parentID.String()]
	} else {
		var err error
		snaps, err = load(ctx, r.byChildren, parentID.String())
		if err != nil {
			return nil, err
		}
	}

	out := make([]*domaintypedef.TypeDefinition, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, domaintypedef.Rehydrate(snap))
	}
	return out, nil
}

func (r *typeDefinitionRepository) Get(ctx context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := load(ctx, r.byID, id.String())
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
	query := `SELECT ` + typeDefColumns + ` FROM flexitype_type_definition WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row typeDefRow
	if err := r.q.GetContext(ctx, &row, bind(query), id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, id.String())
		}
		return nil, fmt.Errorf("get type definition: %w", err)
	}
	return domaintypedef.Rehydrate(row.snapshot()), nil
}

func (r *typeDefinitionRepository) GetByInternalName(ctx context.Context, tenant valueobjects.TenantID, internalName string) (*domaintypedef.TypeDefinition, error) {
	if r.inTx {
		query := bind(`SELECT ` + typeDefColumns + ` FROM flexitype_type_definition
		 WHERE tenant_id = ? AND internal_name = ? AND archived_at IS NULL`)
		var row typeDefRow
		if err := r.q.GetContext(ctx, &row, query, tenant.String(), internalName); err != nil {
			if isNoRows(err) {
				return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, internalName)
			}
			return nil, fmt.Errorf("get type definition by name: %w", err)
		}
		return domaintypedef.Rehydrate(row.snapshot()), nil
	}

	snap, err := load(ctx, r.byName, nameKey{Scope: tenant.String(), Name: internalName})
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, internalName)
	}
	return domaintypedef.Rehydrate(snap), nil
}

func (r *typeDefinitionRepository) List(ctx context.Context, filter domaintypedef.Filter, page db.Page) ([]*domaintypedef.TypeDefinition, int, error) {
	f := typeDefListFilter{
		Tenant:               filter.TenantID.String(),
		InternalNames:        filter.InternalNames,
		IncludeArchived:      filter.IncludeArchived,
		IncludeAttributeSets: filter.IncludeAttributeSets,
		Limit:                page.Limit,
		Cursor:               page.Cursor,
	}
	key := f.key()

	var result pagedResult[domaintypedef.Snapshot]
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

	out := make([]*domaintypedef.TypeDefinition, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domaintypedef.Rehydrate(snap))
	}
	total, err := countIf(ctx, r.q, page.WantTotal, f.countQuery)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// extendsParam maps an optional parent pointer to its driver argument.
func extendsParam(id *valueobjects.TypeDefinitionID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

func (r *typeDefinitionRepository) Save(ctx context.Context, t *domaintypedef.TypeDefinition) error {
	s := t.Snapshot()
	_, err := r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_type_definition
		   (id, tenant_id, kind, internal_name, display_name, description, extends_id, version, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   display_name = EXCLUDED.display_name,
		   description  = EXCLUDED.description,
		   version      = EXCLUDED.version,
		   updated_at   = EXCLUDED.updated_at,
		   archived_at  = EXCLUDED.archived_at`),
		s.ID.String(), s.TenantID.String(), string(s.Kind), s.InternalName, s.DisplayName,
		s.Description, extendsParam(s.ExtendsID), s.Version, s.CreatedAt, s.UpdatedAt,
		nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save type definition: %w", err)
	}
	return nil
}
