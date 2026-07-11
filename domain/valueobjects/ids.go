// Package valueobjects holds the strongly-typed identifiers and value types
// shared across flexitype's domain. Every internal identifier is a distinct
// ULID newtype so aggregates can never be cross-wired at compile time.
package valueobjects

import (
	"fmt"
	"regexp"

	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TypeDefinitionID identifies a type definition.
type TypeDefinitionID struct {
	ulid.ID
}

// NewTypeDefinitionID mints a new TypeDefinitionID.
func NewTypeDefinitionID() TypeDefinitionID {
	return TypeDefinitionID{ulid.New()}
}

// ParseTypeDefinitionID parses a string into a TypeDefinitionID.
func ParseTypeDefinitionID(s string) (TypeDefinitionID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return TypeDefinitionID{}, fmt.Errorf("invalid type definition ID: %w", err)
	}
	return TypeDefinitionID{id}, nil
}

// MustParseTypeDefinitionID parses s, panicking on error.
func MustParseTypeDefinitionID(s string) TypeDefinitionID {
	id, err := ParseTypeDefinitionID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// Equals checks if two TypeDefinitionIDs are equal.
func (id TypeDefinitionID) Equals(other TypeDefinitionID) bool {
	return id.String() == other.String()
}

// AttributeDefinitionID identifies an attribute definition.
type AttributeDefinitionID struct {
	ulid.ID
}

// NewAttributeDefinitionID mints a new AttributeDefinitionID.
func NewAttributeDefinitionID() AttributeDefinitionID {
	return AttributeDefinitionID{ulid.New()}
}

// ParseAttributeDefinitionID parses a string into an AttributeDefinitionID.
func ParseAttributeDefinitionID(s string) (AttributeDefinitionID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return AttributeDefinitionID{}, fmt.Errorf("invalid attribute definition ID: %w", err)
	}
	return AttributeDefinitionID{id}, nil
}

// MustParseAttributeDefinitionID parses s, panicking on error.
func MustParseAttributeDefinitionID(s string) AttributeDefinitionID {
	id, err := ParseAttributeDefinitionID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// Equals checks if two AttributeDefinitionIDs are equal.
func (id AttributeDefinitionID) Equals(other AttributeDefinitionID) bool {
	return id.String() == other.String()
}

// AttributeValueID identifies a stored attribute value.
type AttributeValueID struct {
	ulid.ID
}

// NewAttributeValueID mints a new AttributeValueID.
func NewAttributeValueID() AttributeValueID {
	return AttributeValueID{ulid.New()}
}

// ParseAttributeValueID parses a string into an AttributeValueID.
func ParseAttributeValueID(s string) (AttributeValueID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return AttributeValueID{}, fmt.Errorf("invalid attribute value ID: %w", err)
	}
	return AttributeValueID{id}, nil
}

// MustParseAttributeValueID parses s, panicking on error.
func MustParseAttributeValueID(s string) AttributeValueID {
	id, err := ParseAttributeValueID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// Equals checks if two AttributeValueIDs are equal.
func (id AttributeValueID) Equals(other AttributeValueID) bool {
	return id.String() == other.String()
}

// DependencyID identifies an attribute value dependency.
type DependencyID struct {
	ulid.ID
}

// NewDependencyID mints a new DependencyID.
func NewDependencyID() DependencyID {
	return DependencyID{ulid.New()}
}

// ParseDependencyID parses a string into a DependencyID.
func ParseDependencyID(s string) (DependencyID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return DependencyID{}, fmt.Errorf("invalid dependency ID: %w", err)
	}
	return DependencyID{id}, nil
}

// MustParseDependencyID parses s, panicking on error.
func MustParseDependencyID(s string) DependencyID {
	id, err := ParseDependencyID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// Equals checks if two DependencyIDs are equal.
func (id DependencyID) Equals(other DependencyID) bool {
	return id.String() == other.String()
}

// DefaultTenant is the tenant used when a caller does not segment data.
const DefaultTenant = TenantID("default")

var tenantPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// TenantID segments all data for multi-tenant deployments. It is a consumer
// controlled identifier, not a ULID, so hosted tiers can map it onto their
// own account model.
type TenantID string

// ParseTenantID validates a tenant identifier. Empty resolves to
// DefaultTenant.
func ParseTenantID(s string) (TenantID, error) {
	if s == "" {
		return DefaultTenant, nil
	}
	if !tenantPattern.MatchString(s) {
		return "", fmt.Errorf("invalid tenant ID %q: must match %s", s, tenantPattern)
	}
	return TenantID(s), nil
}

// String returns the raw tenant identifier.
func (t TenantID) String() string { return string(t) }

// IsZero reports whether the tenant is unset.
func (t TenantID) IsZero() bool { return t == "" }

// EntityID anchors attribute values to the consumer's own domain object
// (a product, a part, a user, ...). It is deliberately an opaque string —
// consumers keep their identifier scheme; ULIDs are recommended.
type EntityID string

const maxEntityIDLength = 255

// ParseEntityID validates a consumer entity identifier.
func ParseEntityID(s string) (EntityID, error) {
	if s == "" {
		return "", fmt.Errorf("entity ID must not be empty")
	}
	if len(s) > maxEntityIDLength {
		return "", fmt.Errorf("entity ID exceeds %d characters", maxEntityIDLength)
	}
	return EntityID(s), nil
}

// String returns the raw entity identifier.
func (e EntityID) String() string { return string(e) }

// IsZero reports whether the entity ID is unset.
func (e EntityID) IsZero() bool { return e == "" }
