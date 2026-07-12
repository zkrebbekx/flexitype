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

	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

const dependencyColumns = `id, tenant_id, source_attribute_id, target_attribute_id, conditions,
	effect, description, version, created_at, updated_at, archived_at`

type dependencyRow struct {
	ID                ulid.ID      `db:"id"`
	TenantID          string       `db:"tenant_id"`
	SourceAttributeID ulid.ID      `db:"source_attribute_id"`
	TargetAttributeID ulid.ID      `db:"target_attribute_id"`
	Conditions        []byte       `db:"conditions"`
	Effect            []byte       `db:"effect"`
	Description       string       `db:"description"`
	Version           int          `db:"version"`
	CreatedAt         time.Time    `db:"created_at"`
	UpdatedAt         time.Time    `db:"updated_at"`
	ArchivedAt        sql.NullTime `db:"archived_at"`
}

func (r dependencyRow) snapshot() (domaindependency.Snapshot, error) {
	var conditions []domaindependency.Condition
	if len(r.Conditions) > 0 {
		if err := json.Unmarshal(r.Conditions, &conditions); err != nil {
			return domaindependency.Snapshot{}, fmt.Errorf("decode conditions for %s: %w", r.ID, err)
		}
	}
	var effect domaindependency.Effect
	if len(r.Effect) > 0 {
		if err := json.Unmarshal(r.Effect, &effect); err != nil {
			return domaindependency.Snapshot{}, fmt.Errorf("decode effect for %s: %w", r.ID, err)
		}
	}
	return domaindependency.Snapshot{
		ID:                valueobjects.DependencyID{ID: r.ID},
		TenantID:          valueobjects.TenantID(r.TenantID),
		SourceAttributeID: valueobjects.AttributeDefinitionID{ID: r.SourceAttributeID},
		TargetAttributeID: valueobjects.AttributeDefinitionID{ID: r.TargetAttributeID},
		Conditions:        conditions,
		Effect:            effect,
		Description:       r.Description,
		Version:           r.Version,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
		ArchivedAt:        timePtr(r.ArchivedAt),
	}, nil
}

// dependencyListFilter is the cleansed JSON dataloader key for dependency
// List queries; unique keys become UNION ALL arms.
type dependencyListFilter struct {
	Tenant          string `json:"tenant"`
	SourceID        string `json:"source_attribute_id,omitempty"`
	TargetID        string `json:"target_attribute_id,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit"`
	Cursor          string `json:"cursor,omitempty"`
}

func (f dependencyListFilter) key() string {
	b, _ := json.Marshal(f)
	return string(b)
}

func (f dependencyListFilter) where() ([]string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{f.Tenant}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if f.SourceID != "" {
		where = append(where, "source_attribute_id = ?")
		args = append(args, f.SourceID)
	}
	if f.TargetID != "" {
		where = append(where, "target_attribute_id = ?")
		args = append(args, f.TargetID)
	}
	return where, args
}

func (f dependencyListFilter) arm(key string) (string, []any) {
	where, filterArgs := f.where()
	args := append([]any{key}, filterArgs...)
	where, args = keysetWhere(where, args, idKeyset, f.Cursor)
	args = append(args, f.Limit+1)

	query := `(SELECT ?::text AS loader_key, ` + dependencyColumns + `
	 FROM flexitype_attribute_value_dependency
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ?)`
	return query, args
}

func (f dependencyListFilter) countQuery() (string, []any) {
	where, args := f.where()
	return `SELECT count(*) FROM flexitype_attribute_value_dependency WHERE ` + strings.Join(where, " AND "), args
}

type dependencyRepository struct {
	q        db.QueryExecer
	inTx     bool
	byID     *dataloader.Loader[string, domaindependency.Snapshot]
	byTarget *dataloader.Loader[string, []domaindependency.Snapshot]
	bySource *dataloader.Loader[string, []domaindependency.Snapshot]
	byList   *dataloader.Loader[string, pagedResult[domaindependency.Snapshot]]
}

// NewDependencyRepository builds a dataloader-backed repository over the
// pool.
func NewDependencyRepository(q db.QueryExecer) domaindependency.Repository {
	r := &dependencyRepository{q: q}
	r.byID = newLoader(r.batchByID)
	r.byTarget = newLoader(r.batchByColumn("target_attribute_id"))
	r.bySource = newLoader(r.batchByColumn("source_attribute_id"))
	r.byList = newLoader(r.batchList)
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *dependencyRepository) WithTx(tx db.QueryExecer) domaindependency.Repository {
	return &dependencyRepository{q: tx, inTx: true}
}

func (r *dependencyRepository) batchByID(ctx context.Context, ids []string) (map[string]domaindependency.Snapshot, error) {
	var rows []dependencyRow
	query := bind(`SELECT ` + dependencyColumns + ` FROM flexitype_attribute_value_dependency WHERE id = ANY(?)`)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("batch dependencies by id: %w", err)
	}
	out := make(map[string]domaindependency.Snapshot, len(rows))
	for _, row := range rows {
		snap, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		out[row.ID.String()] = snap
	}
	return out, nil
}

// batchByColumn builds a batch loader keyed on one attribute column
// (source or target) — both sides share the same query shape.
func (r *dependencyRepository) batchByColumn(column string) mapBatchFunc[string, []domaindependency.Snapshot] {
	return func(ctx context.Context, ids []string) (map[string][]domaindependency.Snapshot, error) {
		query := bind(`SELECT ` + dependencyColumns + ` FROM flexitype_attribute_value_dependency
		 WHERE ` + column + ` = ANY(?) AND archived_at IS NULL
		 ORDER BY id`)
		var rows []dependencyRow
		if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
			return nil, fmt.Errorf("batch dependencies by %s: %w", column, err)
		}

		out := make(map[string][]domaindependency.Snapshot, len(ids))
		for _, row := range rows {
			snap, err := row.snapshot()
			if err != nil {
				return nil, err
			}
			key := row.TargetAttributeID.String()
			if column == "source_attribute_id" {
				key = row.SourceAttributeID.String()
			}
			out[key] = append(out[key], snap)
		}
		return out, nil
	}
}

// batchList runs every unique filter key as one UNION ALL statement.
func (r *dependencyRepository) batchList(ctx context.Context, keys []string) (map[string]pagedResult[domaindependency.Snapshot], error) {
	arms := make([]string, 0, len(keys))
	var args []any
	for _, key := range keys {
		var f dependencyListFilter
		if err := json.Unmarshal([]byte(key), &f); err != nil {
			return nil, fmt.Errorf("decode list key: %w", err)
		}
		arm, armArgs := f.arm(key)
		arms = append(arms, arm)
		args = append(args, armArgs...)
	}

	var rows []struct {
		LoaderKey string `db:"loader_key"`
		dependencyRow
	}
	if err := r.q.SelectContext(ctx, &rows, bind(strings.Join(arms, "\nUNION ALL\n")), args...); err != nil {
		return nil, fmt.Errorf("batch list dependencies: %w", err)
	}

	out := make(map[string]pagedResult[domaindependency.Snapshot], len(keys))
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

func (r *dependencyRepository) Get(ctx context.Context, id valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := load(ctx, r.byID, id.String())
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domaindependency.AggregateType, id.String())
	}
	return domaindependency.Rehydrate(snap), nil
}

func (r *dependencyRepository) GetForUpdate(ctx context.Context, id valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	if !r.inTx {
		return nil, fmt.Errorf("dependency repository: GetForUpdate requires a transaction")
	}
	return r.getDirect(ctx, id, true)
}

func (r *dependencyRepository) getDirect(ctx context.Context, id valueobjects.DependencyID, forUpdate bool) (*domaindependency.Dependency, error) {
	query := `SELECT ` + dependencyColumns + ` FROM flexitype_attribute_value_dependency WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row dependencyRow
	if err := r.q.GetContext(ctx, &row, bind(query), id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domaindependency.AggregateType, id.String())
		}
		return nil, fmt.Errorf("get dependency: %w", err)
	}
	snap, err := row.snapshot()
	if err != nil {
		return nil, err
	}
	return domaindependency.Rehydrate(snap), nil
}

func (r *dependencyRepository) ListByTarget(ctx context.Context, targetID valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	return r.listByColumn(ctx, r.byTarget, "target_attribute_id", targetID.String())
}

func (r *dependencyRepository) ListBySource(ctx context.Context, sourceID valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	return r.listByColumn(ctx, r.bySource, "source_attribute_id", sourceID.String())
}

func (r *dependencyRepository) listByColumn(ctx context.Context, loader *dataloader.Loader[string, []domaindependency.Snapshot], column, id string) ([]*domaindependency.Dependency, error) {
	var snaps []domaindependency.Snapshot
	if r.inTx {
		fetched, err := r.batchByColumn(column)(ctx, []string{id})
		if err != nil {
			return nil, err
		}
		snaps = fetched[id]
	} else {
		var err error
		snaps, err = load(ctx, loader, id)
		if err != nil {
			return nil, err
		}
	}

	out := make([]*domaindependency.Dependency, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, domaindependency.Rehydrate(snap))
	}
	return out, nil
}

func (r *dependencyRepository) List(ctx context.Context, filter domaindependency.Filter, page db.Page) ([]*domaindependency.Dependency, int, error) {
	f := dependencyListFilter{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Cursor:          page.Cursor,
	}
	if !filter.SourceAttributeID.IsZero() {
		f.SourceID = filter.SourceAttributeID.String()
	}
	if !filter.TargetAttributeID.IsZero() {
		f.TargetID = filter.TargetAttributeID.String()
	}
	key := f.key()

	var result pagedResult[domaindependency.Snapshot]
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

	out := make([]*domaindependency.Dependency, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domaindependency.Rehydrate(snap))
	}
	total, err := countIf(ctx, r.q, page.WantTotal, f.countQuery)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *dependencyRepository) Save(ctx context.Context, d *domaindependency.Dependency) error {
	s := d.Snapshot()

	conditions, err := json.Marshal(s.Conditions)
	if err != nil {
		return fmt.Errorf("encode conditions: %w", err)
	}
	effect, err := json.Marshal(s.Effect)
	if err != nil {
		return fmt.Errorf("encode effect: %w", err)
	}

	_, err = r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_attribute_value_dependency
		   (id, tenant_id, source_attribute_id, target_attribute_id, conditions, effect,
		    description, version, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   conditions  = EXCLUDED.conditions,
		   effect      = EXCLUDED.effect,
		   description = EXCLUDED.description,
		   version     = EXCLUDED.version,
		   updated_at  = EXCLUDED.updated_at,
		   archived_at = EXCLUDED.archived_at`),
		s.ID.String(), s.TenantID.String(), s.SourceAttributeID.String(),
		s.TargetAttributeID.String(), jsonbParam(conditions), jsonbParam(effect),
		s.Description, s.Version, s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save dependency: %w", err)
	}
	return nil
}
