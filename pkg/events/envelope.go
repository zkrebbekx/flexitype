package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// SchemaVersion identifies the envelope wire format. Bump only on breaking
// envelope changes; event payloads are versioned by their EventType.
const SchemaVersion = 1

// Envelope is the stable message format every subscriber receives,
// regardless of transport (pub/sub, webhook, in-process func). Payload is
// the JSON encoding of the concrete domain event.
type Envelope struct {
	ID            string `json:"id"`
	Type          Type   `json:"type"`
	AggregateType string `json:"aggregate_type"`
	AggregateID   string `json:"aggregate_id"`
	TenantID      string `json:"tenant_id,omitempty"`
	// TypeDefinitionID and EntityID address the ENTITY an event concerns,
	// when it concerns exactly one.
	//
	// AggregateID names the aggregate that emitted the event, which for a
	// value event is the attribute VALUE. A consumer that only wants "entity
	// E changed, re-read it" could not answer that from the envelope and had
	// to decode the payload, which couples every router to the payload schema
	// of every event type it routes — the thing an envelope exists to
	// prevent.
	//
	// They are empty for an event that concerns no entity (a schema change)
	// or more than one (a relationship link, which names two). Those keep
	// their payload, because one pair of fields cannot honestly describe two
	// endpoints.
	TypeDefinitionID string          `json:"type_definition_id,omitempty"`
	EntityID         string          `json:"entity_id,omitempty"`
	Actor            string          `json:"actor,omitempty"`
	OccurredAt       time.Time       `json:"occurred_at"`
	RecordedAt       time.Time       `json:"recorded_at"`
	SchemaVersion    int             `json:"schema_version"`
	Payload          json.RawMessage `json:"payload"`
}

// Metadata carries per-dispatch context stamped onto every envelope.
type Metadata struct {
	TenantID string
	Actor    string
}

// EntityAddressed is implemented by an event that concerns exactly ONE
// entity. NewEnvelope copies the coordinates onto the envelope, so a router
// never has to decode a payload to learn what changed.
//
// An event that concerns no entity, or more than one, does not implement it.
type EntityAddressed interface {
	// EntityCoordinates returns the type definition and entity the event
	// concerns. Both must be non-empty.
	EntityCoordinates() (typeDefinitionID, entityID string)
}

// NewEnvelope wraps a domain event in the wire envelope.
func NewEnvelope(e Event, meta Metadata, now time.Time) (Envelope, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal event payload %s: %w", e.EventType(), err)
	}
	typeDefinitionID, entityID := "", ""
	if addressed, ok := e.(EntityAddressed); ok {
		typeDefinitionID, entityID = addressed.EntityCoordinates()
	}
	return Envelope{
		ID:               ulid.New().String(),
		Type:             e.EventType(),
		AggregateType:    e.AggregateType(),
		AggregateID:      e.AggregateID(),
		TenantID:         meta.TenantID,
		TypeDefinitionID: typeDefinitionID,
		EntityID:         entityID,
		Actor:            meta.Actor,
		OccurredAt:       e.OccurredWhen(),
		RecordedAt:       now,
		SchemaVersion:    SchemaVersion,
		Payload:          payload,
	}, nil
}
