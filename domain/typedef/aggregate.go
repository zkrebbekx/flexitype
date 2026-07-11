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

// TypeDefinition is the aggregate root for a soft type. All state is
// private; mutations go through methods that enforce invariants and return
// the domain events they produce.
type TypeDefinition struct {
	id           valueobjects.TypeDefinitionID
	tenantID     valueobjects.TenantID
	internalName string
	displayName  string
	description  string
	version      int
	createdAt    time.Time
	updatedAt    time.Time
	archivedAt   *time.Time
}

// New creates a TypeDefinition, returning the aggregate and the events it
// emitted.
func New(tenant valueobjects.TenantID, internalName, displayName, description string, now time.Time) (*TypeDefinition, []events.Event, error) {
	if tenant.IsZero() {
		tenant = valueobjects.DefaultTenant
	}
	if !internalNamePattern.MatchString(internalName) {
		return nil, nil, domainerrors.NewValidation(
			"internal name must be snake_case, start with a letter and be 2-63 characters",
			"internal_name", internalName,
		)
	}
	if displayName == "" {
		return nil, nil, domainerrors.NewValidation("display name is required")
	}

	t := &TypeDefinition{
		id:           valueobjects.NewTypeDefinitionID(),
		tenantID:     tenant,
		internalName: internalName,
		displayName:  displayName,
		description:  description,
		version:      1,
		createdAt:    now,
		updatedAt:    now,
	}
	return t, []events.Event{Created{
		TypeDefinitionID: t.id,
		TenantID:         t.tenantID,
		InternalName:     t.internalName,
		DisplayName:      t.displayName,
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

// ID returns the aggregate identifier.
func (t *TypeDefinition) ID() valueobjects.TypeDefinitionID { return t.id }

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
	ID           valueobjects.TypeDefinitionID `json:"id"`
	TenantID     valueobjects.TenantID         `json:"tenant_id"`
	InternalName string                        `json:"internal_name"`
	DisplayName  string                        `json:"display_name"`
	Description  string                        `json:"description,omitempty"`
	Version      int                           `json:"version"`
	CreatedAt    time.Time                     `json:"created_at"`
	UpdatedAt    time.Time                     `json:"updated_at"`
	ArchivedAt   *time.Time                    `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (t *TypeDefinition) Snapshot() Snapshot {
	return Snapshot{
		ID:           t.id,
		TenantID:     t.tenantID,
		InternalName: t.internalName,
		DisplayName:  t.displayName,
		Description:  t.description,
		Version:      t.version,
		CreatedAt:    t.createdAt,
		UpdatedAt:    t.updatedAt,
		ArchivedAt:   t.archivedAt,
	}
}

// Rehydrate rebuilds the aggregate from a persisted snapshot. Repository
// use only — it bypasses invariant checks.
func Rehydrate(s Snapshot) *TypeDefinition {
	return &TypeDefinition{
		id:           s.ID,
		tenantID:     s.TenantID,
		internalName: s.InternalName,
		displayName:  s.DisplayName,
		description:  s.Description,
		version:      s.Version,
		createdAt:    s.CreatedAt,
		updatedAt:    s.UpdatedAt,
		archivedAt:   s.ArchivedAt,
	}
}
