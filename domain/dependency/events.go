package dependency

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// AggregateType is the event aggregate identifier for dependencies.
const AggregateType = "attribute_value_dependency"

// The event types this package emits, for use with
// events.WithEventTypes and subscriber routing.
const (
	// EventCreated identifies dependency created events.
	EventCreated events.Type = "flexitype.attribute_value_dependency.created"
	// EventUpdated identifies dependency updated events.
	EventUpdated events.Type = "flexitype.attribute_value_dependency.updated"
	// EventArchived identifies dependency archived events.
	EventArchived events.Type = "flexitype.attribute_value_dependency.archived"
)

// Created is emitted when a dependency is created.
type Created struct {
	DependencyID      valueobjects.DependencyID          `json:"dependency_id"`
	TenantID          valueobjects.TenantID              `json:"tenant_id"`
	SourceAttributeID valueobjects.AttributeDefinitionID `json:"source_attribute_id"`
	TargetAttributeID valueobjects.AttributeDefinitionID `json:"target_attribute_id"`
	OccurredAt        time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Created) EventType() events.Type { return EventCreated }

// AggregateType names the emitting aggregate.
func (e Created) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Created) AggregateID() string { return e.DependencyID.String() }

// OccurredWhen returns when the domain change happened.
func (e Created) OccurredWhen() time.Time { return e.OccurredAt }

// Updated is emitted when a dependency's rule changes.
type Updated struct {
	DependencyID      valueobjects.DependencyID          `json:"dependency_id"`
	TenantID          valueobjects.TenantID              `json:"tenant_id"`
	SourceAttributeID valueobjects.AttributeDefinitionID `json:"source_attribute_id"`
	TargetAttributeID valueobjects.AttributeDefinitionID `json:"target_attribute_id"`
	Version           int                                `json:"version"`
	OccurredAt        time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Updated) EventType() events.Type { return EventUpdated }

// AggregateType names the emitting aggregate.
func (e Updated) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Updated) AggregateID() string { return e.DependencyID.String() }

// OccurredWhen returns when the domain change happened.
func (e Updated) OccurredWhen() time.Time { return e.OccurredAt }

// Archived is emitted when a dependency is archived.
type Archived struct {
	DependencyID      valueobjects.DependencyID          `json:"dependency_id"`
	TenantID          valueobjects.TenantID              `json:"tenant_id"`
	SourceAttributeID valueobjects.AttributeDefinitionID `json:"source_attribute_id"`
	TargetAttributeID valueobjects.AttributeDefinitionID `json:"target_attribute_id"`
	OccurredAt        time.Time                          `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Archived) EventType() events.Type { return EventArchived }

// AggregateType names the emitting aggregate.
func (e Archived) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Archived) AggregateID() string { return e.DependencyID.String() }

// OccurredWhen returns when the domain change happened.
func (e Archived) OccurredWhen() time.Time { return e.OccurredAt }
