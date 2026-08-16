package memory

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/erasure"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// --- type definitions -----------------------------------------------------

type typeDefRepo struct {
	s *Store
	j *undoJournal
}

func (r *typeDefRepo) WithTx(tx db.Tx) domaintypedef.Repository {
	return &typeDefRepo{s: r.s, j: journalOf(tx)}
}

func (r *typeDefRepo) Get(_ context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	snap, ok := r.s.typeDefs[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, id.String())
	}
	return domaintypedef.Rehydrate(snap), nil
}

func (r *typeDefRepo) GetForUpdate(ctx context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	return r.Get(ctx, id)
}

func (r *typeDefRepo) GetByInternalName(_ context.Context, tenant valueobjects.TenantID, name string) (*domaintypedef.TypeDefinition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, snap := range r.s.typeDefs {
		if snap.TenantID == tenant && snap.InternalName == name && snap.ArchivedAt == nil {
			return domaintypedef.Rehydrate(snap), nil
		}
	}
	return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, name)
}

func (r *typeDefRepo) List(_ context.Context, filter domaintypedef.Filter, page db.Page) ([]*domaintypedef.TypeDefinition, int, error) {
	r.s.mu.RLock()
	var snaps []domaintypedef.Snapshot
	for _, snap := range r.s.typeDefs {
		if snap.TenantID != filter.TenantID {
			continue
		}
		if !filter.IncludeArchived && snap.ArchivedAt != nil {
			continue
		}
		if !filter.IncludeAttributeSets && snap.Kind == domaintypedef.KindRelationshipAttributes {
			continue
		}
		if !matchNames(filter.InternalNames, snap.InternalName) {
			continue
		}
		snaps = append(snaps, snap)
	}
	r.s.mu.RUnlock()

	sortByID(snaps, func(s domaintypedef.Snapshot) string { return s.ID.String() })
	pageItems, total, err := paginate(snaps, page, idKeyset, func(s domaintypedef.Snapshot) []string { return idKey(s.ID.String()) })
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domaintypedef.TypeDefinition, 0, len(pageItems))
	for _, snap := range pageItems {
		out = append(out, domaintypedef.Rehydrate(snap))
	}
	return out, total, nil
}

func (r *typeDefRepo) ListChildren(_ context.Context, parentID valueobjects.TypeDefinitionID) ([]*domaintypedef.TypeDefinition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []*domaintypedef.TypeDefinition
	for _, snap := range r.s.typeDefs {
		if snap.ExtendsID != nil && snap.ExtendsID.Equals(parentID) && snap.ArchivedAt == nil {
			out = append(out, domaintypedef.Rehydrate(snap))
		}
	}
	sortByID(out, func(t *domaintypedef.TypeDefinition) string { return t.InternalName() })
	return out, nil
}

// The uniqueness scan mirrors the unique index the Postgres schema declares.
// Without it the two backends disagreed about what is even representable: the
// interactor pre-checks and then saves, so two concurrent callers both cleared
// the check and both wrote, leaving two live rows sharing a natural key that
// Postgres refuses outright. Archived rows are skipped, because every one of
// those indexes is partial on archived_at IS NULL, and a row never collides
// with itself.
func (r *typeDefRepo) Save(_ context.Context, t *domaintypedef.TypeDefinition) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := t.Snapshot()
	if snap.ArchivedAt == nil {
		for _, other := range r.s.typeDefs {
			if other.ID.Equals(snap.ID) || other.ArchivedAt != nil {
				continue
			}
			if other.TenantID == snap.TenantID && other.InternalName == snap.InternalName {
				return domainerrors.NewConflict(
					"a type with this internal name already exists",
					"internal_name", snap.InternalName)
			}
		}
	}
	captureMap(r.j, collTypeDefs, r.s.typeDefs, snap.ID.String())
	r.s.typeDefs[snap.ID.String()] = snap
	r.s.bumpSchemaVersion(snap.TenantID.String()) // a type change reshapes the GraphQL schema
	return nil
}

// --- attribute definitions ------------------------------------------------

type attrRepo struct {
	s *Store
	j *undoJournal
}

func (r *attrRepo) WithTx(tx db.Tx) domainattribute.Repository {
	return &attrRepo{s: r.s, j: journalOf(tx)}
}

func (r *attrRepo) Get(_ context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	snap, ok := r.s.attrs[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domainattribute.AggregateType, id.String())
	}
	return domainattribute.Rehydrate(snap), nil
}

func (r *attrRepo) GetMany(ctx context.Context, ids []valueobjects.AttributeDefinitionID) ([]*domainattribute.Definition, error) {
	out := make([]*domainattribute.Definition, 0, len(ids))
	for _, id := range ids {
		a, err := r.Get(ctx, id)
		if domainerrors.IsNotFound(err) {
			continue // absent by contract, matching the Postgres repository
		}
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *attrRepo) GetForUpdate(ctx context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	return r.Get(ctx, id)
}

func (r *attrRepo) GetByInternalName(_ context.Context, typeDefID valueobjects.TypeDefinitionID, name string) (*domainattribute.Definition, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, snap := range r.s.attrs {
		if snap.TypeDefinitionID.Equals(typeDefID) && snap.InternalName == name && snap.ArchivedAt == nil {
			return domainattribute.Rehydrate(snap), nil
		}
	}
	return nil, domainerrors.NewNotFound(domainattribute.AggregateType, name)
}

func (r *attrRepo) ListByTypeDefinition(_ context.Context, typeDefID valueobjects.TypeDefinitionID, page db.Page) ([]*domainattribute.Definition, int, error) {
	r.s.mu.RLock()
	var snaps []domainattribute.Snapshot
	for _, snap := range r.s.attrs {
		if snap.TypeDefinitionID.Equals(typeDefID) && snap.ArchivedAt == nil {
			snaps = append(snaps, snap)
		}
	}
	r.s.mu.RUnlock()

	sortByID(snaps, func(s domainattribute.Snapshot) string { return s.ID.String() })
	pageItems, total, err := paginate(snaps, page, idKeyset, func(s domainattribute.Snapshot) []string { return idKey(s.ID.String()) })
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domainattribute.Definition, 0, len(pageItems))
	for _, snap := range pageItems {
		out = append(out, domainattribute.Rehydrate(snap))
	}
	return out, total, nil
}

func (r *attrRepo) List(_ context.Context, filter domainattribute.Filter, page db.Page) ([]*domainattribute.Definition, int, error) {
	r.s.mu.RLock()
	var snaps []domainattribute.Snapshot
	for _, snap := range r.s.attrs {
		if snap.TenantID != filter.TenantID {
			continue
		}
		if !filter.IncludeArchived && snap.ArchivedAt != nil {
			continue
		}
		if !filter.TypeDefinitionID.IsZero() && !snap.TypeDefinitionID.Equals(filter.TypeDefinitionID) {
			continue
		}
		if !matchNames(filter.InternalNames, snap.InternalName) {
			continue
		}
		if len(filter.DataTypes) > 0 {
			found := false
			for _, dt := range filter.DataTypes {
				if snap.DataType == dt {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if filter.UnitFamilyID != "" && snap.UnitFamilyID != filter.UnitFamilyID {
			continue
		}
		snaps = append(snaps, snap)
	}
	r.s.mu.RUnlock()

	sortByID(snaps, func(s domainattribute.Snapshot) string { return s.ID.String() })
	pageItems, total, err := paginate(snaps, page, idKeyset, func(s domainattribute.Snapshot) []string { return idKey(s.ID.String()) })
	if err != nil {
		return nil, 0, err
	}

	out := make([]*domainattribute.Definition, 0, len(pageItems))
	for _, snap := range pageItems {
		out = append(out, domainattribute.Rehydrate(snap))
	}
	return out, total, nil
}

func (r *attrRepo) Save(_ context.Context, a *domainattribute.Definition) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	snap := a.Snapshot()
	if snap.ArchivedAt == nil {
		for _, other := range r.s.attrs {
			if other.ID.Equals(snap.ID) || other.ArchivedAt != nil {
				continue
			}
			if other.TypeDefinitionID.Equals(snap.TypeDefinitionID) &&
				other.InternalName == snap.InternalName {
				return domainerrors.NewConflict(
					"an attribute with this internal name already exists on this type",
					"internal_name", snap.InternalName)
			}
		}
	}
	captureMap(r.j, collAttrs, r.s.attrs, snap.ID.String())
	r.s.attrs[snap.ID.String()] = snap
	r.s.bumpSchemaVersion(snap.TenantID.String()) // an attribute change adds/removes a schema field
	return nil
}

// --- activity log -----------------------------------------------------------

type activityLog struct{ s *Store }

func (l *activityLog) Write(_ context.Context, tx db.Tx, entries []activity.Entry) error {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	// The audit log is written from a pre-commit hook; journal its length so a
	// later pre-commit hook failing (which aborts the commit) also unwinds the
	// entries just appended.
	captureActivities(journalOf(tx), l.s)
	l.s.activities = append(l.s.activities, entries...)
	return nil
}

func (l *activityLog) List(_ context.Context, filter activity.Filter, page db.Page) ([]activity.Entry, int, error) {
	l.s.mu.RLock()
	var out []activity.Entry
	for _, e := range l.s.activities {
		if e.TenantID != filter.TenantID {
			continue
		}
		if filter.Entity != "" && e.Entity != filter.Entity {
			continue
		}
		if filter.EntityID != "" && e.EntityID != filter.EntityID {
			continue
		}
		if filter.Actor != "" && e.Actor != filter.Actor {
			continue
		}
		out = append(out, e)
	}
	l.s.mu.RUnlock()

	// Newest first (occurred-at then id, both descending), matching the SQL
	// implementation and the keyset cursor.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].OccurredAt.After(out[j].OccurredAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	pageItems, total, err := paginate(out, page, activityKeyset, entryKey)
	if err != nil {
		return nil, 0, err
	}
	return pageItems, total, nil
}

// activityEraser redacts an erased entity's value snapshots from the audit
// log, mirroring the Postgres eraser.
//
// Every write persisted the full value in an entry's before/after state, and
// the log deliberately survives an erasure so the erasure stays provable —
// which is exactly why the entries are redacted rather than deleted: the
// proof survives, the personal data does not.
type activityEraser struct{ s *Store }

// NewActivityEraser builds the in-memory audit-log residual eraser.
func (s *Store) NewActivityEraser() erasure.ResidualEraser { return &activityEraser{s: s} }

func (e *activityEraser) Name() string { return "activity log" }

// RedactEntity matches on the entity named INSIDE the state snapshot, not on
// the entry's EntityID: an entry for a value write keys on the value's own id,
// so the entity appears only in the recorded before/after JSON.
func (e *activityEraser) RedactEntity(_ context.Context, _ db.Tx, tenant valueobjects.TenantID, entityID string) (int, error) {
	return e.redact(func(entry activity.Entry) bool {
		return entry.TenantID == tenant && statesName(entry, entityID)
	})
}

// RedactTenant redacts every entry that names any entity. Entries that name
// none are schema history, which a tenant erasure keeps: it erases entity
// DATA, not definitions.
func (e *activityEraser) RedactTenant(_ context.Context, _ db.Tx, tenant valueobjects.TenantID) (int, error) {
	return e.redact(func(entry activity.Entry) bool {
		return entry.TenantID == tenant && statesName(entry, "")
	})
}

// statesName reports whether an entry's before/after snapshots name the given
// entity, or any entity when want is empty.
func statesName(entry activity.Entry, want string) bool {
	for _, raw := range []json.RawMessage{entry.Before, entry.After} {
		if len(raw) == 0 {
			continue
		}
		var probe struct {
			EntityID string `json:"entity_id"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil || probe.EntityID == "" {
			continue
		}
		if want == "" || probe.EntityID == want {
			return true
		}
	}
	return false
}

func (e *activityEraser) redact(match func(activity.Entry) bool) (int, error) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	marker := json.RawMessage(`{"` + erasure.RedactedMarker + `":true}`)
	n := 0
	for i := range e.s.activities {
		entry := e.s.activities[i]
		if !match(entry) || (entry.Before == nil && entry.After == nil) {
			continue
		}
		if entry.Before != nil {
			e.s.activities[i].Before = marker
		}
		if entry.After != nil {
			e.s.activities[i].After = marker
		}
		n++
	}
	return n, nil
}
