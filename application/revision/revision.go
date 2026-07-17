// Package revision versions the ENTITY, not just the schema: a revision is
// an immutable snapshot of all of an entity's live attribute values at a
// point in time. Revisions support point-in-time reads, diffing and
// restore (which creates new forward state and never mutates history) — the
// object-versioning model PLM-class systems are built around.
package revision

import (
	"context"
	"sort"
	"time"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Value is one attribute's value captured in a revision. DataType is
// retained so a restore can reconstruct the typed value from its string form.
// Locale and Channel preserve the value's scope so scoped (localized /
// per-channel) values restore, diff and read back at the exact scope they
// were captured in rather than collapsing onto the base value.
type Value struct {
	AttributeDefinitionID string `json:"attribute_definition_id"`
	InternalName          string `json:"internal_name"`
	DisplayName           string `json:"display_name"`
	DataType              string `json:"data_type"`
	Locale                string `json:"locale,omitempty"`
	Channel               string `json:"channel,omitempty"`
	Value                 string `json:"value"`
}

// Revision is an immutable snapshot of an entity's live values.
type Revision struct {
	ID               ulid.ID               `json:"id"`
	TenantID         valueobjects.TenantID `json:"tenant_id"`
	TypeDefinitionID string                `json:"type_definition_id"`
	EntityID         string                `json:"entity_id"`
	Seq              int                   `json:"seq"`
	Label            string                `json:"label,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	Values           []Value               `json:"values"`
}

// Store persists entity revisions, scoped by tenant.
type Store interface {
	// WithTx binds the store to a transaction so an erasure's revision purge
	// joins the value write's atomic unit of work — a rollback then also
	// un-does the revision delete, and the audit trail is never orphaned by a
	// value-tx abort. It mirrors the value repository's WithTx.
	WithTx(tx db.QueryExecer) Store

	Create(ctx context.Context, r Revision) error
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (Revision, error)
	List(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) ([]Revision, error)
	// AsOf returns the latest revision at or before the instant, or NotFound.
	AsOf(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string, at time.Time) (Revision, error)
	// LastSeq returns the highest revision sequence for an entity, or 0.
	LastSeq(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error)

	// PurgeEntity HARD-deletes every revision of one entity — the
	// right-to-erasure primitive — and returns the number of rows removed.
	PurgeEntity(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error)

	// PurgeTenant HARD-deletes every revision of a tenant, returning the row
	// count.
	PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error)
}

// SnapshotApplier applies a target value set to an entity in one unit of
// work — the write half of a restore, kept in the value interactor so it
// reuses the full validation path. Each SnapshotCell is a raw string value
// plus the data type to interpret it.
type SnapshotApplier interface {
	ApplySnapshot(ctx context.Context, typeDefID, entityID string, cells []SnapshotCell) error
}

// SnapshotCell is one attribute value to restore, at its captured scope.
type SnapshotCell struct {
	AttributeDefinitionID string
	DataType              string
	Locale                string
	Channel               string
	Value                 string
}

// Interactor implements the entity-revision usecases.
type Interactor struct {
	store    Store
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	values   domainvalue.Repository
	applier  SnapshotApplier
	now      func() time.Time
}

// NewInteractor wires the entity-revision usecases.
func NewInteractor(
	store Store,
	typeDefs domaintypedef.Repository,
	attrs domainattribute.Repository,
	values domainvalue.Repository,
	applier SnapshotApplier,
	now func() time.Time,
) *Interactor {
	if now == nil {
		now = uow.UTCNow
	}
	return &Interactor{store: store, typeDefs: typeDefs, attrs: attrs, values: values, applier: applier, now: now}
}

// Create captures the entity's current live values as a new revision.
func (i *Interactor) Create(ctx context.Context, rawTypeID, entityID, label string) (*Revision, error) {
	typeID, tenant, err := i.resolveEntity(ctx, rawTypeID, entityID)
	if err != nil {
		return nil, err
	}
	values, err := i.snapshotValues(ctx, tenant, typeID, entityID)
	if err != nil {
		return nil, err
	}
	last, err := i.store.LastSeq(ctx, tenant, rawTypeID, entityID)
	if err != nil {
		return nil, err
	}
	rev := Revision{
		ID:               ulid.New(),
		TenantID:         tenant,
		TypeDefinitionID: rawTypeID,
		EntityID:         entityID,
		Seq:              last + 1,
		Label:            label,
		CreatedAt:        i.now(),
		Values:           values,
	}
	if err := i.store.Create(ctx, rev); err != nil {
		return nil, err
	}
	return &rev, nil
}

// List returns an entity's revisions, newest first, without value payloads.
func (i *Interactor) List(ctx context.Context, rawTypeID, entityID string) ([]Revision, error) {
	tenant := uow.TenantFromContext(ctx)
	revs, err := i.store.List(ctx, tenant, rawTypeID, entityID)
	if err != nil {
		return nil, err
	}
	for idx := range revs {
		revs[idx].Values = nil // list is metadata only
	}
	return revs, nil
}

// Get returns one revision with its full value snapshot.
func (i *Interactor) Get(ctx context.Context, rawRevisionID string) (*Revision, error) {
	id, err := ulid.Parse(rawRevisionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	rev, err := i.store.Get(ctx, uow.TenantFromContext(ctx), id)
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// AsOf returns the entity's state at an instant: the latest revision at or
// before the timestamp.
func (i *Interactor) AsOf(ctx context.Context, rawTypeID, entityID string, at time.Time) (*Revision, error) {
	rev, err := i.store.AsOf(ctx, uow.TenantFromContext(ctx), rawTypeID, entityID, at)
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// Change is one attribute's difference between two revisions, at one scope.
type Change struct {
	AttributeDefinitionID string `json:"attribute_definition_id"`
	InternalName          string `json:"internal_name"`
	Locale                string `json:"locale,omitempty"`
	Channel               string `json:"channel,omitempty"`
	Kind                  string `json:"kind"` // added | changed | removed
	Before                string `json:"before,omitempty"`
	After                 string `json:"after,omitempty"`
}

// DiffOutput lists the changes from one revision to another.
type DiffOutput struct {
	FromSeq int      `json:"from_seq"`
	ToSeq   int      `json:"to_seq"`
	Changes []Change `json:"changes"`
}

// Diff compares two revisions and lists added, changed and removed values.
func (i *Interactor) Diff(ctx context.Context, rawFrom, rawTo string) (*DiffOutput, error) {
	from, err := i.Get(ctx, rawFrom)
	if err != nil {
		return nil, err
	}
	to, err := i.Get(ctx, rawTo)
	if err != nil {
		return nil, err
	}

	// Key by (attribute, locale, channel) so each scope of a scoped value is
	// diffed independently rather than collapsing onto one entry per attribute.
	fromByScope := map[string]Value{}
	for _, v := range from.Values {
		fromByScope[scopeKey(v)] = v
	}
	toByScope := map[string]Value{}
	for _, v := range to.Values {
		toByScope[scopeKey(v)] = v
	}

	out := &DiffOutput{FromSeq: from.Seq, ToSeq: to.Seq, Changes: []Change{}}
	for k, tv := range toByScope {
		if fv, ok := fromByScope[k]; !ok {
			out.Changes = append(out.Changes, change(tv, "added", "", tv.Value))
		} else if fv.Value != tv.Value {
			out.Changes = append(out.Changes, change(tv, "changed", fv.Value, tv.Value))
		}
	}
	for k, fv := range fromByScope {
		if _, ok := toByScope[k]; !ok {
			out.Changes = append(out.Changes, change(fv, "removed", fv.Value, ""))
		}
	}
	sort.SliceStable(out.Changes, func(a, b int) bool {
		if out.Changes[a].InternalName != out.Changes[b].InternalName {
			return out.Changes[a].InternalName < out.Changes[b].InternalName
		}
		if out.Changes[a].Locale != out.Changes[b].Locale {
			return out.Changes[a].Locale < out.Changes[b].Locale
		}
		if out.Changes[a].Channel != out.Changes[b].Channel {
			return out.Changes[a].Channel < out.Changes[b].Channel
		}
		return out.Changes[a].AttributeDefinitionID < out.Changes[b].AttributeDefinitionID
	})
	return out, nil
}

// scopeKey identifies a revision value by attribute and scope, so scoped
// values of the same attribute are distinct map entries.
func scopeKey(v Value) string {
	return v.AttributeDefinitionID + "\x00" + v.Locale + "\x00" + v.Channel
}

// change builds a Change carrying the value's attribute identity and scope.
func change(v Value, kind, before, after string) Change {
	return Change{
		AttributeDefinitionID: v.AttributeDefinitionID,
		InternalName:          v.InternalName,
		Locale:                v.Locale,
		Channel:               v.Channel,
		Kind:                  kind,
		Before:                before,
		After:                 after,
	}
}

// Restore makes the entity's current values match a revision by applying its
// snapshot through the normal value write path, then captures the result as
// a new revision. History is never mutated.
func (i *Interactor) Restore(ctx context.Context, rawRevisionID string) (*Revision, error) {
	rev, err := i.Get(ctx, rawRevisionID)
	if err != nil {
		return nil, err
	}
	cells := make([]SnapshotCell, 0, len(rev.Values))
	for _, v := range rev.Values {
		cells = append(cells, SnapshotCell{
			AttributeDefinitionID: v.AttributeDefinitionID,
			DataType:              v.DataType,
			Locale:                v.Locale,
			Channel:               v.Channel,
			Value:                 v.Value,
		})
	}
	if err := i.applier.ApplySnapshot(ctx, rev.TypeDefinitionID, rev.EntityID, cells); err != nil {
		return nil, err
	}
	return i.Create(ctx, rev.TypeDefinitionID, rev.EntityID, "restored from revision "+itoa(rev.Seq))
}

// resolveEntity parses the type id, loads the type and enforces the tenant.
func (i *Interactor) resolveEntity(ctx context.Context, rawTypeID, entityID string) (valueobjects.TypeDefinitionID, valueobjects.TenantID, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(rawTypeID)
	if err != nil {
		return valueobjects.TypeDefinitionID{}, "", domainerrors.NewValidation(err.Error())
	}
	if _, err := valueobjects.ParseEntityID(entityID); err != nil {
		return valueobjects.TypeDefinitionID{}, "", domainerrors.NewValidation(err.Error())
	}
	t, err := i.typeDefs.Get(ctx, typeID)
	if err != nil {
		return valueobjects.TypeDefinitionID{}, "", err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", rawTypeID); err != nil {
		return valueobjects.TypeDefinitionID{}, "", err
	}
	return typeID, t.TenantID(), nil
}

// snapshotValues captures an entity's live values with their attribute names
// and data types.
func (i *Interactor) snapshotValues(ctx context.Context, tenant valueobjects.TenantID, typeID valueobjects.TypeDefinitionID, rawEntityID string) ([]Value, error) {
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	vals, err := i.values.ListByEntity(ctx, domainvalue.EntityKey{TenantID: tenant, TypeDefinitionID: typeID, EntityID: entityID})
	if err != nil {
		return nil, err
	}
	out := make([]Value, 0, len(vals))
	for _, av := range vals {
		def, err := i.attrs.Get(ctx, av.AttributeDefinitionID())
		if err != nil {
			return nil, err
		}
		s := def.Snapshot()
		scope := av.Scope()
		out = append(out, Value{
			AttributeDefinitionID: av.AttributeDefinitionID().String(),
			InternalName:          s.InternalName,
			DisplayName:           s.DisplayName,
			DataType:              string(av.Value().DataType()),
			Locale:                scope.Locale,
			Channel:               scope.Channel,
			Value:                 av.Value().String(),
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].InternalName != out[b].InternalName {
			return out[a].InternalName < out[b].InternalName
		}
		if out[a].Locale != out[b].Locale {
			return out[a].Locale < out[b].Locale
		}
		return out[a].Channel < out[b].Channel
	})
	return out, nil
}

// itoa avoids importing strconv for a single use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
