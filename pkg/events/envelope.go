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
	ID            string          `json:"id"`
	Type          Type            `json:"type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	TenantID      string          `json:"tenant_id,omitempty"`
	Actor         string          `json:"actor,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at"`
	RecordedAt    time.Time       `json:"recorded_at"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// Metadata carries per-dispatch context stamped onto every envelope.
type Metadata struct {
	TenantID string
	Actor    string
}

// NewEnvelope wraps a domain event in the wire envelope.
func NewEnvelope(e Event, meta Metadata, now time.Time) (Envelope, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal event payload %s: %w", e.EventType(), err)
	}
	return Envelope{
		ID:            ulid.New().String(),
		Type:          e.EventType(),
		AggregateType: e.AggregateType(),
		AggregateID:   e.AggregateID(),
		TenantID:      meta.TenantID,
		Actor:         meta.Actor,
		OccurredAt:    e.OccurredWhen(),
		RecordedAt:    now,
		SchemaVersion: SchemaVersion,
		Payload:       payload,
	}, nil
}
