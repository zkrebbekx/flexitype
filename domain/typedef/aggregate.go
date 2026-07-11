// Package typedef holds the TypeDefinition aggregate: the named "class" of
// consumer entities (a product, a part, a ticket, ...) that attribute
// definitions attach to.
package typedef

import (
	"regexp"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

var internalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// Kind distinguishes ordinary entity types from the hidden attribute-set
// types owned by relationship definitions.
type Kind string

const (
	// KindEntity is a normal, user-facing type definition.
	KindEntity Kind = "entity"
	// KindRelationshipAttributes is the hidden companion type that holds a
	// relationship definition's attributes.
	KindRelationshipAttributes Kind = "relationship_attributes"
)

// TypeDefinition is the aggregate root for a soft type. All state is
// private; mutations go through methods that enforce invariants and return
// the domain events they produce.
type TypeDefinition struct {
	id           valueobjects.TypeDefinitionID
	tenantID     valueobjects.TenantID
	kind         Kind
	internalName string
	displayName  string
	description  string
	extendsID    *valueobjects.TypeDefinitionID
	version      int
	createdAt    time.Time
	updatedAt    time.Time
	archivedAt   *time.Time
}

// NewInput carries construction parameters for a TypeDefinition. Extends,
// when set, makes the new type a subtype inheriting the parent's whole
// effective schema; the pointer is immutable after creation (see
// docs/design/type-inheritance.md).
type NewInput struct {
	TenantID     valueobjects.TenantID
	InternalName string
	DisplayName  string
	Description  string
	Extends      *TypeDefinition
}

// New creates a TypeDefinition, returning the aggregate and the events it
// emitted.
func New(in NewInput, now time.Time) (*TypeDefinition, []events.Event, error) {
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

	var extendsID *valueobjects.TypeDefinitionID
	if in.Extends != nil {
		if in.Extends.Kind() != KindEntity {
			return nil, nil, domainerrors.NewValidation("only entity types can be extended")
		}
		if in.Extends.TenantID() != in.TenantID {
			return nil, nil, domainerrors.NewValidation("a type can only extend a type in the same tenant")
		}
		if in.Extends.IsArchived() {
			return nil, nil, domainerrors.NewValidation("cannot extend an archived type")
		}
		id := in.Extends.ID()
		extendsID = &id
	}

	t := &TypeDefinition{
		id:           valueobjects.NewTypeDefinitionID(),
		tenantID:     in.TenantID,
		kind:         KindEntity,
		internalName: in.InternalName,
		displayName:  in.DisplayName,
		description:  in.Description,
		extendsID:    extendsID,
		version:      1,
		createdAt:    now,
		updatedAt:    now,
	}
	return t, []events.Event{Created{
		TypeDefinitionID: t.id,
		TenantID:         t.tenantID,
		InternalName:     t.internalName,
		DisplayName:      t.displayName,
		ExtendsID:        extendsID,
		OccurredAt:       now,
	}}, nil
}

// Update changes the human-facing fields, bumping the version.
func (t *TypeDefinition) Update(displayName, description string, now time.Time) ([]events.Event, error) {
	if t.IsArchived() {
		return nil, domainerrors.NewArchived("type_definition", t.id.String())
	}
	if displayName == "" {
		return nil, domainerrors.NewValidation("display name is required")
	}
	if t.displayName == displayName && t.description == description {
		return nil, nil
	}

	t.displayName = displayName
	t.description = description
	t.version++
	t.updatedAt = now

	return []events.Event{Updated{
		TypeDefinitionID: t.id,
		TenantID:         t.tenantID,
		DisplayName:      t.displayName,
		Description:      t.description,
		Version:          t.version,
		OccurredAt:       now,
	}}, nil
}

// Archive soft-deletes the type definition.
func (t *TypeDefinition) Archive(now time.Time) ([]events.Event, error) {
	if t.IsArchived() {
		return nil, domainerrors.NewArchived("type_definition", t.id.String())
	}
	t.archivedAt = &now
	t.updatedAt = now

	return []events.Event{Archived{
		TypeDefinitionID: t.id,
		TenantID:         t.tenantID,
		OccurredAt:       now,
	}}, nil
}

// Restore reverses an Archive.
func (t *TypeDefinition) Restore(now time.Time) ([]events.Event, error) {
	if !t.IsArchived() {
		return nil, domainerrors.NewValidation("type definition is not archived")
	}
	t.archivedAt = nil
	t.updatedAt = now

	return []events.Event{Restored{
		TypeDefinitionID: t.id,
		TenantID:         t.tenantID,
		OccurredAt:       now,
	}}, nil
}

// NewAttributeSet creates the hidden companion type a relationship
// definition owns for its attributes. It never appears in entity-type
// listings.
func NewAttributeSet(tenant valueobjects.TenantID, internalName, displayName string, now time.Time) (*TypeDefinition, []events.Event, error) {
	t, evts, err := New(NewInput{TenantID: tenant, InternalName: internalName, DisplayName: displayName}, now)
	if err != nil {
		return nil, nil, err
	}
	t.kind = KindRelationshipAttributes
	return t, evts, nil
}

// ID returns the aggregate identifier.
func (t *TypeDefinition) ID() valueobjects.TypeDefinitionID { return t.id }

// Kind returns whether this is an entity type or a relationship attribute
// set.
func (t *TypeDefinition) Kind() Kind { return t.kind }

// ExtendsID returns the parent type when this is a subtype, or nil.
func (t *TypeDefinition) ExtendsID() *valueobjects.TypeDefinitionID { return t.extendsID }

// TenantID returns the owning tenant.
func (t *TypeDefinition) TenantID() valueobjects.TenantID { return t.tenantID }

// InternalName returns the immutable machine name.
func (t *TypeDefinition) InternalName() string { return t.internalName }

// DisplayName returns the human name.
func (t *TypeDefinition) DisplayName() string { return t.displayName }

// Description returns the description.
func (t *TypeDefinition) Description() string { return t.description }

// Version returns the optimistic version counter.
func (t *TypeDefinition) Version() int { return t.version }

// CreatedAt returns the creation instant.
func (t *TypeDefinition) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt returns the last mutation instant.
func (t *TypeDefinition) UpdatedAt() time.Time { return t.updatedAt }

// ArchivedAt returns the archive instant, if archived.
func (t *TypeDefinition) ArchivedAt() *time.Time { return t.archivedAt }

// IsArchived reports whether the type definition is archived.
func (t *TypeDefinition) IsArchived() bool { return t.archivedAt != nil }

// Snapshot is the exported, JSON-friendly projection of a TypeDefinition,
// used for persistence, activity-log descriptors and API mapping.
type Snapshot struct {
	ID           valueobjects.TypeDefinitionID  `json:"id"`
	TenantID     valueobjects.TenantID          `json:"tenant_id"`
	Kind         Kind                           `json:"kind"`
	InternalName string                         `json:"internal_name"`
	DisplayName  string                         `json:"display_name"`
	Description  string                         `json:"description,omitempty"`
	ExtendsID    *valueobjects.TypeDefinitionID `json:"extends_id,omitempty"`
	Version      int                            `json:"version"`
	CreatedAt    time.Time                      `json:"created_at"`
	UpdatedAt    time.Time                      `json:"updated_at"`
	ArchivedAt   *time.Time                     `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (t *TypeDefinition) Snapshot() Snapshot {
	return Snapshot{
		ID:           t.id,
		TenantID:     t.tenantID,
		Kind:         t.kind,
		InternalName: t.internalName,
		DisplayName:  t.displayName,
		Description:  t.description,
		ExtendsID:    t.extendsID,
		Version:      t.version,
		CreatedAt:    t.createdAt,
		UpdatedAt:    t.updatedAt,
		ArchivedAt:   t.archivedAt,
	}
}

// Rehydrate rebuilds the aggregate from a persisted snapshot. Repository
// use only — it bypasses invariant checks.
func Rehydrate(s Snapshot) *TypeDefinition {
	kind := s.Kind
	if kind == "" {
		kind = KindEntity
	}
	return &TypeDefinition{
		id:           s.ID,
		tenantID:     s.TenantID,
		kind:         kind,
		internalName: s.InternalName,
		displayName:  s.DisplayName,
		extendsID:    s.ExtendsID,
		description:  s.Description,
		version:      s.Version,
		createdAt:    s.CreatedAt,
		updatedAt:    s.UpdatedAt,
		archivedAt:   s.ArchivedAt,
	}
}
