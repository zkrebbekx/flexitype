// Package attribute holds the Definition aggregate: a typed,
// constrained soft attribute attached to a type definition.
package attribute

import (
	"regexp"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

var internalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// Definition is the aggregate root for a soft attribute. State is
// private; mutations enforce invariants and return domain events.
type Definition struct {
	id               valueobjects.AttributeDefinitionID
	tenantID         valueobjects.TenantID
	typeDefinitionID valueobjects.TypeDefinitionID
	internalName     string
	displayName      string
	description      string
	dataType         valueobjects.DataType
	required         bool
	multiValued      bool
	unique           bool
	localizable      bool
	scopable         bool
	constraints      Constraints
	defaultValue     *valueobjects.Default
	group            string
	sortOrder        int
	helpText         string
	version          int
	createdAt        time.Time
	updatedAt        time.Time
	archivedAt       *time.Time
}

// NewInput carries the construction parameters for an Definition.
type NewInput struct {
	TenantID         valueobjects.TenantID
	TypeDefinitionID valueobjects.TypeDefinitionID
	InternalName     string
	DisplayName      string
	Description      string
	DataType         valueobjects.DataType
	Required         bool
	MultiValued      bool
	Unique           bool
	// Localizable lets an attribute hold a value per locale; Scopable, a
	// value per channel. Together the value identity is (entity, attribute,
	// locale, channel).
	Localizable  bool
	Scopable     bool
	Constraints  Constraints
	DefaultValue *valueobjects.Default
	// Presentation metadata (optional): the section this attribute belongs
	// to, its order within the type, and inline help.
	Group     string
	SortOrder int
	HelpText  string
}

// New creates an Definition, returning the aggregate and its
// events.
func New(in NewInput, now time.Time) (*Definition, []events.Event, error) {
	if in.TenantID.IsZero() {
		in.TenantID = valueobjects.DefaultTenant
	}
	if in.TypeDefinitionID.IsZero() {
		return nil, nil, domainerrors.NewValidation("type definition ID is required")
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
	if _, err := valueobjects.ParseDataType(in.DataType.String()); err != nil {
		return nil, nil, domainerrors.NewValidation(err.Error())
	}
	if err := validateRules(in.DataType, in.MultiValued, in.Unique, in.Constraints, in.DefaultValue); err != nil {
		return nil, nil, err
	}

	a := &Definition{
		id:               valueobjects.NewAttributeDefinitionID(),
		tenantID:         in.TenantID,
		typeDefinitionID: in.TypeDefinitionID,
		internalName:     in.InternalName,
		displayName:      in.DisplayName,
		description:      in.Description,
		dataType:         in.DataType,
		required:         in.Required,
		multiValued:      in.MultiValued,
		unique:           in.Unique,
		localizable:      in.Localizable,
		scopable:         in.Scopable,
		constraints:      in.Constraints,
		defaultValue:     in.DefaultValue,
		group:            in.Group,
		sortOrder:        in.SortOrder,
		helpText:         in.HelpText,
		version:          1,
		createdAt:        now,
		updatedAt:        now,
	}
	return a, []events.Event{Created{
		AttributeDefinitionID: a.id,
		TenantID:              a.tenantID,
		TypeDefinitionID:      a.typeDefinitionID,
		InternalName:          a.internalName,
		DisplayName:           a.displayName,
		DataType:              a.dataType,
		Required:              a.required,
		OccurredAt:            now,
	}}, nil
}

// UpdateInput carries the mutable fields of an Definition. The
// data type and internal name are immutable: changing them would silently
// invalidate stored values.
type UpdateInput struct {
	DisplayName  string
	Description  string
	Required     bool
	MultiValued  bool
	Unique       bool
	Localizable  bool
	Scopable     bool
	Constraints  Constraints
	DefaultValue *valueobjects.Default
	Group        string
	SortOrder    int
	HelpText     string
}

// Update mutates the definition, bumping the version. Stored values keep
// the version they were validated against.
func (a *Definition) Update(in UpdateInput, now time.Time) ([]events.Event, error) {
	if a.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, a.id.String())
	}
	if in.DisplayName == "" {
		return nil, domainerrors.NewValidation("display name is required")
	}
	if err := validateRules(a.dataType, in.MultiValued, in.Unique, in.Constraints, in.DefaultValue); err != nil {
		return nil, err
	}

	a.displayName = in.DisplayName
	a.description = in.Description
	a.required = in.Required
	a.multiValued = in.MultiValued
	a.unique = in.Unique
	a.localizable = in.Localizable
	a.scopable = in.Scopable
	a.constraints = in.Constraints
	a.defaultValue = in.DefaultValue
	a.group = in.Group
	a.sortOrder = in.SortOrder
	a.helpText = in.HelpText
	a.version++
	a.updatedAt = now

	return []events.Event{Updated{
		AttributeDefinitionID: a.id,
		TenantID:              a.tenantID,
		TypeDefinitionID:      a.typeDefinitionID,
		DisplayName:           a.displayName,
		Required:              a.required,
		Version:               a.version,
		OccurredAt:            now,
	}}, nil
}

// Archive soft-deletes the attribute definition.
func (a *Definition) Archive(now time.Time) ([]events.Event, error) {
	if a.IsArchived() {
		return nil, domainerrors.NewArchived(AggregateType, a.id.String())
	}
	a.archivedAt = &now
	a.updatedAt = now

	return []events.Event{Archived{
		AttributeDefinitionID: a.id,
		TenantID:              a.tenantID,
		TypeDefinitionID:      a.typeDefinitionID,
		OccurredAt:            now,
	}}, nil
}

// Restore reverses an Archive.
func (a *Definition) Restore(now time.Time) ([]events.Event, error) {
	if !a.IsArchived() {
		return nil, domainerrors.NewValidation("attribute definition is not archived")
	}
	a.archivedAt = nil
	a.updatedAt = now

	return []events.Event{Restored{
		AttributeDefinitionID: a.id,
		TenantID:              a.tenantID,
		TypeDefinitionID:      a.typeDefinitionID,
		OccurredAt:            now,
	}}, nil
}

// ValidateValue checks a candidate value against the attribute's type and
// constraints. It is the single validation gate for value writes.
func (a *Definition) ValidateValue(v valueobjects.Value) error {
	if a.IsArchived() {
		return domainerrors.NewArchived(AggregateType, a.id.String())
	}
	if v.IsZero() {
		if a.required {
			return domainerrors.NewValidation("value is required", "attribute", a.internalName)
		}
		return nil
	}
	if v.DataType() != a.dataType {
		return domainerrors.NewValidation("value type does not match attribute type",
			"value_type", v.DataType().String(), "data_type", a.dataType.String())
	}
	return a.constraints.Check(v)
}

// DefaultFor resolves the attribute's default value at instant now, or the
// zero Value when no default is declared.
func (a *Definition) DefaultFor(now time.Time) (valueobjects.Value, error) {
	if a.defaultValue == nil {
		return valueobjects.Value{}, nil
	}
	return a.defaultValue.Resolve(a.dataType, now)
}

func validateRules(dt valueobjects.DataType, multiValued, unique bool, cs Constraints, def *valueobjects.Default) error {
	if multiValued && unique {
		return domainerrors.NewValidation("an attribute cannot be both multi-valued and unique")
	}
	if dt == valueobjects.DataTypeEnum && !hasOneOf(cs) {
		return domainerrors.NewValidation("enum attributes require a one_of constraint")
	}
	if err := cs.Validate(dt); err != nil {
		return err
	}
	if def != nil {
		if err := def.Validate(dt); err != nil {
			return domainerrors.NewValidation(err.Error())
		}
		if def.Static != nil {
			if err := cs.Check(*def.Static); err != nil {
				return domainerrors.NewValidation("default value violates constraints", "error", err.Error())
			}
		}
	}
	return nil
}

func hasOneOf(cs Constraints) bool {
	for _, c := range cs {
		if c.Kind() == KindOneOf {
			return true
		}
	}
	return false
}

// ID returns the aggregate identifier.
func (a *Definition) ID() valueobjects.AttributeDefinitionID { return a.id }

// TenantID returns the owning tenant.
func (a *Definition) TenantID() valueobjects.TenantID { return a.tenantID }

// TypeDefinitionID returns the owning type definition.
func (a *Definition) TypeDefinitionID() valueobjects.TypeDefinitionID {
	return a.typeDefinitionID
}

// InternalName returns the immutable machine name.
func (a *Definition) InternalName() string { return a.internalName }

// DisplayName returns the human name.
func (a *Definition) DisplayName() string { return a.displayName }

// Description returns the description.
func (a *Definition) Description() string { return a.description }

// DataType returns the attribute's soft type.
func (a *Definition) DataType() valueobjects.DataType { return a.dataType }

// Required reports whether values are mandatory for complete entities.
func (a *Definition) Required() bool { return a.required }

// MultiValued reports whether an entity may hold multiple values.
func (a *Definition) MultiValued() bool { return a.multiValued }

// Unique reports whether values must be unique across entities.
func (a *Definition) Unique() bool { return a.unique }

// Localizable reports whether the attribute holds a value per locale.
func (a *Definition) Localizable() bool { return a.localizable }

// Scopable reports whether the attribute holds a value per channel.
func (a *Definition) Scopable() bool { return a.scopable }

// Constraints returns the validation rules.
func (a *Definition) Constraints() Constraints { return a.constraints }

// DefaultValue returns the declared default, if any.
func (a *Definition) DefaultValue() *valueobjects.Default { return a.defaultValue }

// Version returns the definition version stored values pin to.
func (a *Definition) Version() int { return a.version }

// CreatedAt returns the creation instant.
func (a *Definition) CreatedAt() time.Time { return a.createdAt }

// UpdatedAt returns the last mutation instant.
func (a *Definition) UpdatedAt() time.Time { return a.updatedAt }

// ArchivedAt returns the archive instant, if archived.
func (a *Definition) ArchivedAt() *time.Time { return a.archivedAt }

// IsArchived reports whether the definition is archived.
func (a *Definition) IsArchived() bool { return a.archivedAt != nil }

// Snapshot is the exported projection of an Definition used for
// persistence, activity-log descriptors and API mapping.
type Snapshot struct {
	ID               valueobjects.AttributeDefinitionID `json:"id"`
	TenantID         valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	InternalName     string                             `json:"internal_name"`
	DisplayName      string                             `json:"display_name"`
	Description      string                             `json:"description,omitempty"`
	DataType         valueobjects.DataType              `json:"data_type"`
	Required         bool                               `json:"required"`
	MultiValued      bool                               `json:"multi_valued"`
	Unique           bool                               `json:"unique"`
	Localizable      bool                               `json:"localizable,omitempty"`
	Scopable         bool                               `json:"scopable,omitempty"`
	Constraints      Constraints                        `json:"constraints"`
	DefaultValue     *valueobjects.Default              `json:"default_value,omitempty"`
	Group            string                             `json:"group,omitempty"`
	SortOrder        int                                `json:"sort_order"`
	HelpText         string                             `json:"help_text,omitempty"`
	Version          int                                `json:"version"`
	CreatedAt        time.Time                          `json:"created_at"`
	UpdatedAt        time.Time                          `json:"updated_at"`
	ArchivedAt       *time.Time                         `json:"archived_at,omitempty"`
}

// Snapshot projects the aggregate's current state.
func (a *Definition) Snapshot() Snapshot {
	return Snapshot{
		ID:               a.id,
		TenantID:         a.tenantID,
		TypeDefinitionID: a.typeDefinitionID,
		InternalName:     a.internalName,
		DisplayName:      a.displayName,
		Description:      a.description,
		DataType:         a.dataType,
		Required:         a.required,
		MultiValued:      a.multiValued,
		Unique:           a.unique,
		Localizable:      a.localizable,
		Scopable:         a.scopable,
		Constraints:      a.constraints,
		DefaultValue:     a.defaultValue,
		Group:            a.group,
		SortOrder:        a.sortOrder,
		HelpText:         a.helpText,
		Version:          a.version,
		CreatedAt:        a.createdAt,
		UpdatedAt:        a.updatedAt,
		ArchivedAt:       a.archivedAt,
	}
}

// Rehydrate rebuilds the aggregate from a persisted snapshot. Repository
// use only.
func Rehydrate(s Snapshot) *Definition {
	return &Definition{
		id:               s.ID,
		tenantID:         s.TenantID,
		typeDefinitionID: s.TypeDefinitionID,
		internalName:     s.InternalName,
		displayName:      s.DisplayName,
		description:      s.Description,
		dataType:         s.DataType,
		required:         s.Required,
		multiValued:      s.MultiValued,
		unique:           s.Unique,
		localizable:      s.Localizable,
		scopable:         s.Scopable,
		constraints:      s.Constraints,
		defaultValue:     s.DefaultValue,
		group:            s.Group,
		sortOrder:        s.SortOrder,
		helpText:         s.HelpText,
		version:          s.Version,
		createdAt:        s.CreatedAt,
		updatedAt:        s.UpdatedAt,
		archivedAt:       s.ArchivedAt,
	}
}
