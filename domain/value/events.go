package value

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// AggregateType is the event aggregate identifier for attribute values.
const AggregateType = "attribute_value"

// The event types this package emits, for use with
// events.WithEventTypes and subscriber routing.
const (
	// EventSet identifies value set events.
	EventSet events.Type = "flexitype.attribute_value.set"
	// EventUpdated identifies value updated events.
	EventUpdated events.Type = "flexitype.attribute_value.updated"
	// EventRemoved identifies value removed events.
	EventRemoved events.Type = "flexitype.attribute_value.removed"
)

// Set is emitted when a value is first written for an entity attribute.
type Set struct {
	AttributeValueID      valueobjects.AttributeValueID      `json:"attribute_value_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	EntityID              valueobjects.EntityID              `json:"entity_id"`
	Value                 valueobjects.Value                 `json:"value"`
	DefinitionVersion     int                                `json:"definition_version"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Set) EventType() events.Type { return EventSet }

// AggregateType names the emitting aggregate.
func (e Set) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Set) AggregateID() string { return e.AttributeValueID.String() }

// OccurredWhen returns when the domain change happened.
func (e Set) OccurredWhen() time.Time { return e.OccurredAt }

// Updated is emitted when an existing value changes; both old and new
// values ride along so subscribers never need a lookup.
type Updated struct {
	AttributeValueID      valueobjects.AttributeValueID      `json:"attribute_value_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	EntityID              valueobjects.EntityID              `json:"entity_id"`
	OldValue              valueobjects.Value                 `json:"old_value"`
	NewValue              valueobjects.Value                 `json:"new_value"`
	DefinitionVersion     int                                `json:"definition_version"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Updated) EventType() events.Type { return EventUpdated }

// AggregateType names the emitting aggregate.
func (e Updated) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Updated) AggregateID() string { return e.AttributeValueID.String() }

// OccurredWhen returns when the domain change happened.
func (e Updated) OccurredWhen() time.Time { return e.OccurredAt }

// Removed is emitted when a value is removed from an entity.
type Removed struct {
	AttributeValueID      valueobjects.AttributeValueID      `json:"attribute_value_id"`
	TenantID              valueobjects.TenantID              `json:"tenant_id"`
	TypeDefinitionID      valueobjects.TypeDefinitionID      `json:"type_definition_id"`
	AttributeDefinitionID valueobjects.AttributeDefinitionID `json:"attribute_definition_id"`
	EntityID              valueobjects.EntityID              `json:"entity_id"`
	Value                 valueobjects.Value                 `json:"value"`
	OccurredAt            time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Removed) EventType() events.Type { return EventRemoved }

// AggregateType names the emitting aggregate.
func (e Removed) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Removed) AggregateID() string { return e.AttributeValueID.String() }

// OccurredWhen returns when the domain change happened.
func (e Removed) OccurredWhen() time.Time { return e.OccurredAt }
