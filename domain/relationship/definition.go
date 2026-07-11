// Package relationship holds the relationship aggregates: Definition (a
// user-defined relationship type between a parent type and a child type,
// optionally inheriting from another definition, with its own attributes
// held by a hidden companion attribute-set type) and Relationship (one
// link between two entities, optionally pinned to specific type versions).
package relationship

import (
	"regexp"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

var internalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// VersionPolicy declares how one side of a relationship binds to its
// endpoint's type-definition version.
type VersionPolicy string

const (
	// PolicyLatest tracks the endpoint type's latest version.
	PolicyLatest VersionPolicy = "latest"
	// PolicyPinned requires each link to pin a specific version.
	PolicyPinned VersionPolicy = "pinned"
)

func (p VersionPolicy) valid() bool { return p == PolicyLatest || p == PolicyPinned }

// Definition is the aggregate root for a relationship type.
type Definition struct {
	id                  valueobjects.RelationshipDefinitionID
	tenantID            valueobjects.TenantID
	internalName        string
	displayName         string
	description         string
	parentTypeID        valueobjects.TypeDefinitionID
	childTypeID         valueobjects.TypeDefinitionID
	attributeSetID      valueobjects.TypeDefinitionID
	extendsID           *valueobjects.RelationshipDefinitionID
	parentVersionPolicy VersionPolicy
	childVersionPolicy  VersionPolicy
	version             int
	createdAt           time.Time
	updatedAt           time.Time
	archivedAt          *time.Time
}

// NewDefinitionInput carries construction parameters for a Definition. The
// usecase resolves and passes the endpoint types, the pre-created attribute
// set, and the base definition when extending.
type NewDefinitionInput struct {
	TenantID            valueobjects.TenantID
	InternalName        string
	DisplayName         string
	Description         string
	ParentType          *typedef.TypeDefinition
	ChildType           *typedef.TypeDefinition
	AttributeSet        *typedef.TypeDefinition
	Extends             *Definition
	ParentVersionPolicy VersionPolicy
	ChildVersionPolicy  VersionPolicy
}

// NewDefinition creates a relationship Definition.
func NewDefinition(in NewDefinitionInput, now time.Time) (*Definition, []events.Event, error) {
	if in.TenantID.IsZero() {
		in.TenantID = valueobjects.DefaultTenant
	}
	if !internalNamePattern.MatchString(in.InternalName) {
		return nil, nil, domainerrors.NewValidation(
			"internal name must be snake_case, start with a letter and be 2-63 characters",
			"internal_name", in.InternalName,
		)
	}
	if in.DisplayName == "" {
		return nil, nil, domainerrors.NewValidation("display name is required")
	}
	if in.ParentType == nil || in.ChildType == nil {
		return nil, nil, domainerrors.NewValidation("parent and child types are required")
	}
	if in.ParentType.Kind() != typedef.KindEntity || in.ChildType.Kind() != typedef.KindEntity {
		return nil, nil, domainerrors.NewValidation("relationship endpoints must be entity types")
	}
	if in.ParentType.IsArchived() || in.ChildType.IsArchived() {
		return nil, nil, domainerrors.NewValidation("relationship endpoints must not be archived")
	}
	if in.AttributeSet == nil || in.AttributeSet.Kind() != typedef.KindRelationshipAttributes {
		return nil, nil, domainerrors.NewValidation("a relationship definition requires its attribute-set type")
	}
	if in.ParentVersionPolicy == "" {
		in.ParentVersionPolicy = PolicyLatest
	}
	if in.ChildVersionPolicy == "" {
		in.ChildVersionPolicy = PolicyLatest
	}
	if !in.ParentVersionPolicy.valid() || !in.ChildVersionPolicy.valid() {
		return nil, nil, domainerrors.NewValidation("version policies must be latest or pinned")
	}

	var extendsID *valueobjects.RelationshipDefinitionID
	if in.Extends != nil {
		// Inheritance: the extending definition must connect the same
		// endpoint types; its attribute set layers on top of the base's.
		if in.Extends.IsArchived() {
			return nil, nil, domainerrors.NewValidation("cannot extend an archived relationship definition")
		}
		if !in.Extends.ParentTypeID().Equals(in.ParentType.ID()) || !in.Extends.ChildTypeID().Equals(in.ChildType.ID()) {
			return nil, nil, domainerrors.NewValidation(
				"an extending relationship definition must connect the same parent and child types as its base")
		}
		id := in.Extends.ID()
		extendsID = &id
	}

	d := &Definition{
		id:                  valueobjects.NewRelationshipDefinitionID(),
		tenantID:            in.TenantID,
		internalName:        in.InternalName,
		displayName:         in.DisplayName,
		description:         in.Description,
		parentTypeID:        in.ParentType.ID(),
		childTypeID:         in.ChildType.ID(),
		attributeSetID:      in.AttributeSet.ID(),
		extendsID:           extendsID,
		parentVersionPolicy: in.ParentVersionPolicy,
		childVersionPolicy:  in.ChildVersionPolicy,
		version:             1,
		createdAt:           now,
		updatedAt:           now,
	}
	return d, []events.Event{DefinitionCreated{
		RelationshipDefinitionID: d.id,
		TenantID:                 d.tenantID,
		InternalName:             d.internalName,
		ParentTypeID:             d.parentTypeID,
		ChildTypeID:              d.childTypeID,
		OccurredAt:               now,
	}}, nil
}

// UpdateDefinitionInput carries the mutable fields of a Definition.
// Endpoints, inheritance and the attribute set are immutable — they anchor
// existing links.
type UpdateDefinitionInput struct {
	DisplayName         string
	Description         string
	ParentVersionPolicy VersionPolicy
	ChildVersionPolicy  VersionPolicy
}

// Update mutates the definition, bumping the version.
func (d *Definition) Update(in UpdateDefinitionInput, now time.Time) ([]events.Event, error) {
	if d.IsArchived() {
		return nil, domainerrors.NewArchived(DefinitionAggregateType, d.id.String())
	}
	if in.DisplayName == "" {
		return nil, domainerrors.NewValidation("display name is required")
	}
	if in.ParentVersionPolicy == "" {
		in.ParentVersionPolicy = d.parentVersionPolicy
	}
	if in.ChildVersionPolicy == "" {
		in.ChildVersionPolicy = d.childVersionPolicy
	}
	if !in.ParentVersionPolicy.valid() || !in.ChildVersionPolicy.valid() {
		return nil, domainerrors.NewValidation("version policies must be latest or pinned")
	}
	if d.displayName == in.DisplayName && d.description == in.Description &&
		d.parentVersionPolicy == in.ParentVersionPolicy && d.childVersionPolicy == in.ChildVersionPolicy {
		return nil, nil
	}

	d.displayName = in.DisplayName
	d.description = in.Description
	d.parentVersionPolicy = in.ParentVersionPolicy
	d.childVersionPolicy = in.ChildVersionPolicy
	d.version++
	d.updatedAt = now

	return []events.Event{DefinitionUpdated{
		RelationshipDefinitionID: d.id,
		TenantID:                 d.tenantID,
		Version:                  d.version,
		OccurredAt:               now,
	}}, nil
}

// Archive soft-deletes the definition.
func (d *Definition) Archive(now time.Time) ([]events.Event, error) {
	if d.IsArchived() {
		return nil, domainerrors.NewArchived(DefinitionAggregateType, d.id.String())
	}
	d.archivedAt = &now
	d.updatedAt = now

	return []events.Event{DefinitionArchived{
		RelationshipDefinitionID: d.id,
		TenantID:                 d.tenantID,
		OccurredAt:               now,
	}}, nil
}

// Restore reverses an Archive.
func (d *Definition) Restore(now time.Time) ([]events.Event, error) {
	if !d.IsArchived() {
		return nil, domainerrors.NewValidation("relationship definition is not archived")
	}
	d.archivedAt = nil
	d.updatedAt = now

	return []events.Event{DefinitionRestored{
		RelationshipDefinitionID: d.id,
		TenantID:                 d.tenantID,
		OccurredAt:               now,
	}}, nil
}

// ID returns the aggregate identifier.
func (d *Definition) ID() valueobjects.RelationshipDefinitionID { return d.id }

// TenantID returns the owning tenant.
func (d *Definition) TenantID() valueobjects.TenantID { return d.tenantID }

// InternalName returns the immutable machine name.
func (d *Definition) InternalName() string { return d.internalName }

// DisplayName returns the human name.
func (d *Definition) DisplayName() string { return d.displayName }

// Description returns the description.
func (d *Definition) Description() string { return d.description }

// ParentTypeID returns the parent-side entity type.
func (d *Definition) ParentTypeID() valueobjects.TypeDefinitionID { return d.parentTypeID }

// ChildTypeID returns the child-side entity type.
func (d *Definition) ChildTypeID() valueobjects.TypeDefinitionID { return d.childTypeID }

// AttributeSetID returns the hidden companion type holding this
// definition's attributes; link values anchor to it with the relationship
// ID as the entity.
func (d *Definition) AttributeSetID() valueobjects.TypeDefinitionID { return d.attributeSetID }

// ExtendsID returns the base definition when this one inherits, or nil.
func (d *Definition) ExtendsID() *valueobjects.RelationshipDefinitionID { return d.extendsID }

// ParentVersionPolicy returns how links bind to the parent type's version.
func (d *Definition) ParentVersionPolicy() VersionPolicy { return d.parentVersionPolicy }

// ChildVersionPolicy returns how links bind to the child type's version.
func (d *Definition) ChildVersionPolicy() VersionPolicy { return d.childVersionPolicy }

// Version returns the optimistic version counter.
func (d *Definition) Version() int { return d.version }

// CreatedAt returns the creation instant.
func (d *Definition) CreatedAt() time.Time { return d.createdAt }

// UpdatedAt returns the last mutation instant.
func (d *Definition) UpdatedAt() time.Time { return d.updatedAt }

// ArchivedAt returns the archive instant, if archived.
func (d *Definition) ArchivedAt() *time.Time { return d.archivedAt }

// IsArchived reports whether the definition is archived.
func (d *Definition) IsArchived() bool { return d.archivedAt != nil }

// DefinitionSnapshot is the exported projection of a Definition.
type DefinitionSnapshot struct {
	ID                  valueobjects.RelationshipDefinitionID  `json:"id"`
	TenantID            valueobjects.TenantID                  `json:"tenant_id"`
	InternalName        string                                 `json:"internal_name"`
	DisplayName         string                                 `json:"display_name"`
	Description         string                                 `json:"description,omitempty"`
	ParentTypeID        valueobjects.TypeDefinitionID          `json:"parent_type_id"`
	ChildTypeID         valueobjects.TypeDefinitionID          `json:"child_type_id"`
	AttributeSetID      valueobjects.TypeDefinitionID          `json:"attribute_set_id"`
	ExtendsID           *valueobjects.RelationshipDefinitionID `json:"extends_id,omitempty"`
	ParentVersionPolicy VersionPolicy                          `json:"parent_version_policy"`
	ChildVersionPolicy  VersionPolicy                          `json:"child_version_policy"`
	Version             int                                    `json:"version"`
	CreatedAt           time.Time                              `json:"created_at"`
	UpdatedAt           time.Time                              `json:"updated_at"`
	ArchivedAt          *time.Time                             `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (d *Definition) Snapshot() DefinitionSnapshot {
	return DefinitionSnapshot{
		ID:                  d.id,
		TenantID:            d.tenantID,
		InternalName:        d.internalName,
		DisplayName:         d.displayName,
		Description:         d.description,
		ParentTypeID:        d.parentTypeID,
		ChildTypeID:         d.childTypeID,
		AttributeSetID:      d.attributeSetID,
		ExtendsID:           d.extendsID,
		ParentVersionPolicy: d.parentVersionPolicy,
		ChildVersionPolicy:  d.childVersionPolicy,
		Version:             d.version,
		CreatedAt:           d.createdAt,
		UpdatedAt:           d.updatedAt,
		ArchivedAt:          d.archivedAt,
	}
}

// RehydrateDefinition rebuilds the aggregate from a persisted snapshot.
// Repository use only.
func RehydrateDefinition(s DefinitionSnapshot) *Definition {
	return &Definition{
		id:                  s.ID,
		tenantID:            s.TenantID,
		internalName:        s.InternalName,
		displayName:         s.DisplayName,
		description:         s.Description,
		parentTypeID:        s.ParentTypeID,
		childTypeID:         s.ChildTypeID,
		attributeSetID:      s.AttributeSetID,
		extendsID:           s.ExtendsID,
		parentVersionPolicy: s.ParentVersionPolicy,
		childVersionPolicy:  s.ChildVersionPolicy,
		version:             s.Version,
		createdAt:           s.CreatedAt,
		updatedAt:           s.UpdatedAt,
		archivedAt:          s.ArchivedAt,
	}
}
