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
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// --- relationship definitions -------------------------------------------------

const relDefColumns = `id, tenant_id, internal_name, display_name, description, kind, parent_type_id,
	child_type_id, parent_label, child_label, attribute_set_id, extends_id, parent_version_policy,
	child_version_policy, version, created_at, updated_at, archived_at`

type relDefRow struct {
	ID                  ulid.ID      `db:"id"`
	TenantID            string       `db:"tenant_id"`
	InternalName        string       `db:"internal_name"`
	DisplayName         string       `db:"display_name"`
	Description         string       `db:"description"`
	Kind                string       `db:"kind"`
	ParentTypeID        ulid.ID      `db:"parent_type_id"`
	ChildTypeID         ulid.ID      `db:"child_type_id"`
	ParentLabel         string       `db:"parent_label"`
	ChildLabel          string       `db:"child_label"`
	AttributeSetID      ulid.ID      `db:"attribute_set_id"`
	ExtendsID           ulid.ID      `db:"extends_id"`
	ParentVersionPolicy string       `db:"parent_version_policy"`
	ChildVersionPolicy  string       `db:"child_version_policy"`
	Version             int          `db:"version"`
	CreatedAt           time.Time    `db:"created_at"`
	UpdatedAt           time.Time    `db:"updated_at"`
	ArchivedAt          sql.NullTime `db:"archived_at"`
}

func (r relDefRow) snapshot() domainrelationship.DefinitionSnapshot {
	var extends *valueobjects.RelationshipDefinitionID
	if !r.ExtendsID.IsZero() {
		id := valueobjects.RelationshipDefinitionID{ID: r.ExtendsID}
		extends = &id
	}
	return domainrelationship.DefinitionSnapshot{
		ID:                  valueobjects.RelationshipDefinitionID{ID: r.ID},
		TenantID:            valueobjects.TenantID(r.TenantID),
		InternalName:        r.InternalName,
		DisplayName:         r.DisplayName,
		Description:         r.Description,
		Kind:                domainrelationship.Kind(r.Kind),
		ParentTypeID:        valueobjects.TypeDefinitionID{ID: r.ParentTypeID},
		ChildTypeID:         valueobjects.TypeDefinitionID{ID: r.ChildTypeID},
		ParentLabel:         r.ParentLabel,
		ChildLabel:          r.ChildLabel,
		AttributeSetID:      valueobjects.TypeDefinitionID{ID: r.AttributeSetID},
		ExtendsID:           extends,
		ParentVersionPolicy: domainrelationship.VersionPolicy(r.ParentVersionPolicy),
		ChildVersionPolicy:  domainrelationship.VersionPolicy(r.ChildVersionPolicy),
		Version:             r.Version,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
		ArchivedAt:          timePtr(r.ArchivedAt),
	}
}

// relDefListFilter is the cleansed JSON dataloader key for definition List
// queries; unique keys become UNION ALL arms.
type relDefListFilter struct {
	Tenant          string   `json:"tenant"`
	TypeIDs         []string `json:"type_definition_ids,omitempty"`
	IncludeArchived bool     `json:"include_archived,omitempty"`
	Limit           int      `json:"limit"`
	Offset          int      `json:"offset"`
}

func (f relDefListFilter) key() string {
	sort.Strings(f.TypeIDs)
	b, _ := json.Marshal(f)
	return string(b)
}

func (f relDefListFilter) arm(key string) (string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{key, f.Tenant}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if len(f.TypeIDs) > 0 {
		where = append(where, "(parent_type_id = ANY(?) OR child_type_id = ANY(?))")
		args = append(args, pq.Array(f.TypeIDs), pq.Array(f.TypeIDs))
	}
	args = append(args, f.Limit, f.Offset)

	query := `(SELECT ?::text AS loader_key, ` + relDefColumns + `, count(*) OVER () AS total_count
	 FROM flexitype_relationship_definition
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ? OFFSET ?)`
	return query, args
}

type relationshipDefinitionRepository struct {
	q      db.QueryExecer
	inTx   bool
	byID   *dataloader.Loader[string, domainrelationship.DefinitionSnapshot]
	byList *dataloader.Loader[string, pagedResult[domainrelationship.DefinitionSnapshot]]
}

// NewRelationshipDefinitionRepository builds a dataloader-backed repository
// over the pool.
func NewRelationshipDefinitionRepository(q db.QueryExecer) domainrelationship.DefinitionRepository {
	r := &relationshipDefinitionRepository{q: q}
	r.byID = newLoader(r.batchByID)
	r.byList = newLoader(r.batchList)
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *relationshipDefinitionRepository) WithTx(tx db.QueryExecer) domainrelationship.DefinitionRepository {
	return &relationshipDefinitionRepository{q: tx, inTx: true}
}

func (r *relationshipDefinitionRepository) batchByID(ctx context.Context, ids []string) (map[string]domainrelationship.DefinitionSnapshot, error) {
	var rows []relDefRow
	query := bind(`SELECT ` + relDefColumns + ` FROM flexitype_relationship_definition WHERE id = ANY(?)`)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("batch relationship definitions by id: %w", err)
	}
	out := make(map[string]domainrelationship.DefinitionSnapshot, len(rows))
	for _, row := range rows {
		out[row.ID.String()] = row.snapshot()
	}
	return out, nil
}

func (r *relationshipDefinitionRepository) batchList(ctx context.Context, keys []string) (map[string]pagedResult[domainrelationship.DefinitionSnapshot], error) {
	arms := make([]string, 0, len(keys))
	var args []any
	for _, key := range keys {
		var f relDefListFilter
		if err := json.Unmarshal([]byte(key), &f); err != nil {
			return nil, fmt.Errorf("decode list key: %w", err)
		}
		arm, armArgs := f.arm(key)
		arms = append(arms, arm)
		args = append(args, armArgs...)
	}

	var rows []struct {
		LoaderKey string `db:"loader_key"`
		relDefRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, bind(strings.Join(arms, "\nUNION ALL\n")), args...); err != nil {
		return nil, fmt.Errorf("batch list relationship definitions: %w", err)
	}

	out := make(map[string]pagedResult[domainrelationship.DefinitionSnapshot], len(keys))
	for _, row := range rows {
		pr := out[row.LoaderKey]
		pr.Items = append(pr.Items, row.snapshot())
		pr.Total = row.TotalCount
		out[row.LoaderKey] = pr
	}
	return out, nil
}

func (r *relationshipDefinitionRepository) Get(ctx context.Context, id valueobjects.RelationshipDefinitionID) (*domainrelationship.Definition, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := load(ctx, r.byID, id.String())
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domainrelationship.DefinitionAggregateType, id.String())
	}
	return domainrelationship.RehydrateDefinition(snap), nil
}

func (r *relationshipDefinitionRepository) GetForUpdate(ctx context.Context, id valueobjects.RelationshipDefinitionID) (*domainrelationship.Definition, error) {
	if !r.inTx {
		return nil, fmt.Errorf("relationship definition repository: GetForUpdate requires a transaction")
	}
	return r.getDirect(ctx, id, true)
}

func (r *relationshipDefinitionRepository) getDirect(ctx context.Context, id valueobjects.RelationshipDefinitionID, forUpdate bool) (*domainrelationship.Definition, error) {
	query := `SELECT ` + relDefColumns + ` FROM flexitype_relationship_definition WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row relDefRow
	if err := r.q.GetContext(ctx, &row, bind(query), id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domainrelationship.DefinitionAggregateType, id.String())
		}
		return nil, fmt.Errorf("get relationship definition: %w", err)
	}
	return domainrelationship.RehydrateDefinition(row.snapshot()), nil
}

func (r *relationshipDefinitionRepository) GetByInternalName(ctx context.Context, tenant valueobjects.TenantID, internalName string) (*domainrelationship.Definition, error) {
	query := bind(`SELECT ` + relDefColumns + ` FROM flexitype_relationship_definition
	 WHERE tenant_id = ? AND internal_name = ? AND archived_at IS NULL`)
	var row relDefRow
	if err := r.q.GetContext(ctx, &row, query, tenant.String(), internalName); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domainrelationship.DefinitionAggregateType, internalName)
		}
		return nil, fmt.Errorf("get relationship definition by name: %w", err)
	}
	return domainrelationship.RehydrateDefinition(row.snapshot()), nil
}

func (r *relationshipDefinitionRepository) List(ctx context.Context, filter domainrelationship.DefinitionFilter, page db.Page) ([]*domainrelationship.Definition, int, error) {
	f := relDefListFilter{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	for _, id := range filter.TypeDefinitionIDs {
		f.TypeIDs = append(f.TypeIDs, id.String())
	}
	key := f.key()

	var result pagedResult[domainrelationship.DefinitionSnapshot]
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

	out := make([]*domainrelationship.Definition, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domainrelationship.RehydrateDefinition(snap))
	}
	return out, result.Total, nil
}

func (r *relationshipDefinitionRepository) Save(ctx context.Context, d *domainrelationship.Definition) error {
	s := d.Snapshot()
	var extends any
	if s.ExtendsID != nil {
		extends = s.ExtendsID.String()
	}
	_, err := r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_relationship_definition
		   (id, tenant_id, internal_name, display_name, description, kind, parent_type_id, child_type_id,
		    parent_label, child_label, attribute_set_id, extends_id, parent_version_policy,
		    child_version_policy, version, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   display_name          = EXCLUDED.display_name,
		   description           = EXCLUDED.description,
		   parent_label          = EXCLUDED.parent_label,
		   child_label           = EXCLUDED.child_label,
		   parent_version_policy = EXCLUDED.parent_version_policy,
		   child_version_policy  = EXCLUDED.child_version_policy,
		   version               = EXCLUDED.version,
		   updated_at            = EXCLUDED.updated_at,
		   archived_at           = EXCLUDED.archived_at`),
		s.ID.String(), s.TenantID.String(), s.InternalName, s.DisplayName, s.Description,
		string(s.Kind), s.ParentTypeID.String(), s.ChildTypeID.String(), s.ParentLabel, s.ChildLabel,
		s.AttributeSetID.String(), extends,
		string(s.ParentVersionPolicy), string(s.ChildVersionPolicy), s.Version,
		s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save relationship definition: %w", err)
	}
	return nil
}

// --- relationship instances ---------------------------------------------------

const relColumns = `id, tenant_id, relationship_definition_id, parent_entity_id, child_entity_id,
	parent_type_version, child_type_version, created_at, updated_at, archived_at`

type relRow struct {
	ID             ulid.ID       `db:"id"`
	TenantID       string        `db:"tenant_id"`
	DefinitionID   ulid.ID       `db:"relationship_definition_id"`
	ParentEntityID string        `db:"parent_entity_id"`
	ChildEntityID  string        `db:"child_entity_id"`
	ParentVersion  sql.NullInt64 `db:"parent_type_version"`
	ChildVersion   sql.NullInt64 `db:"child_type_version"`
	CreatedAt      time.Time     `db:"created_at"`
	UpdatedAt      time.Time     `db:"updated_at"`
	ArchivedAt     sql.NullTime  `db:"archived_at"`
}

func (r relRow) snapshot() domainrelationship.Snapshot {
	return domainrelationship.Snapshot{
		ID:             valueobjects.RelationshipID{ID: r.ID},
		TenantID:       valueobjects.TenantID(r.TenantID),
		DefinitionID:   valueobjects.RelationshipDefinitionID{ID: r.DefinitionID},
		ParentEntityID: valueobjects.EntityID(r.ParentEntityID),
		ChildEntityID:  valueobjects.EntityID(r.ChildEntityID),
		ParentVersion:  intPtr(r.ParentVersion),
		ChildVersion:   intPtr(r.ChildVersion),
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
		ArchivedAt:     timePtr(r.ArchivedAt),
	}
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// relListFilter is the cleansed JSON dataloader key for relationship List
// queries; unique keys become UNION ALL arms.
type relListFilter struct {
	Tenant          string `json:"tenant"`
	DefinitionID    string `json:"relationship_definition_id,omitempty"`
	ParentEntityID  string `json:"parent_entity_id,omitempty"`
	ChildEntityID   string `json:"child_entity_id,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit"`
	Offset          int    `json:"offset"`
}

func (f relListFilter) key() string {
	b, _ := json.Marshal(f)
	return string(b)
}

func (f relListFilter) arm(key string) (string, []any) {
	where := []string{"tenant_id = ?"}
	args := []any{key, f.Tenant}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if f.DefinitionID != "" {
		where = append(where, "relationship_definition_id = ?")
		args = append(args, f.DefinitionID)
	}
	if f.ParentEntityID != "" {
		where = append(where, "parent_entity_id = ?")
		args = append(args, f.ParentEntityID)
	}
	if f.ChildEntityID != "" {
		where = append(where, "child_entity_id = ?")
		args = append(args, f.ChildEntityID)
	}
	args = append(args, f.Limit, f.Offset)

	query := `(SELECT ?::text AS loader_key, ` + relColumns + `, count(*) OVER () AS total_count
	 FROM flexitype_relationship
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ? OFFSET ?)`
	return query, args
}

// relEntityKey is the comparable projection of
// domainrelationship.EntityLinksKey.
type relEntityKey struct {
	Tenant   string
	EntityID string
}

type relationshipRepository struct {
	q        db.QueryExecer
	inTx     bool
	byID     *dataloader.Loader[string, domainrelationship.Snapshot]
	byEntity *dataloader.Loader[relEntityKey, []domainrelationship.Snapshot]
	byList   *dataloader.Loader[string, pagedResult[domainrelationship.Snapshot]]
}

// NewRelationshipRepository builds a dataloader-backed repository over the
// pool.
func NewRelationshipRepository(q db.QueryExecer) domainrelationship.Repository {
	r := &relationshipRepository{q: q}
	r.byID = newLoader(r.batchByID)
	r.byEntity = newLoader(r.batchByEntity)
	r.byList = newLoader(r.batchList)
	return r
}

// WithTx binds the repository to a transaction, bypassing loader caches.
func (r *relationshipRepository) WithTx(tx db.QueryExecer) domainrelationship.Repository {
	return &relationshipRepository{q: tx, inTx: true}
}

func (r *relationshipRepository) batchByID(ctx context.Context, ids []string) (map[string]domainrelationship.Snapshot, error) {
	var rows []relRow
	query := bind(`SELECT ` + relColumns + ` FROM flexitype_relationship WHERE id = ANY(?)`)
	if err := r.q.SelectContext(ctx, &rows, query, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("batch relationships by id: %w", err)
	}
	out := make(map[string]domainrelationship.Snapshot, len(rows))
	for _, row := range rows {
		out[row.ID.String()] = row.snapshot()
	}
	return out, nil
}

// batchByEntity collapses per-entity link loads into one query covering
// both sides.
func (r *relationshipRepository) batchByEntity(ctx context.Context, keys []relEntityKey) (map[relEntityKey][]domainrelationship.Snapshot, error) {
	tuples := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		tuples = append(tuples, "(?, ?)")
		args = append(args, k.Tenant, k.EntityID)
	}
	tupleList := strings.Join(tuples, ", ")
	query := bind(`SELECT ` + relColumns + ` FROM flexitype_relationship
	 WHERE archived_at IS NULL
	   AND ((tenant_id, parent_entity_id) IN (` + tupleList + `)
	     OR (tenant_id, child_entity_id) IN (` + tupleList + `))
	 ORDER BY id`)
	args = append(args, args...) // the tuple list appears twice

	var rows []relRow
	if err := r.q.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("batch relationships by entity: %w", err)
	}

	out := make(map[relEntityKey][]domainrelationship.Snapshot, len(keys))
	wanted := make(map[relEntityKey]struct{}, len(keys))
	for _, k := range keys {
		wanted[k] = struct{}{}
	}
	for _, row := range rows {
		snap := row.snapshot()
		parentKey := relEntityKey{Tenant: row.TenantID, EntityID: row.ParentEntityID}
		childKey := relEntityKey{Tenant: row.TenantID, EntityID: row.ChildEntityID}
		if _, ok := wanted[parentKey]; ok {
			out[parentKey] = append(out[parentKey], snap)
		}
		if _, ok := wanted[childKey]; ok && childKey != parentKey {
			out[childKey] = append(out[childKey], snap)
		}
	}
	return out, nil
}

// batchList runs every unique filter key as one UNION ALL statement.
func (r *relationshipRepository) batchList(ctx context.Context, keys []string) (map[string]pagedResult[domainrelationship.Snapshot], error) {
	arms := make([]string, 0, len(keys))
	var args []any
	for _, key := range keys {
		var f relListFilter
		if err := json.Unmarshal([]byte(key), &f); err != nil {
			return nil, fmt.Errorf("decode list key: %w", err)
		}
		arm, armArgs := f.arm(key)
		arms = append(arms, arm)
		args = append(args, armArgs...)
	}

	var rows []struct {
		LoaderKey string `db:"loader_key"`
		relRow
		TotalCount int `db:"total_count"`
	}
	if err := r.q.SelectContext(ctx, &rows, bind(strings.Join(arms, "\nUNION ALL\n")), args...); err != nil {
		return nil, fmt.Errorf("batch list relationships: %w", err)
	}

	out := make(map[string]pagedResult[domainrelationship.Snapshot], len(keys))
	for _, row := range rows {
		pr := out[row.LoaderKey]
		pr.Items = append(pr.Items, row.snapshot())
		pr.Total = row.TotalCount
		out[row.LoaderKey] = pr
	}
	return out, nil
}

func (r *relationshipRepository) Get(ctx context.Context, id valueobjects.RelationshipID) (*domainrelationship.Relationship, error) {
	if r.inTx {
		return r.getDirect(ctx, id, false)
	}
	snap, err := load(ctx, r.byID, id.String())
	if err != nil {
		return nil, err
	}
	if snap.ID.IsZero() {
		return nil, domainerrors.NewNotFound(domainrelationship.AggregateType, id.String())
	}
	return domainrelationship.Rehydrate(snap), nil
}

func (r *relationshipRepository) GetForUpdate(ctx context.Context, id valueobjects.RelationshipID) (*domainrelationship.Relationship, error) {
	if !r.inTx {
		return nil, fmt.Errorf("relationship repository: GetForUpdate requires a transaction")
	}
	return r.getDirect(ctx, id, true)
}

func (r *relationshipRepository) getDirect(ctx context.Context, id valueobjects.RelationshipID, forUpdate bool) (*domainrelationship.Relationship, error) {
	query := `SELECT ` + relColumns + ` FROM flexitype_relationship WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row relRow
	if err := r.q.GetContext(ctx, &row, bind(query), id.String()); err != nil {
		if isNoRows(err) {
			return nil, domainerrors.NewNotFound(domainrelationship.AggregateType, id.String())
		}
		return nil, fmt.Errorf("get relationship: %w", err)
	}
	return domainrelationship.Rehydrate(row.snapshot()), nil
}

func (r *relationshipRepository) FindLive(ctx context.Context, defID valueobjects.RelationshipDefinitionID, parent, child valueobjects.EntityID) (*domainrelationship.Relationship, error) {
	query := bind(`SELECT ` + relColumns + ` FROM flexitype_relationship
	 WHERE relationship_definition_id = ? AND parent_entity_id = ? AND child_entity_id = ?
	   AND archived_at IS NULL`)
	var row relRow
	if err := r.q.GetContext(ctx, &row, query, defID.String(), parent.String(), child.String()); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("find live relationship: %w", err)
	}
	rel := domainrelationship.Rehydrate(row.snapshot())
	return rel, nil
}

func (r *relationshipRepository) ListByEntity(ctx context.Context, key domainrelationship.EntityLinksKey) ([]*domainrelationship.Relationship, error) {
	loaderKey := relEntityKey{Tenant: key.TenantID.String(), EntityID: key.EntityID.String()}

	var snaps []domainrelationship.Snapshot
	if r.inTx {
		fetched, err := r.batchByEntity(ctx, []relEntityKey{loaderKey})
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

	out := make([]*domainrelationship.Relationship, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, domainrelationship.Rehydrate(snap))
	}
	return out, nil
}

func (r *relationshipRepository) List(ctx context.Context, filter domainrelationship.Filter, page db.Page) ([]*domainrelationship.Relationship, int, error) {
	f := relListFilter{
		Tenant:          filter.TenantID.String(),
		IncludeArchived: filter.IncludeArchived,
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if !filter.DefinitionID.IsZero() {
		f.DefinitionID = filter.DefinitionID.String()
	}
	if !filter.ParentEntityID.IsZero() {
		f.ParentEntityID = filter.ParentEntityID.String()
	}
	if !filter.ChildEntityID.IsZero() {
		f.ChildEntityID = filter.ChildEntityID.String()
	}
	key := f.key()

	var result pagedResult[domainrelationship.Snapshot]
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

	out := make([]*domainrelationship.Relationship, 0, len(result.Items))
	for _, snap := range result.Items {
		out = append(out, domainrelationship.Rehydrate(snap))
	}
	return out, result.Total, nil
}

func (r *relationshipRepository) Save(ctx context.Context, rel *domainrelationship.Relationship) error {
	s := rel.Snapshot()
	var parentVersion, childVersion any
	if s.ParentVersion != nil {
		parentVersion = *s.ParentVersion
	}
	if s.ChildVersion != nil {
		childVersion = *s.ChildVersion
	}

	_, err := r.q.ExecContext(ctx, bind(
		`INSERT INTO flexitype_relationship
		   (id, tenant_id, relationship_definition_id, parent_entity_id, child_entity_id,
		    parent_type_version, child_type_version, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		   parent_type_version = EXCLUDED.parent_type_version,
		   child_type_version  = EXCLUDED.child_type_version,
		   updated_at          = EXCLUDED.updated_at,
		   archived_at         = EXCLUDED.archived_at`),
		s.ID.String(), s.TenantID.String(), s.DefinitionID.String(),
		s.ParentEntityID.String(), s.ChildEntityID.String(), parentVersion, childVersion,
		s.CreatedAt, s.UpdatedAt, nullableTime(s.ArchivedAt),
	)
	if err != nil {
		return fmt.Errorf("save relationship: %w", err)
	}
	return nil
}
