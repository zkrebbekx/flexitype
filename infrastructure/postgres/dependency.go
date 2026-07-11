package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/dataloader"
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

// dependencyListKey batches List queries.
type dependencyListKey struct {
	Tenant          string
	SourceID        string
	TargetID        string
	IncludeArchived bool
	Limit           int
	Offset          int
}

type dependencyRepository struct {
	q        db.QueryExecer
	inTx     bool
	byID     *dataloader.Loader[string, domaindependency.Snapshot]
	byTarget *dataloader.Loader[string, []domaindependency.Snapshot]
	bySource *dataloader.Loader[string, []domaindependency.Snapshot]
	byList   *dataloader.Loader[dependencyListKey, dataloader.PagedResult[domaindependency.Snapshot]]
}

// NewDependencyRepository builds a dataloader-backed repository over the
// pool.
func NewDependencyRepository(q db.QueryExecer) domaindependency.Repository {
	r := &dependencyRepository{q: q}
	r.byID = dataloader.NewZeroLoader(r.batchByID, loaderConfig())
	r.byTarget = dataloader.NewSliceLoader(r.batchByColumn("target_attribute_id"), loaderConfig())
	r.bySource = dataloader.NewSliceLoader(r.batchByColumn("source_attribute_id"), loaderConfig())
	r.byList = dataloader.NewSliceLoader(r.batchList, loaderConfig())
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *dependencyRepository) WithTx(tx db.QueryExecer) domaindependency.Repository {
	return &dependencyRepository{q: tx, inTx: true}
}

func (r *dependencyRepository) batchByID(ctx context.Context, ids []string) (map[string]domaindependency.Snapshot, error) {
	var rows []dependencyRow
	query := fmt.Sprintf(`SELECT %s FROM flexitype_attribute_value_dependency WHERE id = ANY($1)`, dependencyColumns)
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
func (r *dependencyRepository) batchByColumn(column string) dataloader.BatchFunc[string, []domaindependency.Snapshot] {
	return func(ctx context.Context, ids []string) (map[string][]domaindependency.Snapshot, error) {
		query := fmt.Sprintf(
			`SELECT %s FROM flexitype_attribute_value_dependency
			 WHERE %s = ANY($1) AND archived_at IS NULL
			 ORDER BY id`,
			dependencyColumns, column,
		)
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

func (r *dependencyRepository) batchList(ctx context.Context, keys []dependencyListKey) (map[dependencyListKey]dataloader.PagedResult[domaindependency.Snapshot], error) {
	out := make(map[dependencyListKey]dataloader.PagedResult[domaindependency.Snapshot], len(keys))
	for _, key := range keys {
		items, total, err := r.queryList(ctx, key)
		if err != nil {
			return nil, err
		}
		out[key] = dataloader.PagedResult[domaindependency.Snapshot]{Items: items, Total: total}
	}
	return out, nil
}

func (r *dependencyRepository) queryList(ctx context.Context, key dependencyListKey) ([]domaindependency.Snapshot, int, error) {
	where := []string{"tenant_id = $1"}
	args := []any{key.Tenant}
	if !key.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if key.SourceID != "" {
		args = append(args, key.SourceID)
		where = append(where, fmt.Sprintf("source_attribute_id = $%d", len(args)))
	}
	if key.TargetID != "" {
		args = append(args, key.TargetID)
		where = append(where, fmt.Sprintf("target_attribute_id = $%d", len(args)))
	}
	args = append(args, key.Limit, key.Offset)

	query := fmt.Sprintf(
		`SELECT %s, count(*) OVER () AS total_count
		 FROM flexitype_attribute_value_dependency
		 WHERE %s
		 ORDER BY id
		 LIMIT $%d OFFSET $%d`,
		dependencyColumns, strings.Join(where, " AND "), len(args)-1, len(args),
	)

	var rows []struct {
		dependencyRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, fmt.Errorf("list dependencies: %w", err)
	}

	items := make([]domaindependency.Snapshot, 0, len(rows))
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

func (r *dependencyRepository) Get(ctx context.Context, id valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := r.byID.Load(ctx, id.String())
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
	query := fmt.Sprintf(`SELECT %s FROM flexitype_attribute_value_dependency WHERE id = $1`, dependencyColumns)
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row dependencyRow
	if err := r.q.GetContext(ctx, &row, query, id.String()); err != nil {
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
	var err error
	if r.inTx {
		fetched, ferr := r.batchByColumn(column)(ctx, []string{id})
		if ferr != nil {
			return nil, ferr
		}
		snaps = fetched[id]
	} else {
		snaps, err = loader.Load(ctx, id)
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
	key := dependencyListKey{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if !filter.SourceAttributeID.IsZero() {
		key.SourceID = filter.SourceAttributeID.String()
	}
	if !filter.TargetAttributeID.IsZero() {
		key.TargetID = filter.TargetAttributeID.String()
	}

	var result dataloader.PagedResult[domaindependency.Snapshot]
	var err error
	if r.inTx {
		var items []domaindependency.Snapshot
		var total int
		items, total, err = r.queryList(ctx, key)
		result = dataloader.PagedResult[domaindependency.Snapshot]{Items: items, Total: total}
	} else {
		result, err = r.byList.Load(ctx, key)
	}
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domaindependency.Dependency, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domaindependency.Rehydrate(snap))
	}
	return out, result.Total, nil
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

	_, err = r.q.ExecContext(ctx,
		`INSERT INTO flexitype_attribute_value_dependency
		   (id, tenant_id, source_attribute_id, target_attribute_id, conditions, effect,
		    description, version, created_at, updated_at, archived_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (id) DO UPDATE SET
		   conditions  = EXCLUDED.conditions,
		   effect      = EXCLUDED.effect,
		   description = EXCLUDED.description,
		   version     = EXCLUDED.version,
		   updated_at  = EXCLUDED.updated_at,
		   archived_at = EXCLUDED.archived_at`,
		s.ID.String(), s.TenantID.String(), s.SourceAttributeID.String(),
		s.TargetAttributeID.String(), jsonbParam(conditions), jsonbParam(effect),
		s.Description, s.Version, s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save dependency: %w", err)
	}
	return nil
}
