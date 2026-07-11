package typedef

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// AggregateType is the event aggregate identifier for type definitions.
const AggregateType = "type_definition"

// The event types this package emits, for use with
// events.WithEventTypes and subscriber routing.
const (
	// EventCreated identifies typedef created events.
	EventCreated events.Type = "flexitype.type_definition.created"
	// EventUpdated identifies typedef updated events.
	EventUpdated events.Type = "flexitype.type_definition.updated"
	// EventArchived identifies typedef archived events.
	EventArchived events.Type = "flexitype.type_definition.archived"
	// EventRestored identifies typedef restored events.
	EventRestored events.Type = "flexitype.type_definition.restored"
)

// Created is emitted when a type definition is created.
type Created struct {
	TypeDefinitionID valueobjects.TypeDefinitionID  `json:"type_definition_id"`
	TenantID         valueobjects.TenantID          `json:"tenant_id"`
	InternalName     string                         `json:"internal_name"`
	DisplayName      string                         `json:"display_name"`
	ExtendsID        *valueobjects.TypeDefinitionID `json:"extends_id,omitempty"`
	OccurredAt       time.Time                      `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Created) EventType() events.Type { return EventCreated }

// AggregateType names the emitting aggregate.
func (e Created) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Created) AggregateID() string { return e.TypeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Created) OccurredWhen() time.Time { return e.OccurredAt }

// Updated is emitted when a type definition's display fields change.
type Updated struct {
	TypeDefinitionID valueobjects.TypeDefinitionID `json:"type_definition_id"`
	TenantID         valueobjects.TenantID         `json:"tenant_id"`
	DisplayName      string                        `json:"display_name"`
	Description      string                        `json:"description,omitempty"`
	Version          int                           `json:"version"`
	OccurredAt       time.Time                     `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Updated) EventType() events.Type { return EventUpdated }

// AggregateType names the emitting aggregate.
func (e Updated) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Updated) AggregateID() string { return e.TypeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Updated) OccurredWhen() time.Time { return e.OccurredAt }

// Archived is emitted when a type definition is archived.
type Archived struct {
	TypeDefinitionID valueobjects.TypeDefinitionID `json:"type_definition_id"`
	TenantID         valueobjects.TenantID         `json:"tenant_id"`
	OccurredAt       time.Time                     `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Archived) EventType() events.Type { return EventArchived }

// AggregateType names the emitting aggregate.
func (e Archived) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Archived) AggregateID() string { return e.TypeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Archived) OccurredWhen() time.Time { return e.OccurredAt }

// Restored is emitted when an archived type definition is restored.
type Restored struct {
	TypeDefinitionID valueobjects.TypeDefinitionID `json:"type_definition_id"`
	TenantID         valueobjects.TenantID         `json:"tenant_id"`
	OccurredAt       time.Time                     `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Restored) EventType() events.Type { return EventRestored }

// AggregateType names the emitting aggregate.
func (e Restored) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Restored) AggregateID() string { return e.TypeDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e Restored) OccurredWhen() time.Time { return e.OccurredAt }
