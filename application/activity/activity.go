// Package activity defines the change-audit vocabulary: usecases record
// Changes with before/after snapshots; the unit of work's pre-commit
// handler serializes them into activity-log entries written in the same
// transaction as the change itself.
package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Action classifies what happened to an entity.
type Action string

// Audit actions recorded against changed entities.
const (
	ActionCreated  Action = "created"
	ActionUpdated  Action = "updated"
	ActionArchived Action = "archived"
	ActionRestored Action = "restored"
	ActionRemoved  Action = "removed"
	// ActionPurged records a HARD delete (right-to-erasure): the entity or
	// tenant data was permanently and irreversibly removed, not archived.
	ActionPurged Action = "purged"
)

// Change is what a usecase records: the entity that changed plus
// JSON-marshalable before/after snapshots (nil before for creates, nil
// after for removals).
type Change struct {
	Entity   string
	EntityID string
	Action   Action
	Before   any
	After    any
}

// Entry is one persisted activity-log row with JSON descriptors of the
// pre/post state.
type Entry struct {
	ID         ulid.ID               `db:"id" json:"id"`
	TenantID   valueobjects.TenantID `db:"tenant_id" json:"tenant_id"`
	Actor      string                `db:"actor" json:"actor"`
	Entity     string                `db:"entity" json:"entity"`
	EntityID   string                `db:"entity_id" json:"entity_id"`
	Action     Action                `db:"action" json:"action"`
	Before     json.RawMessage       `db:"before_state" json:"before,omitempty"`
	After      json.RawMessage       `db:"after_state" json:"after,omitempty"`
	OccurredAt time.Time             `db:"occurred_at" json:"occurred_at"`
}

// NewEntry serializes a Change into a persistable Entry.
func NewEntry(c Change, tenant valueobjects.TenantID, actor string, now time.Time) (Entry, error) {
	e := Entry{
		ID:         ulid.New(),
		TenantID:   tenant,
		Actor:      actor,
		Entity:     c.Entity,
		EntityID:   c.EntityID,
		Action:     c.Action,
		OccurredAt: now,
	}
	var err error
	if c.Before != nil {
		if e.Before, err = json.Marshal(c.Before); err != nil {
			return Entry{}, fmt.Errorf("marshal before state: %w", err)
		}
	}
	if c.After != nil {
		if e.After, err = json.Marshal(c.After); err != nil {
			return Entry{}, fmt.Errorf("marshal after state: %w", err)
		}
	}
	return e, nil
}

// Filter narrows activity-log queries.
type Filter struct {
	TenantID valueobjects.TenantID
	Entity   string
	EntityID string
	Actor    string
}

// Log is the persistence port for the activity log. Write runs inside the
// business transaction (pre-commit); List serves the audit API.
type Log interface {
	Write(ctx context.Context, tx db.Tx, entries []Entry) error
	List(ctx context.Context, filter Filter, page db.Page) ([]Entry, int, error)
}
