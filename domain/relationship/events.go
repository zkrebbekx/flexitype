package relationship

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Aggregate identifiers for events and the activity log.
const (
	DefinitionAggregateType = "relationship_definition"
	AggregateType           = "relationship"
)

// The event types this package emits, for use with events.WithEventTypes
// and subscriber routing.
const (
	// EventDefinitionCreated identifies relationship-definition created events.
	EventDefinitionCreated events.Type = "flexitype.relationship_definition.created"
	// EventDefinitionUpdated identifies relationship-definition updated events.
	EventDefinitionUpdated events.Type = "flexitype.relationship_definition.updated"
	// EventDefinitionArchived identifies relationship-definition archived events.
	EventDefinitionArchived events.Type = "flexitype.relationship_definition.archived"
	// EventDefinitionRestored identifies relationship-definition restored events.
	EventDefinitionRestored events.Type = "flexitype.relationship_definition.restored"
	// EventLinked identifies relationship linked events.
	EventLinked events.Type = "flexitype.relationship.linked"
	// EventRePinned identifies relationship re-pinned events.
	EventRePinned events.Type = "flexitype.relationship.repinned"
	// EventUnlinked identifies relationship unlinked events.
	EventUnlinked events.Type = "flexitype.relationship.unlinked"
)

// DefinitionCreated is emitted when a relationship definition is created.
type DefinitionCreated struct {
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	InternalName             string                                `json:"internal_name"`
	ParentTypeID             valueobjects.TypeDefinitionID         `json:"parent_type_id"`
	ChildTypeID              valueobjects.TypeDefinitionID         `json:"child_type_id"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e DefinitionCreated) EventType() events.Type { return EventDefinitionCreated }

// AggregateType names the emitting aggregate.
func (e DefinitionCreated) AggregateType() string { return DefinitionAggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e DefinitionCreated) AggregateID() string { return e.RelationshipDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e DefinitionCreated) OccurredWhen() time.Time { return e.OccurredAt }

// DefinitionUpdated is emitted when a relationship definition changes.
type DefinitionUpdated struct {
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	Version                  int                                   `json:"version"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e DefinitionUpdated) EventType() events.Type { return EventDefinitionUpdated }

// AggregateType names the emitting aggregate.
func (e DefinitionUpdated) AggregateType() string { return DefinitionAggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e DefinitionUpdated) AggregateID() string { return e.RelationshipDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e DefinitionUpdated) OccurredWhen() time.Time { return e.OccurredAt }

// DefinitionArchived is emitted when a relationship definition is archived.
type DefinitionArchived struct {
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e DefinitionArchived) EventType() events.Type { return EventDefinitionArchived }

// AggregateType names the emitting aggregate.
func (e DefinitionArchived) AggregateType() string { return DefinitionAggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e DefinitionArchived) AggregateID() string { return e.RelationshipDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e DefinitionArchived) OccurredWhen() time.Time { return e.OccurredAt }

// DefinitionRestored is emitted when an archived relationship definition is
// restored.
type DefinitionRestored struct {
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e DefinitionRestored) EventType() events.Type { return EventDefinitionRestored }

// AggregateType names the emitting aggregate.
func (e DefinitionRestored) AggregateType() string { return DefinitionAggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e DefinitionRestored) AggregateID() string { return e.RelationshipDefinitionID.String() }

// OccurredWhen returns when the domain change happened.
func (e DefinitionRestored) OccurredWhen() time.Time { return e.OccurredAt }

// Linked is emitted when two entities are linked.
type Linked struct {
	RelationshipID           valueobjects.RelationshipID           `json:"relationship_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	ParentEntityID           valueobjects.EntityID                 `json:"parent_entity_id"`
	ChildEntityID            valueobjects.EntityID                 `json:"child_entity_id"`
	ParentVersion            *int                                  `json:"parent_type_version,omitempty"`
	ChildVersion             *int                                  `json:"child_type_version,omitempty"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Linked) EventType() events.Type { return EventLinked }

// AggregateType names the emitting aggregate.
func (e Linked) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Linked) AggregateID() string { return e.RelationshipID.String() }

// OccurredWhen returns when the domain change happened.
func (e Linked) OccurredWhen() time.Time { return e.OccurredAt }

// RePinned is emitted when a link's version pins change.
type RePinned struct {
	RelationshipID           valueobjects.RelationshipID           `json:"relationship_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	ParentVersion            *int                                  `json:"parent_type_version,omitempty"`
	ChildVersion             *int                                  `json:"child_type_version,omitempty"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e RePinned) EventType() events.Type { return EventRePinned }

// AggregateType names the emitting aggregate.
func (e RePinned) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e RePinned) AggregateID() string { return e.RelationshipID.String() }

// OccurredWhen returns when the domain change happened.
func (e RePinned) OccurredWhen() time.Time { return e.OccurredAt }

// Unlinked is emitted when a link is removed.
type Unlinked struct {
	RelationshipID           valueobjects.RelationshipID           `json:"relationship_id"`
	TenantID                 valueobjects.TenantID                 `json:"tenant_id"`
	RelationshipDefinitionID valueobjects.RelationshipDefinitionID `json:"relationship_definition_id"`
	ParentEntityID           valueobjects.EntityID                 `json:"parent_entity_id"`
	ChildEntityID            valueobjects.EntityID                 `json:"child_entity_id"`
	OccurredAt               time.Time                             `json:"occurred_at"`
}

// EventType identifies the event on the wire.
func (e Unlinked) EventType() events.Type { return EventUnlinked }

// AggregateType names the emitting aggregate.
func (e Unlinked) AggregateType() string { return AggregateType }

// AggregateID returns the emitting aggregate identifier.
func (e Unlinked) AggregateID() string { return e.RelationshipID.String() }

// OccurredWhen returns when the domain change happened.
func (e Unlinked) OccurredWhen() time.Time { return e.OccurredAt }
