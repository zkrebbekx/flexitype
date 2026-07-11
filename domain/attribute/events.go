package attribute

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// AggregateType is the event aggregate identifier for attribute definitions.
const AggregateType = "attribute_definition"

// Created is emitted when an attribute definition is created.
type Created struct {
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	InternalName          string                             `json:"internal_name"`
	DisplayName           string                             `json:"display_name"`
	DataType              valueobjects.DataType              `json:"data_type"`
	Required              bool                               `json:"required"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Created) EventType() string { return "flexitype.attribute_definition.created" }

// AggregateType names the emitting aggregate.
func (e Created) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Created) AggregateID() string { return e.AttributeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Created) OccurredWhen() time.Time { return e.OccurredAt }

// Updated is emitted when an attribute definition changes; Version is the
// new definition version.
type Updated struct {
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	DisplayName           string                             `json:"display_name"`
	Required              bool                               `json:"required"`
	Version               int                                `json:"version"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Updated) EventType() string { return "flexitype.attribute_definition.updated" }

// AggregateType names the emitting aggregate.
func (e Updated) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Updated) AggregateID() string { return e.AttributeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Updated) OccurredWhen() time.Time { return e.OccurredAt }

// Archived is emitted when an attribute definition is archived.
type Archived struct {
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Archived) EventType() string { return "flexitype.attribute_definition.archived" }

// AggregateType names the emitting aggregate.
func (e Archived) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Archived) AggregateID() string { return e.AttributeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Archived) OccurredWhen() time.Time { return e.OccurredAt }

// Restored is emitted when an archived attribute definition is restored.
type Restored struct {
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Restored) EventType() string { return "flexitype.attribute_definition.restored" }

// AggregateType names the emitting aggregate.
func (e Restored) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Restored) AggregateID() string { return e.AttributeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Restored) OccurredWhen() time.Time { return e.OccurredAt }
