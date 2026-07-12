package memory

import (
	"context"
	"sort"

	"github.com/zkrebbekx/flexitype/application/activity"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// --- type definitions -----------------------------------------------------

type typeDefRepo struct{ s *Store }

func (r *typeDefRepo) WithTx(db.QueryExecer) domaintypedef.Repository { return r }

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
	pageItems, total := paginate(snaps, page, func(s domaintypedef.Snapshot) []string { return idKey(s.ID.String()) })

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

func (r *typeDefRepo) Save(_ context.Context, t *domaintypedef.TypeDefinition) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.typeDefs[t.ID().String()] = t.Snapshot()
	return nil
}

// --- attribute definitions ------------------------------------------------

type attrRepo struct{ s *Store }

func (r *attrRepo) WithTx(db.QueryExecer) domainattribute.Repository { return r }

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
	pageItems, total := paginate(snaps, page, func(s domainattribute.Snapshot) []string { return idKey(s.ID.String()) })

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
		snaps = append(snaps, snap)
	}
	r.s.mu.RUnlock()

	sortByID(snaps, func(s domainattribute.Snapshot) string { return s.ID.String() })
	pageItems, total := paginate(snaps, page, func(s domainattribute.Snapshot) []string { return idKey(s.ID.String()) })

	out := make([]*domainattribute.Definition, 0, len(pageItems))
	for _, snap := range pageItems {
		out = append(out, domainattribute.Rehydrate(snap))
	}
	return out, total, nil
}

func (r *attrRepo) Save(_ context.Context, a *domainattribute.Definition) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.attrs[a.ID().String()] = a.Snapshot()
	return nil
}

// --- activity log -----------------------------------------------------------

type activityLog struct{ s *Store }

func (l *activityLog) Write(_ context.Context, _ db.QueryExecer, entries []activity.Entry) error {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
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
	pageItems, total := paginate(out, page, entryKey, true, true)
	return pageItems, total, nil
}
