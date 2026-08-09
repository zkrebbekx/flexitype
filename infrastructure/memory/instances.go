package memory

import (
	"context"
	"fmt"
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

func (r *valueRepo) WithTx(tx db.Tx) domainvalue.Repository {
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
	pageItems, total, err := paginate(out, page, idKeyset, func(v *domainvalue.AttributeValue) []string { return idKey(v.ID().String()) })
	if err != nil {
		return nil, 0, err
	}
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
	pageItems, total, err := paginate(out, page, idKeyset, func(v *domainvalue.AttributeValue) []string { return idKey(v.ID().String()) })
	if err != nil {
		return nil, 0, err
	}
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
	// A full sweep orders on the IMMUTABLE key, matching the SQL
	// implementation: last_updated_at changes on every write, so an entity
	// written mid-sweep jumps ahead of a newest-first cursor and is skipped.
	if page.Stable {
		sort.Slice(out, func(i, j int) bool {
			return out[i].EntityID.String() < out[j].EntityID.String()
		})
		pageItems, total, err := paginate(out, page, entityIDKeyset,
			func(e domainvalue.EntitySummary) []string { return []string{e.EntityID.String()} })
		if err != nil {
			return nil, 0, err
		}
		return pageItems, total, nil
	}

	// Most recently changed first, matching the SQL implementation.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastUpdatedAt.Equal(out[j].LastUpdatedAt) {
			return out[i].LastUpdatedAt.After(out[j].LastUpdatedAt)
		}
		return out[i].EntityID.String() < out[j].EntityID.String()
	})
	pageItems, total, err := paginate(out, page, entityKeyset, func(e domainvalue.EntitySummary) []string { return entityKey(e.LastUpdatedAt, e.EntityID.String()) })
	if err != nil {
		return nil, 0, err
	}
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

func (r *valueRepo) EntityAnchor(_ context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) (valueobjects.TypeDefinitionID, bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var best *domainvalue.Snapshot
	for _, snap := range r.s.values {
		if snap.TenantID != tenant || snap.EntityID != entityID {
			continue
		}
		// Ties broken by id, matching the SQL ordering: rows written in one
		// batch share a timestamp, and an undefined anchor would flap
		// between reads.
		if best == nil || snap.CreatedAt.Before(best.CreatedAt) ||
			(snap.CreatedAt.Equal(best.CreatedAt) && snap.ID.String() < best.ID.String()) {
			s := snap
			best = &s
		}
	}
	if best == nil {
		return valueobjects.TypeDefinitionID{}, false, nil
	}
	return best.TypeDefinitionID, true, nil
}

func (r *valueRepo) ReanchorEntity(_ context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID, to valueobjects.TypeDefinitionID) (int, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	moved := 0
	for id, snap := range r.s.values {
		if snap.TenantID != tenant || snap.EntityID != entityID || snap.TypeDefinitionID.Equals(to) {
			continue
		}
		captureMap(r.j, collValues, r.s.values, id)
		snap.TypeDefinitionID = to
		r.s.values[id] = snap
		moved++
	}
	return moved, nil
}

func (r *valueRepo) MediaValueForKey(_ context.Context, tenant valueobjects.TenantID, objectKey string) (domainvalue.Snapshot, bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var best *domainvalue.Snapshot
	for _, snap := range r.s.values {
		if snap.TenantID != tenant || snap.Value.DataType() != valueobjects.DataTypeMedia ||
			snap.Value.Media().ObjectKey != objectKey {
			continue
		}
		// Ties broken by id, matching the SQL ordering. This row decides
		// which attribute OWNS the object key, and ownership governs both
		// adoption and download authorization — so an undefined winner would
		// make the field ACL that applies depend on iteration order.
		if best == nil || snap.CreatedAt.Before(best.CreatedAt) ||
			(snap.CreatedAt.Equal(best.CreatedAt) && snap.ID.String() < best.ID.String()) {
			s := snap
			best = &s
		}
	}
	if best == nil {
		return domainvalue.Snapshot{}, false, nil
	}
	return *best, true, nil
}

func (r *valueRepo) MediaKeyRefCount(_ context.Context, objectKey string, exclude valueobjects.AttributeValueID) (int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	n := 0
	for _, snap := range r.s.values {
		if snap.ArchivedAt != nil {
			continue
		}
		if snap.Value.DataType() == valueobjects.DataTypeMedia &&
			snap.Value.Media().ObjectKey == objectKey && !snap.ID.Equals(exclude) {
			n++
		}
	}
	return n, nil
}

// MediaKeyRefCounts is the batched MediaKeyRefCount: live rows per key, any
// tenant, no exclusion. Keys with no live rows are absent from the result.
func (r *valueRepo) MediaKeyRefCounts(_ context.Context, objectKeys []string) (map[string]int, error) {
	wanted := make(map[string]struct{}, len(objectKeys))
	for _, k := range objectKeys {
		wanted[k] = struct{}{}
	}
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make(map[string]int, len(objectKeys))
	for _, snap := range r.s.values {
		if snap.ArchivedAt != nil || snap.Value.DataType() != valueobjects.DataTypeMedia {
			continue
		}
		if _, ok := wanted[snap.Value.Media().ObjectKey]; ok {
			out[snap.Value.Media().ObjectKey]++
		}
	}
	return out, nil
}

// LockMediaKey serializes adoption and blob GC of one object key, mirroring
// the PostgreSQL advisory lock: it blocks until the lock is free (bounded by
// ctx), is re-entrant inside the transaction, and the transactor releases it
// at commit or rollback.
func (r *valueRepo) LockMediaKey(ctx context.Context, objectKey string) error {
	if r.j == nil {
		return fmt.Errorf("memory: LockMediaKey requires a transaction")
	}
	if r.j.holdsMediaKey(objectKey) {
		return nil
	}
	if err := r.s.lockMediaKey(ctx, objectKey); err != nil {
		return err
	}
	r.j.heldMediaKeys = append(r.j.heldMediaKeys, objectKey)
	return nil
}

func (r *valueRepo) MediaKeyAttributes(_ context.Context, tenant valueobjects.TenantID, objectKey string) ([]valueobjects.AttributeDefinitionID, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	seen := map[string]bool{}
	out := []valueobjects.AttributeDefinitionID{}
	for _, snap := range r.s.values {
		if snap.TenantID != tenant ||
			snap.Value.DataType() != valueobjects.DataTypeMedia ||
			snap.Value.Media().ObjectKey != objectKey {
			continue
		}
		if id := snap.AttributeDefinitionID; !seen[id.String()] {
			seen[id.String()] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].String() < out[b].String() })
	return out, nil
}

// AttributeDataShape scans the tenant's live rows for the one attribute.
func (r *valueRepo) AttributeDataShape(_ context.Context, tenant valueobjects.TenantID, attrID valueobjects.AttributeDefinitionID) (domainvalue.DataShape, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	var out domainvalue.DataShape
	// Keyed by (entity, locale, channel), not by entity alone: a localizable
	// attribute holding one value per locale is not "more than one value" for
	// the purposes of making it single-valued, and counting it as such
	// refused the change for data the new schema expresses perfectly.
	perScope := map[string]int{}
	// Duplicates are counted per DISTINCT ENTITY, as Postgres does.
	perValue := map[string]map[string]bool{}
	manyEntities := map[string]bool{}
	for _, snap := range r.s.values {
		if snap.TenantID != tenant || !snap.AttributeDefinitionID.Equals(attrID) || snap.ArchivedAt != nil {
			continue
		}
		out.LiveValues++
		entity := snap.EntityID.String()
		key := entity + "\x1f" + snap.Locale + "\x1f" + snap.Channel
		perScope[key]++
		if perScope[key] > 1 {
			manyEntities[entity] = true
		}
		if snap.Locale != "" || snap.Channel != "" {
			out.ScopedValues++
		}
		// Key duplicates on the value's EQUALITY key, and per scope.
		//
		// The rendering was the key before, but a rendering is not an
		// identity: it keeps a decimal's trailing zeros and a quantity's
		// authored unit, so "1.5" against "1.50" and "5 kg" against
		// "5000 g" counted as distinct — while the write path compares them
		// with Value.Equal and calls them duplicates. Uniqueness is also
		// per (locale, channel), so one value held in two locales is not a
		// duplicate of itself.
		vkey := snap.Value.EqualityKey() + "\x1f" + snap.Locale + "\x1f" + snap.Channel
		if perValue[vkey] == nil {
			perValue[vkey] = map[string]bool{}
		}
		perValue[vkey][entity] = true
	}
	out.EntitiesWithMany = len(manyEntities)
	for _, entities := range perValue {
		if n := len(entities); n > 1 {
			out.DuplicateValues += n
		}
	}
	return out, nil
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

func (r *depRepo) WithTx(tx db.Tx) domaindependency.Repository {
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

func (r *depRepo) ListEnforcedOnWrite(_ context.Context, tenant valueobjects.TenantID) ([]*domaindependency.Dependency, error) {
	return r.listBy(func(s domaindependency.Snapshot) bool {
		return s.TenantID == tenant && s.Effect.DemandsValue() &&
			s.Effect.Enforcement() == domaindependency.EnforceOnWrite
	})
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
	pageItems, total, err := paginate(out, page, idKeyset, func(d *domaindependency.Dependency) []string { return idKey(d.ID().String()) })
	if err != nil {
		return nil, 0, err
	}
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

func (r *relDefRepo) WithTx(tx db.Tx) domainrelationship.DefinitionRepository {
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
	pageItems, total, err := paginate(out, page, idKeyset, func(d *domainrelationship.Definition) []string { return idKey(d.ID().String()) })
	if err != nil {
		return nil, 0, err
	}
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

func (r *relRepo) WithTx(tx db.Tx) domainrelationship.Repository {
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
	// entity id. A cursor that carries any other number of values cannot
	// address a link, so this rejects it as a validation error — the same
	// answer the Postgres window gives, which builds its arm predicate with
	// db.KeysetPredicate.
	afterID := ""
	if w.Page.Cursor != "" {
		vals, err := db.ValidateKeyset(idKeyset, w.Page.Cursor)
		if err != nil {
			return nil, err
		}
		afterID = vals[0]
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
		// One row per COUNTERPART, matching the SQL window's DISTINCT. A
		// symmetric relationship holding both A->B and B->A adds B twice, and
		// so do two links between one pair on any side. The cursor is the
		// opposite id alone, so a repeat both duplicated the counterpart
		// inside a page and, across a page boundary, let the `> cursor`
		// predicate skip it entirely.
		deduped := os[:0]
		for i, other := range os {
			if i == 0 || other != os[i-1] {
				deduped = append(deduped, other)
			}
		}
		os = deduped
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
	pageItems, total, err := paginate(out, page, idKeyset, func(rel *domainrelationship.Relationship) []string { return idKey(rel.ID().String()) })
	if err != nil {
		return nil, 0, err
	}
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
