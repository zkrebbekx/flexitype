package memory

import (
	"context"
	"sort"

	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// --- attribute values -----------------------------------------------------

type valueRepo struct {
	s *Store
	j *undoJournal
}

func (r *valueRepo) WithTx(tx db.QueryExecer) domainvalue.Repository {
	return &valueRepo{s: r.s, j: journalOf(tx)}
}

func (r *valueRepo) Get(_ context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	snap, ok := r.s.values[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domainvalue.AggregateType, id.String())
	}
	return domainvalue.Rehydrate(snap), nil
}

func (r *valueRepo) GetForUpdate(ctx context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	return r.Get(ctx, id)
}

func (r *valueRepo) ListByEntity(_ context.Context, key domainvalue.EntityKey) ([]*domainvalue.AttributeValue, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []*domainvalue.AttributeValue
	for _, snap := range r.s.values {
		if snap.TenantID == key.TenantID && snap.TypeDefinitionID.Equals(key.TypeDefinitionID) &&
			snap.EntityID == key.EntityID && snap.ArchivedAt == nil {
			out = append(out, domainvalue.Rehydrate(snap))
		}
	}
	sortByID(out, func(v *domainvalue.AttributeValue) string { return v.ID().String() })
	return out, nil
}

func (r *valueRepo) ListByDefinition(_ context.Context, defID valueobjects.AttributeDefinitionID, page db.Page) ([]*domainvalue.AttributeValue, int, error) {
	r.s.mu.RLock()
	var out []*domainvalue.AttributeValue
	for _, snap := range r.s.values {
		if snap.AttributeDefinitionID.Equals(defID) && snap.ArchivedAt == nil {
			out = append(out, domainvalue.Rehydrate(snap))
		}
	}
	r.s.mu.RUnlock()
	sortByID(out, func(v *domainvalue.AttributeValue) string { return v.ID().String() })
	pageItems, total := paginate(out, page, func(v *domainvalue.AttributeValue) []string { return idKey(v.ID().String()) })
	return pageItems, total, nil
}

func (r *valueRepo) ListByEntities(_ context.Context, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) ([]*domainvalue.AttributeValue, error) {
	want := make(map[valueobjects.EntityID]bool, len(entityIDs))
	for _, id := range entityIDs {
		want[id] = true
	}
	r.s.mu.RLock()
	var out []*domainvalue.AttributeValue
	for _, snap := range r.s.values {
		if snap.TenantID == tenant && want[snap.EntityID] && snap.ArchivedAt == nil {
			out = append(out, domainvalue.Rehydrate(snap))
		}
	}
	r.s.mu.RUnlock()
	sortByID(out, func(v *domainvalue.AttributeValue) string { return v.ID().String() })
	return out, nil
}

func (r *valueRepo) FindByDefinitionAndEntity(_ context.Context, defID valueobjects.AttributeDefinitionID, entityID valueobjects.EntityID) ([]*domainvalue.AttributeValue, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []*domainvalue.AttributeValue
	for _, snap := range r.s.values {
		if snap.AttributeDefinitionID.Equals(defID) && snap.EntityID == entityID && snap.ArchivedAt == nil {
			out = append(out, domainvalue.Rehydrate(snap))
		}
	}
	sortByID(out, func(v *domainvalue.AttributeValue) string { return v.ID().String() })
	return out, nil
}

func (r *valueRepo) CountByDefinitionAndValue(_ context.Context, defID valueobjects.AttributeDefinitionID, scope valueobjects.Scope, v valueobjects.Value, excludeEntity valueobjects.EntityID) (int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	count := 0
	for _, snap := range r.s.values {
		if snap.AttributeDefinitionID.Equals(defID) && snap.EntityID != excludeEntity &&
			snap.Locale == scope.Locale && snap.Channel == scope.Channel &&
			snap.ArchivedAt == nil && snap.Value.Equal(v) {
			count++
		}
	}
	return count, nil
}

func (r *valueRepo) List(_ context.Context, filter domainvalue.Filter, page db.Page) ([]*domainvalue.AttributeValue, int, error) {
	r.s.mu.RLock()
	var out []*domainvalue.AttributeValue
	for _, snap := range r.s.values {
		if snap.TenantID != filter.TenantID {
			continue
		}
		if !filter.IncludeArchived && snap.ArchivedAt != nil {
			continue
		}
		if !filter.TypeDefinitionID.IsZero() && !snap.TypeDefinitionID.Equals(filter.TypeDefinitionID) {
			continue
		}
		if !filter.AttributeDefinitionID.IsZero() && !snap.AttributeDefinitionID.Equals(filter.AttributeDefinitionID) {
			continue
		}
		if !filter.EntityID.IsZero() && snap.EntityID != filter.EntityID {
			continue
		}
		out = append(out, domainvalue.Rehydrate(snap))
	}
	r.s.mu.RUnlock()
	sortByID(out, func(v *domainvalue.AttributeValue) string { return v.ID().String() })
	pageItems, total := paginate(out, page, func(v *domainvalue.AttributeValue) []string { return idKey(v.ID().String()) })
	return pageItems, total, nil
}

func (r *valueRepo) ListEntities(_ context.Context, tenant valueobjects.TenantID, typeDefIDs []valueobjects.TypeDefinitionID, page db.Page) ([]domainvalue.EntitySummary, int, error) {
	wanted := make(map[string]bool, len(typeDefIDs))
	for _, id := range typeDefIDs {
		wanted[id.String()] = true
	}

	r.s.mu.RLock()
	agg := map[string]*domainvalue.EntitySummary{}
	for _, snap := range r.s.values {
		if snap.TenantID != tenant || snap.ArchivedAt != nil || !wanted[snap.TypeDefinitionID.String()] {
			continue
		}
		key := snap.TypeDefinitionID.String() + "\x00" + snap.EntityID.String()
		e := agg[key]
		if e == nil {
			e = &domainvalue.EntitySummary{
				EntityID:         snap.EntityID,
				TypeDefinitionID: snap.TypeDefinitionID,
			}
			agg[key] = e
		}
		e.ValueCount++
		if snap.UpdatedAt.After(e.LastUpdatedAt) {
			e.LastUpdatedAt = snap.UpdatedAt
		}
	}
	r.s.mu.RUnlock()

	out := make([]domainvalue.EntitySummary, 0, len(agg))
	for _, e := range agg {
		out = append(out, *e)
	}
	// Most recently changed first, matching the SQL implementation.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastUpdatedAt.Equal(out[j].LastUpdatedAt) {
			return out[i].LastUpdatedAt.After(out[j].LastUpdatedAt)
		}
		return out[i].EntityID.String() < out[j].EntityID.String()
	})
	pageItems, total := paginate(out, page, func(e domainvalue.EntitySummary) []string { return entityKey(e.LastUpdatedAt, e.EntityID.String()) }, true, false)
	return pageItems, total, nil
}

func (r *valueRepo) Save(_ context.Context, av *domainvalue.AttributeValue) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	id := av.ID().String()
	captureMap(r.j, collValues, r.s.values, id)
	r.s.values[id] = av.Snapshot()
	return nil
}

func (r *valueRepo) MediaKeyBelongsToTenant(_ context.Context, tenant valueobjects.TenantID, objectKey string) (bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, snap := range r.s.values {
		if snap.TenantID == tenant &&
			snap.Value.DataType() == valueobjects.DataTypeMedia &&
			snap.Value.Media().ObjectKey == objectKey {
			return true, nil
		}
	}
	return false, nil
}

func (r *valueRepo) PurgeEntity(_ context.Context, key domainvalue.EntityKey) ([]string, int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var mediaKeys []string
	count := 0
	// Erase every row of the entity, archived rows included (no archived_at
	// guard), collecting media object keys so the interactor can GC the blobs.
	for id, snap := range r.s.values {
		if snap.TenantID != key.TenantID || !snap.TypeDefinitionID.Equals(key.TypeDefinitionID) || snap.EntityID != key.EntityID {
			continue
		}
		if snap.Value.DataType() == valueobjects.DataTypeMedia {
			if k := snap.Value.Media().ObjectKey; k != "" {
				mediaKeys = append(mediaKeys, k)
			}
		}
		captureMap(r.j, collValues, r.s.values, id)
		delete(r.s.values, id)
		count++
	}
	return mediaKeys, count, nil
}

func (r *valueRepo) PurgeTenant(_ context.Context, tenant valueobjects.TenantID) ([]string, int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var mediaKeys []string
	count := 0
	for id, snap := range r.s.values {
		if snap.TenantID != tenant {
			continue
		}
		if snap.Value.DataType() == valueobjects.DataTypeMedia {
			if k := snap.Value.Media().ObjectKey; k != "" {
				mediaKeys = append(mediaKeys, k)
			}
		}
		captureMap(r.j, collValues, r.s.values, id)
		delete(r.s.values, id)
		count++
	}
	return mediaKeys, count, nil
}

// --- dependencies -----------------------------------------------------------

type depRepo struct {
	s *Store
	j *undoJournal
}

func (r *depRepo) WithTx(tx db.QueryExecer) domaindependency.Repository {
	return &depRepo{s: r.s, j: journalOf(tx)}
}

func (r *depRepo) Get(_ context.Context, id valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	snap, ok := r.s.deps[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domaindependency.AggregateType, id.String())
	}
	return domaindependency.Rehydrate(snap), nil
}

func (r *depRepo) GetForUpdate(ctx context.Context, id valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	return r.Get(ctx, id)
}

func (r *depRepo) ListByTarget(_ context.Context, targetID valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	return r.listBy(func(s domaindependency.Snapshot) bool { return s.TargetAttributeID.Equals(targetID) })
}

func (r *depRepo) ListBySource(_ context.Context, sourceID valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	return r.listBy(func(s domaindependency.Snapshot) bool { return s.SourceAttributeID.Equals(sourceID) })
}

func (r *depRepo) listBy(match func(domaindependency.Snapshot) bool) ([]*domaindependency.Dependency, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []*domaindependency.Dependency
	for _, snap := range r.s.deps {
		if snap.ArchivedAt == nil && match(snap) {
			out = append(out, domaindependency.Rehydrate(snap))
		}
	}
	sortByID(out, func(d *domaindependency.Dependency) string { return d.ID().String() })
	return out, nil
}

func (r *depRepo) List(_ context.Context, filter domaindependency.Filter, page db.Page) ([]*domaindependency.Dependency, int, error) {
	r.s.mu.RLock()
	var out []*domaindependency.Dependency
	for _, snap := range r.s.deps {
		if snap.TenantID != filter.TenantID {
			continue
		}
		if !filter.IncludeArchived && snap.ArchivedAt != nil {
			continue
		}
		if !filter.SourceAttributeID.IsZero() && !snap.SourceAttributeID.Equals(filter.SourceAttributeID) {
			continue
		}
		if !filter.TargetAttributeID.IsZero() && !snap.TargetAttributeID.Equals(filter.TargetAttributeID) {
			continue
		}
		out = append(out, domaindependency.Rehydrate(snap))
	}
	r.s.mu.RUnlock()
	sortByID(out, func(d *domaindependency.Dependency) string { return d.ID().String() })
	pageItems, total := paginate(out, page, func(d *domaindependency.Dependency) []string { return idKey(d.ID().String()) })
	return pageItems, total, nil
}

func (r *depRepo) Save(_ context.Context, d *domaindependency.Dependency) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	id := d.ID().String()
	captureMap(r.j, collDeps, r.s.deps, id)
	r.s.deps[id] = d.Snapshot()
	return nil
}

// --- relationship definitions -------------------------------------------------

type relDefRepo struct {
	s *Store
	j *undoJournal
}

func (r *relDefRepo) WithTx(tx db.QueryExecer) domainrelationship.DefinitionRepository {
	return &relDefRepo{s: r.s, j: journalOf(tx)}
}

func (r *relDefRepo) Get(_ context.Context, id valueobjects.RelationshipDefinitionID) (*domainrelationship.Definition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	snap, ok := r.s.relDefs[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domainrelationship.DefinitionAggregateType, id.String())
	}
	return domainrelationship.RehydrateDefinition(snap), nil
}

func (r *relDefRepo) GetForUpdate(ctx context.Context, id valueobjects.RelationshipDefinitionID) (*domainrelationship.Definition, error) {
	return r.Get(ctx, id)
}

func (r *relDefRepo) GetByInternalName(_ context.Context, tenant valueobjects.TenantID, name string) (*domainrelationship.Definition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, snap := range r.s.relDefs {
		if snap.TenantID == tenant && snap.InternalName == name && snap.ArchivedAt == nil {
			return domainrelationship.RehydrateDefinition(snap), nil
		}
	}
	return nil, domainerrors.NewNotFound(domainrelationship.DefinitionAggregateType, name)
}

func (r *relDefRepo) List(_ context.Context, filter domainrelationship.DefinitionFilter, page db.Page) ([]*domainrelationship.Definition, int, error) {
	typeWanted := make(map[string]bool, len(filter.TypeDefinitionIDs))
	for _, id := range filter.TypeDefinitionIDs {
		typeWanted[id.String()] = true
	}

	r.s.mu.RLock()
	var out []*domainrelationship.Definition
	for _, snap := range r.s.relDefs {
		if snap.TenantID != filter.TenantID {
			continue
		}
		if !filter.IncludeArchived && snap.ArchivedAt != nil {
			continue
		}
		if len(typeWanted) > 0 && !typeWanted[snap.ParentTypeID.String()] && !typeWanted[snap.ChildTypeID.String()] {
			continue
		}
		out = append(out, domainrelationship.RehydrateDefinition(snap))
	}
	r.s.mu.RUnlock()
	sortByID(out, func(d *domainrelationship.Definition) string { return d.ID().String() })
	pageItems, total := paginate(out, page, func(d *domainrelationship.Definition) []string { return idKey(d.ID().String()) })
	return pageItems, total, nil
}

func (r *relDefRepo) Save(_ context.Context, d *domainrelationship.Definition) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := d.Snapshot()
	captureMap(r.j, collRelDefs, r.s.relDefs, snap.ID.String())
	r.s.relDefs[snap.ID.String()] = snap
	r.s.bumpSchemaVersion(snap.TenantID.String()) // a relationship change adds/removes a connection field
	return nil
}

// --- relationships ----------------------------------------------------------

type relRepo struct {
	s *Store
	j *undoJournal
}

func (r *relRepo) WithTx(tx db.QueryExecer) domainrelationship.Repository {
	return &relRepo{s: r.s, j: journalOf(tx)}
}

func (r *relRepo) Get(_ context.Context, id valueobjects.RelationshipID) (*domainrelationship.Relationship, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	snap, ok := r.s.rels[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domainrelationship.AggregateType, id.String())
	}
	return domainrelationship.Rehydrate(snap), nil
}

func (r *relRepo) GetForUpdate(ctx context.Context, id valueobjects.RelationshipID) (*domainrelationship.Relationship, error) {
	return r.Get(ctx, id)
}

func (r *relRepo) FindLive(_ context.Context, defID valueobjects.RelationshipDefinitionID, parent, child valueobjects.EntityID) (*domainrelationship.Relationship, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, snap := range r.s.rels {
		if snap.DefinitionID.Equals(defID) && snap.ParentEntityID == parent && snap.ChildEntityID == child && snap.ArchivedAt == nil {
			return domainrelationship.Rehydrate(snap), nil
		}
	}
	return nil, nil
}

func (r *relRepo) CountLiveLinks(_ context.Context, defID valueobjects.RelationshipDefinitionID, entity valueobjects.EntityID) (int, int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var asParent, asChild int
	for _, snap := range r.s.rels {
		if !snap.DefinitionID.Equals(defID) || snap.ArchivedAt != nil {
			continue
		}
		if snap.ParentEntityID == entity {
			asParent++
		}
		if snap.ChildEntityID == entity {
			asChild++
		}
	}
	return asParent, asChild, nil
}

func (r *relRepo) ListByEntity(_ context.Context, key domainrelationship.EntityLinksKey) ([]*domainrelationship.Relationship, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []*domainrelationship.Relationship
	for _, snap := range r.s.rels {
		if snap.TenantID != key.TenantID || snap.ArchivedAt != nil {
			continue
		}
		if snap.ParentEntityID == key.EntityID || snap.ChildEntityID == key.EntityID {
			out = append(out, domainrelationship.Rehydrate(snap))
		}
	}
	sortByID(out, func(rel *domainrelationship.Relationship) string { return rel.ID().String() })
	return out, nil
}

func (r *relRepo) ListByEntities(_ context.Context, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) ([]*domainrelationship.Relationship, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	want := make(map[valueobjects.EntityID]bool, len(entityIDs))
	for _, id := range entityIDs {
		want[id] = true
	}
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []*domainrelationship.Relationship
	for _, snap := range r.s.rels {
		if snap.TenantID != tenant || snap.ArchivedAt != nil {
			continue
		}
		if want[snap.ParentEntityID] || want[snap.ChildEntityID] {
			out = append(out, domainrelationship.Rehydrate(snap))
		}
	}
	sortByID(out, func(rel *domainrelationship.Relationship) string { return rel.ID().String() })
	return out, nil
}

// WindowedLinks mirrors the Postgres row-number window in memory: for each
// self entity it gathers the opposite endpoints of one relationship definition
// in one direction, orders them by opposite id ascending, applies the keyset
// cursor, and keeps at most Page.Limit+1 (the sentinel drives hasNextPage). It
// never materializes a self's whole fan-out into the result — the parity twin
// of the no-N+1 GraphQL path.
func (r *relRepo) WindowedLinks(_ context.Context, w domainrelationship.LinkWindow, selves []valueobjects.EntityID) (map[valueobjects.EntityID]domainrelationship.LinkPage, error) {
	out := make(map[valueobjects.EntityID]domainrelationship.LinkPage, len(selves))
	if len(selves) == 0 {
		return out, nil
	}
	want := make(map[valueobjects.EntityID]bool, len(selves))
	for _, s := range selves {
		want[s] = true
	}

	// The nested-connection cursor is a single-value keyset of the opposite
	// entity id; a malformed cursor pages from the start (matching Postgres,
	// whose keyset predicate treats a bad cursor as absent).
	afterID := ""
	if w.Page.Cursor != "" {
		if vals, err := db.DecodeKeyset(w.Page.Cursor); err == nil && len(vals) == 1 {
			afterID = vals[0]
		}
	}

	r.s.mu.RLock()
	others := make(map[valueobjects.EntityID][]valueobjects.EntityID, len(selves))
	add := func(self, other valueobjects.EntityID) {
		if want[self] {
			others[self] = append(others[self], other)
		}
	}
	for _, snap := range r.s.rels {
		if snap.TenantID != w.TenantID || snap.ArchivedAt != nil || !snap.DefinitionID.Equals(w.DefinitionID) {
			continue
		}
		p, c := snap.ParentEntityID, snap.ChildEntityID
		switch w.Side {
		case domainrelationship.ChildSide:
			add(c, p)
		case domainrelationship.EitherSide:
			add(p, c)
			if p != c { // a self-loop is emitted once (by the parent arm above)
				add(c, p)
			}
		default: // ParentSide
			add(p, c)
		}
	}
	r.s.mu.RUnlock()

	for self, os := range others {
		sort.Slice(os, func(i, j int) bool { return os[i] < os[j] })
		var total *int
		if w.Page.WantTotal { // the full fan-out, independent of the cursor
			t := len(os)
			total = &t
		}
		start := 0
		if afterID != "" {
			for start < len(os) && string(os[start]) <= afterID {
				start++
			}
		}
		window := os[start:]
		hasMore := len(window) > w.Page.Limit
		if hasMore {
			window = window[:w.Page.Limit]
		}
		out[self] = domainrelationship.LinkPage{
			Others:  append([]valueobjects.EntityID(nil), window...),
			HasMore: hasMore,
			Total:   total,
		}
	}
	// Selves with no matching links still answer a totalCount selection.
	for _, self := range selves {
		if _, ok := out[self]; !ok {
			var total *int
			if w.Page.WantTotal {
				z := 0
				total = &z
			}
			out[self] = domainrelationship.LinkPage{Total: total}
		}
	}
	return out, nil
}

func (r *relRepo) List(_ context.Context, filter domainrelationship.Filter, page db.Page) ([]*domainrelationship.Relationship, int, error) {
	r.s.mu.RLock()
	var out []*domainrelationship.Relationship
	for _, snap := range r.s.rels {
		if snap.TenantID != filter.TenantID {
			continue
		}
		if !filter.IncludeArchived && snap.ArchivedAt != nil {
			continue
		}
		if !filter.DefinitionID.IsZero() && !snap.DefinitionID.Equals(filter.DefinitionID) {
			continue
		}
		if !filter.ParentEntityID.IsZero() && snap.ParentEntityID != filter.ParentEntityID {
			continue
		}
		if !filter.ChildEntityID.IsZero() && snap.ChildEntityID != filter.ChildEntityID {
			continue
		}
		out = append(out, domainrelationship.Rehydrate(snap))
	}
	r.s.mu.RUnlock()
	sortByID(out, func(rel *domainrelationship.Relationship) string { return rel.ID().String() })
	pageItems, total := paginate(out, page, func(rel *domainrelationship.Relationship) []string { return idKey(rel.ID().String()) })
	return pageItems, total, nil
}

func (r *relRepo) Save(_ context.Context, rel *domainrelationship.Relationship) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	id := rel.ID().String()
	captureMap(r.j, collRels, r.s.rels, id)
	r.s.rels[id] = rel.Snapshot()
	return nil
}

func (r *relRepo) PurgeEntity(_ context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	count := 0
	// Erase every link touching the entity on either side, archived included.
	for id, snap := range r.s.rels {
		if snap.TenantID == tenant && (snap.ParentEntityID == entityID || snap.ChildEntityID == entityID) {
			captureMap(r.j, collRels, r.s.rels, id)
			delete(r.s.rels, id)
			count++
		}
	}
	return count, nil
}

func (r *relRepo) PurgeTenant(_ context.Context, tenant valueobjects.TenantID) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	count := 0
	for id, snap := range r.s.rels {
		if snap.TenantID == tenant {
			captureMap(r.j, collRels, r.s.rels, id)
			delete(r.s.rels, id)
			count++
		}
	}
	return count, nil
}
