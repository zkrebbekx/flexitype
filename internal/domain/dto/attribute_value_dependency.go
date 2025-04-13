package dto

import (
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// AttributeValueDependencyCreate represents the data needed to create a new attribute value dependency
type AttributeValueDependencyCreate struct {
	SourceAttributeDefinitionID ulid.ULID `json:"source_attribute_definition_id"`
	TargetAttributeDefinitionID ulid.ULID `json:"target_attribute_definition_id"`
	SourceConditions           []model.Condition `json:"source_conditions"`
	TargetValues              []model.DynamicValue `json:"target_values"`
	ValidationRules           []model.ValidationRule `json:"validation_rules"`
}

// AttributeValueDependencyUpdate represents the data needed to update an existing attribute value dependency
type AttributeValueDependencyUpdate struct {
	ID                        ulid.ULID `json:"id"`
	SourceAttributeDefinitionID ulid.ULID `json:"source_attribute_definition_id"`
	TargetAttributeDefinitionID ulid.ULID `json:"target_attribute_definition_id"`
	SourceConditions           []model.Condition `json:"source_conditions"`
	TargetValues              []model.DynamicValue `json:"target_values"`
	ValidationRules           []model.ValidationRule `json:"validation_rules"`
	Version                   uint32    `json:"version"`
}

// AttributeValueDependencyResponse represents the data returned for an attribute value dependency
type AttributeValueDependencyResponse struct {
	ID                        ulid.ULID `json:"id"`
	SourceAttributeDefinitionID ulid.ULID `json:"source_attribute_definition_id"`
	TargetAttributeDefinitionID ulid.ULID `json:"target_attribute_definition_id"`
	SourceConditions           []model.Condition `json:"source_conditions"`
	TargetValues              []model.DynamicValue `json:"target_values"`
	ValidationRules           []model.ValidationRule `json:"validation_rules"`
	Version                   uint32    `json:"version"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
	ArchivedAt               *time.Time `json:"archived_at,omitempty"`
}

// ToModel converts a DTO to a domain model
func (d *AttributeValueDependencyCreate) ToModel() *model.AttributeValueDependency {
	return &model.AttributeValueDependency{
		SourceAttributeDefinitionID: d.SourceAttributeDefinitionID,
		TargetAttributeDefinitionID: d.TargetAttributeDefinitionID,
		SourceConditions:           d.SourceConditions,
		TargetValues:              d.TargetValues,
		ValidationRules:           d.ValidationRules,
	}
}

// ToModel converts a DTO to a domain model
func (d *AttributeValueDependencyUpdate) ToModel() *model.AttributeValueDependency {
	return &model.AttributeValueDependency{
		ID:                        d.ID,
		SourceAttributeDefinitionID: d.SourceAttributeDefinitionID,
		TargetAttributeDefinitionID: d.TargetAttributeDefinitionID,
		SourceConditions:           d.SourceConditions,
		TargetValues:              d.TargetValues,
		ValidationRules:           d.ValidationRules,
		Version:                   d.Version,
	}
}

// FromModel converts a domain model to a DTO
func FromModel(m *model.AttributeValueDependency) *AttributeValueDependencyResponse {
	return &AttributeValueDependencyResponse{
		ID:                        m.ID,
		SourceAttributeDefinitionID: m.SourceAttributeDefinitionID,
		TargetAttributeDefinitionID: m.TargetAttributeDefinitionID,
		SourceConditions:           m.SourceConditions,
		TargetValues:              m.TargetValues,
		ValidationRules:           m.ValidationRules,
		Version:                   m.Version,
		CreatedAt:                 m.CreatedAt,
		UpdatedAt:                 m.UpdatedAt,
		ArchivedAt:               m.ArchivedAt,
	}
} 