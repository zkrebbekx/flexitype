package dto

import (
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// AttributeCreate represents the data needed to create a new attribute
type AttributeCreate struct {
	TypeDefinitionID ulid.ULID `json:"type_definition_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DataType        string    `json:"data_type"`
	IsRequired      bool      `json:"is_required"`
	DefaultValue    interface{} `json:"default_value,omitempty"`
	ValidationRules []model.ValidationRule `json:"validation_rules,omitempty"`
}

// AttributeUpdate represents the data needed to update an existing attribute
type AttributeUpdate struct {
	ID              ulid.ULID `json:"id"`
	TypeDefinitionID ulid.ULID `json:"type_definition_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DataType        string    `json:"data_type"`
	IsRequired      bool      `json:"is_required"`
	DefaultValue    interface{} `json:"default_value,omitempty"`
	ValidationRules []model.ValidationRule `json:"validation_rules,omitempty"`
	Version         uint32    `json:"version"`
}

// AttributeResponse represents the data returned for an attribute
type AttributeResponse struct {
	ID              ulid.ULID `json:"id"`
	TypeDefinitionID ulid.ULID `json:"type_definition_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DataType        string    `json:"data_type"`
	IsRequired      bool      `json:"is_required"`
	DefaultValue    interface{} `json:"default_value,omitempty"`
	ValidationRules []model.ValidationRule `json:"validation_rules,omitempty"`
	Version         uint32    `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
}

// AttributeValueCreate represents the data needed to create a new attribute value
type AttributeValueCreate struct {
	AttributeDefinitionID ulid.ULID `json:"attribute_definition_id"`
	Value               interface{} `json:"value"`
}

// AttributeValueUpdate represents the data needed to update an existing attribute value
type AttributeValueUpdate struct {
	ID                  ulid.ULID `json:"id"`
	AttributeDefinitionID ulid.ULID `json:"attribute_definition_id"`
	Value               interface{} `json:"value"`
	Version             uint32    `json:"version"`
}

// AttributeValueResponse represents the data returned for an attribute value
type AttributeValueResponse struct {
	ID                  ulid.ULID `json:"id"`
	AttributeDefinitionID ulid.ULID `json:"attribute_definition_id"`
	Value               interface{} `json:"value"`
	Version             uint32    `json:"version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty"`
}

// ToModel converts an AttributeCreate DTO to a domain model
func (d *AttributeCreate) ToModel() *model.AttributeDefinition {
	return &model.AttributeDefinition{
		TypeDefinitionID: d.TypeDefinitionID,
		Name:            d.Name,
		Description:     d.Description,
		DataType:        d.DataType,
		IsRequired:      d.IsRequired,
		DefaultValue:    d.DefaultValue,
		ValidationRules: d.ValidationRules,
	}
}

// ToModel converts an AttributeUpdate DTO to a domain model
func (d *AttributeUpdate) ToModel() *model.AttributeDefinition {
	return &model.AttributeDefinition{
		ID:              d.ID,
		TypeDefinitionID: d.TypeDefinitionID,
		Name:            d.Name,
		Description:     d.Description,
		DataType:        d.DataType,
		IsRequired:      d.IsRequired,
		DefaultValue:    d.DefaultValue,
		ValidationRules: d.ValidationRules,
		Version:         d.Version,
	}
}

// FromModel converts an AttributeDefinition domain model to a DTO
func FromAttributeModel(m *model.AttributeDefinition) *AttributeResponse {
	return &AttributeResponse{
		ID:              m.ID,
		TypeDefinitionID: m.TypeDefinitionID,
		Name:            m.Name,
		Description:     m.Description,
		DataType:        m.DataType,
		IsRequired:      m.IsRequired,
		DefaultValue:    m.DefaultValue,
		ValidationRules: m.ValidationRules,
		Version:         m.Version,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		ArchivedAt:     m.ArchivedAt,
	}
}

// ToModel converts an AttributeValueCreate DTO to a domain model
func (d *AttributeValueCreate) ToModel() *model.AttributeValue {
	return &model.AttributeValue{
		AttributeDefinitionID: d.AttributeDefinitionID,
		Value:               d.Value,
	}
}

// ToModel converts an AttributeValueUpdate DTO to a domain model
func (d *AttributeValueUpdate) ToModel() *model.AttributeValue {
	return &model.AttributeValue{
		ID:                  d.ID,
		AttributeDefinitionID: d.AttributeDefinitionID,
		Value:               d.Value,
		Version:             d.Version,
	}
}

// FromModel converts an AttributeValue domain model to a DTO
func FromAttributeValueModel(m *model.AttributeValue) *AttributeValueResponse {
	return &AttributeValueResponse{
		ID:                  m.ID,
		AttributeDefinitionID: m.AttributeDefinitionID,
		Value:               m.Value,
		Version:             m.Version,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
		ArchivedAt:         m.ArchivedAt,
	}
} 