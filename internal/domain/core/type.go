package core

import (
	"time"
)

// TypeDefinition represents a dynamic type definition
type TypeDefinition struct {
	ID          string
	Name        string
	Description string
	Version     int
	Attributes  []*AttributeDefinition
	ParentType  *TypeDefinition
	CreatedAt   time.Time  // When the type was created
	UpdatedAt   time.Time  // When the type was last updated
	ArchivedAt  *time.Time // Nullable timestamp when the type was archived

}

// NewTypeDefinition creates a new type definition
func NewTypeDefinition(id, name, description string) *TypeDefinition {
	now := time.Now()
	return &TypeDefinition{
		ID:          id,
		Name:        name,
		Description: description,
		Version:     1,
		Attributes:  make([]*AttributeDefinition, 0),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// AddAttribute adds an attribute to the type definition
func (t *TypeDefinition) AddAttribute(attr *AttributeDefinition) {
	// Check if attribute already exists
	for i, existing := range t.Attributes {
		if existing.Name == attr.Name {
			// Replace existing attribute
			t.Attributes[i] = attr
			t.UpdatedAt = time.Now()
			return
		}
	}

	// Add new attribute
	t.Attributes = append(t.Attributes, attr)
	t.UpdatedAt = time.Now()
}

// IncrementVersion increments the version of this type definition
func (t *TypeDefinition) IncrementVersion() {
	t.Version++
	t.UpdatedAt = time.Now()
}

// SetParentType sets the parent type, which this type will inherit attributes from
func (t *TypeDefinition) SetParentType(parent *TypeDefinition) {
	t.ParentType = parent
	t.UpdatedAt = time.Now()
}

// GetAllAttributes returns all attributes including inherited ones from parent types
func (t *TypeDefinition) GetAllAttributes() []*AttributeDefinition {
	result := make([]*AttributeDefinition, 0)

	// Process attributes from parent types first (for inheritance)
	if t.ParentType != nil {
		parentAttrs := t.ParentType.GetAllAttributes()

		// Filter parent attributes based on cascade behavior
		for _, parentAttr := range parentAttrs {
			// Check if attribute has any enabled cascades and is not disabled
			if parentAttr.HasEnabledCascades() && !parentAttr.Disabled {
				// Make a copy of the parent attribute to avoid modifying the original
				attrCopy := *parentAttr
				result = append(result, &attrCopy)
			}
		}
	}

	// Create a map for quick lookup and to handle overrides
	attributeMap := make(map[string]*AttributeDefinition)
	for _, attr := range result {
		attributeMap[attr.Name] = attr
	}

	// Process own attributes
	for _, attr := range t.Attributes {
		// Check if this is overriding a cascadeed attribute from parent
		if existingAttr, exists := attributeMap[attr.Name]; exists {
			// Find the highest priority behavior by checking all cascades
			highestBehavior := CascadeInherit // Default behavior
			enabledCascades := existingAttr.GetCascades()

			if len(enabledCascades) > 0 {
				// Use the behavior from the highest weighted cascade
				highestBehavior = enabledCascades[0].Behavior
			}

			// Apply the behavior based on the highest priority cascade
			switch highestBehavior {
			case CascadeInherit:
				// Keep the inherited attribute but allow overriding specific fields
				// This allows inheritance of cascade properties while customizing the attribute
				attributeMap[attr.Name] = attr

			case CascadeOverride:
				// Completely override with child's definition
				attributeMap[attr.Name] = attr

			case CascadeDisabled:
				// Remove the attribute if child wants to disable it
				delete(attributeMap, attr.Name)
			}
		} else {
			// Not overriding, just add normally
			attributeMap[attr.Name] = attr
		}
	}

	// Convert map back to slice
	result = make([]*AttributeDefinition, 0, len(attributeMap))
	for _, attr := range attributeMap {
		result = append(result, attr)
	}

	return result
}
