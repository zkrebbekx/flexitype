// Package value holds the AttributeValue aggregate: a typed value of an
// attribute definition, anchored to the consumer's own entity via EntityID.
package value

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// AttributeValue is the aggregate root for one stored value. It pins the
// definition version it was validated against, so definition evolution
// never silently re-interprets existing data.
type AttributeValue struct {
	id                valueobjects.AttributeValueID
	tenantID          valueobjects.TenantID
	typeDefinitionID  valueobjects.TypeDefinitionID
	attributeDefID    valueobjects.AttributeDefinitionID
	entityID          valueobjects.EntityID
	value             valueobjects.Value
	definitionVersion int
	createdAt         time.Time
	updatedAt         time.Time
	archivedAt        *time.Time
}

// New validates v against the definition and creates the value. entityType
// is the entity's declared (most-derived) type: with inheritance it may be
// a descendant of the attribute's declaring type, and the value anchors to
// it so per-entity hydration stays a single lookup (see
// docs/design/type-inheritance.md). The caller proves the ancestry.
func New(def *attribute.Definition, entityType valueobjects.TypeDefinitionID, entityID valueobjects.EntityID, v valueobjects.Value, now time.Time) (*AttributeValue, []events.Event, error) {
	if entityID.IsZero() {
		return nil, nil, domainerrors.NewValidation("entity ID is required")
	}
	if entityType.IsZero() {
		return nil, nil, domainerrors.NewValidation("entity type is required")
	}
	if v.IsZero() {
		return nil, nil, domainerrors.NewValidation("value is required")
	}
	if err := def.ValidateValue(v); err != nil {
		return nil, nil, err
	}

	av := &AttributeValue{
		id:                valueobjects.NewAttributeValueID(),
		tenantID:          def.TenantID(),
		typeDefinitionID:  entityType,
		attributeDefID:    def.ID(),
		entityID:          entityID,
		value:             v,
		definitionVersion: def.Version(),
		createdAt:         now,
		updatedAt:         now,
	}
	return av, []events.Event{Set{
		AttributeValueID:      av.id,
		TenantID:              av.tenantID,
		TypeDefinitionID:      av.typeDefinitionID,
		AttributeDefinitionID: av.attributeDefID,
		EntityID:              av.entityID,
		Value:                 av.value,
		DefinitionVersion:     av.definitionVersion,
		OccurredAt:            now,
	}}, nil
}

// UpdateValue replaces the stored value after re-validating against the
// (possibly newer) definition, re-pinning the definition version.
func (av *AttributeValue) UpdateValue(def *attribute.Definition, v valueobjects.Value, now time.Time) ([]events.Event, error) {
	if av.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, av.id.String())
	}
	if !def.ID().Equals(av.attributeDefID) {
		return nil, domainerrors.NewValidation("definition does not match the stored value's attribute")
	}
	if v.IsZero() {
		return nil, domainerrors.NewValidation("value is required")
	}
	if err := def.ValidateValue(v); err != nil {
		return nil, err
	}
	if av.value.Equal(v) && av.definitionVersion == def.Version() {
		return nil, nil
	}

	old := av.value
	av.value = v
	av.definitionVersion = def.Version()
	av.updatedAt = now

	return []events.Event{Updated{
		AttributeValueID:      av.id,
		TenantID:              av.tenantID,
		TypeDefinitionID:      av.typeDefinitionID,
		AttributeDefinitionID: av.attributeDefID,
		EntityID:              av.entityID,
		OldValue:              old,
		NewValue:              av.value,
		DefinitionVersion:     av.definitionVersion,
		OccurredAt:            now,
	}}, nil
}

// Remove archives the value (soft delete).
func (av *AttributeValue) Remove(now time.Time) ([]events.Event, error) {
	if av.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, av.id.String())
	}
	av.archivedAt = &now
	av.updatedAt = now

	return []events.Event{Removed{
		AttributeValueID:      av.id,
		TenantID:              av.tenantID,
		TypeDefinitionID:      av.typeDefinitionID,
		AttributeDefinitionID: av.attributeDefID,
		EntityID:              av.entityID,
		Value:                 av.value,
		OccurredAt:            now,
	}}, nil
}

// ID returns the aggregate identifier.
func (av *AttributeValue) ID() valueobjects.AttributeValueID { return av.id }

// TenantID returns the owning tenant.
func (av *AttributeValue) TenantID() valueobjects.TenantID { return av.tenantID }

// TypeDefinitionID returns the entity's type definition.
func (av *AttributeValue) TypeDefinitionID() valueobjects.TypeDefinitionID {
	return av.typeDefinitionID
}

// AttributeDefinitionID returns the attribute this value belongs to.
func (av *AttributeValue) AttributeDefinitionID() valueobjects.AttributeDefinitionID {
	return av.attributeDefID
}

// EntityID returns the consumer entity anchor.
func (av *AttributeValue) EntityID() valueobjects.EntityID { return av.entityID }

// Value returns the typed value.
func (av *AttributeValue) Value() valueobjects.Value { return av.value }

// DefinitionVersion returns the definition version the value was validated
// against.
func (av *AttributeValue) DefinitionVersion() int { return av.definitionVersion }

// CreatedAt returns the creation instant.
func (av *AttributeValue) CreatedAt() time.Time { return av.createdAt }

// UpdatedAt returns the last mutation instant.
func (av *AttributeValue) UpdatedAt() time.Time { return av.updatedAt }

// ArchivedAt returns the archive instant, if removed.
func (av *AttributeValue) ArchivedAt() *time.Time { return av.archivedAt }

// IsArchived reports whether the value was removed.
func (av *AttributeValue) IsArchived() bool { return av.archivedAt != nil }

// Snapshot is the exported projection of an AttributeValue.
type Snapshot struct {
	ID                    valueobjects.AttributeValueID      `json:"id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	EntityID              valueobjects.EntityID              `json:"entity_id"`
	Value                 valueobjects.Value                 `json:"value"`
	DefinitionVersion     int                                `json:"definition_version"`
	CreatedAt             time.Time                          `json:"created_at"`
	UpdatedAt             time.Time                          `json:"updated_at"`
	ArchivedAt            *time.Time                         `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (av *AttributeValue) Snapshot() Snapshot {
	return Snapshot{
		ID:                    av.id,
		TenantID:              av.tenantID,
		TypeDefinitionID:      av.typeDefinitionID,
		AttributeDefinitionID: av.attributeDefID,
		EntityID:              av.entityID,
		Value:                 av.value,
		DefinitionVersion:     av.definitionVersion,
		CreatedAt:             av.createdAt,
		UpdatedAt:             av.updatedAt,
		ArchivedAt:            av.archivedAt,
	}
}

// Rehydrate rebuilds the aggregate from a persisted snapshot. Repository
// use only.
func Rehydrate(s Snapshot) *AttributeValue {
	return &AttributeValue{
		id:                s.ID,
		tenantID:          s.TenantID,
		typeDefinitionID:  s.TypeDefinitionID,
		attributeDefID:    s.AttributeDefinitionID,
		entityID:          s.EntityID,
		value:             s.Value,
		definitionVersion: s.DefinitionVersion,
		createdAt:         s.CreatedAt,
		updatedAt:         s.UpdatedAt,
		archivedAt:        s.ArchivedAt,
	}
}
