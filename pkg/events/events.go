// Package events defines flexitype's domain-event contract, the stable wire
// envelope subscribers receive, and a dispatcher with pluggable hooks so
// consumers can route events into their own infrastructure — a pub/sub
// broker, webhooks, or plain functions — without flexitype knowing about it.
package events

import (
	"time"
)

// Event is the contract every domain event satisfies. Domain aggregate
// methods return []Event; usecases dispatch them after commit.
type Event interface {
	// EventType is the stable, versionless event name, e.g.
	// "flexitype.attribute_definition.created".
	EventType() string

	// AggregateType names the aggregate that emitted the event, e.g.
	// "attribute_definition".
	AggregateType() string

	// AggregateID is the string form of the emitting aggregate's ID.
	AggregateID() string

	// OccurredWhen is when the domain change happened.
	OccurredWhen() time.Time
}
