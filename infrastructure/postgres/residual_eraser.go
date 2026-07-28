package postgres

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/erasure"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// outboxEraser redacts an erased entity's values from the event log.
//
// Value set/updated/removed payloads embed the value, and the feed API serves
// them until retention pruning — 7 days by default, arbitrarily long by
// configuration, and forever for rows never expanded, because pruning
// requires a feed_seq. Webhook deliveries read the same payload through their
// envelope reference, so redacting the outbox covers those too.
//
// The row survives: the feed's sequence is gapless by design, and deleting
// rows would break the one guarantee a consumer relies on. Identifiers stay
// so a consumer can still see that something happened to that entity; the
// value content is replaced.
type outboxEraser struct{}

// NewOutboxEraser builds the event-log residual eraser. It takes no executor:
// every redaction runs inside the erasure transaction it is handed.
func NewOutboxEraser() erasure.ResidualEraser { return &outboxEraser{} }

func (e *outboxEraser) Name() string { return "event log" }

// redactedPayload keeps the routing identifiers and drops everything else.
const redactedPayload = `jsonb_strip_nulls(jsonb_build_object(
    'tenant_id', payload->'tenant_id',
    'entity_id', payload->'entity_id',
    'type_definition_id', payload->'type_definition_id',
    'attribute_definition_id', payload->'attribute_definition_id',
    'erased', to_jsonb(true)))`

func (e *outboxEraser) RedactEntity(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID, entityID string) (int, error) {
	return e.exec(ctx, tx,
		`UPDATE flexitype_event_outbox
		    SET payload = `+redactedPayload+`
		  WHERE tenant_id = ? AND payload->>'entity_id' = ?
		    AND COALESCE(payload->>'erased', 'false') <> 'true'`,
		tenant.String(), entityID)
}

func (e *outboxEraser) RedactTenant(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID) (int, error) {
	return e.exec(ctx, tx,
		`UPDATE flexitype_event_outbox
		    SET payload = `+redactedPayload+`
		  WHERE tenant_id = ? AND payload->>'entity_id' IS NOT NULL
		    AND COALESCE(payload->>'erased', 'false') <> 'true'`,
		tenant.String())
}

// exec runs a redaction inside the erasure transaction. The `?` placeholders
// are rewritten by bind, so no jsonb `?` existence operator appears in these
// statements — `->> ... IS NOT NULL` says the same thing without colliding.
func (e *outboxEraser) exec(ctx context.Context, tx db.Tx, query string, args ...any) (int, error) {
	q := txExecer(tx)
	res, err := q.ExecContext(ctx, bind(query), args...)
	if err != nil {
		return 0, fmt.Errorf("redact event payloads: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("redact event payloads: %w", err)
	}
	return int(n), nil
}

// activityEraser redacts an erased entity's values from the audit log.
//
// Every prior write persisted the full value in an activity entry's before and
// after state. PurgeTenant deliberately keeps the log so the erasure stays
// provable — which is exactly why the entries have to be redacted rather than
// deleted: the proof survives, the personal data does not.
type activityEraser struct{}

// NewActivityEraser builds the audit-log residual eraser.
func NewActivityEraser() erasure.ResidualEraser { return &activityEraser{} }

func (e *activityEraser) Name() string { return "activity log" }

// RedactEntity matches on the entity named INSIDE the state snapshot, not on
// the row's entity_id: an activity row for a value write keys on the value's
// own id, so the entity appears only in the recorded before/after JSON.
func (e *activityEraser) RedactEntity(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID, entityID string) (int, error) {
	return e.exec(ctx, tx,
		`UPDATE flexitype_activity_log
		    SET before_state = CASE WHEN before_state IS NULL THEN NULL ELSE CAST(? AS JSONB) END,
		        after_state  = CASE WHEN after_state  IS NULL THEN NULL ELSE CAST(? AS JSONB) END
		  WHERE tenant_id = ?
		    AND (before_state->>'entity_id' = ? OR after_state->>'entity_id' = ?)`,
		redactedState(), redactedState(), tenant.String(), entityID, entityID)
}

// RedactTenant redacts every entry that names any entity. Entries that name
// none are schema history — type and attribute definitions — which a tenant
// erasure deliberately keeps, since it erases entity DATA.
func (e *activityEraser) RedactTenant(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID) (int, error) {
	return e.exec(ctx, tx,
		`UPDATE flexitype_activity_log
		    SET before_state = CASE WHEN before_state IS NULL THEN NULL ELSE CAST(? AS JSONB) END,
		        after_state  = CASE WHEN after_state  IS NULL THEN NULL ELSE CAST(? AS JSONB) END
		  WHERE tenant_id = ?
		    AND (before_state->>'entity_id' IS NOT NULL OR after_state->>'entity_id' IS NOT NULL)`,
		redactedState(), redactedState(), tenant.String())
}

func (e *activityEraser) exec(ctx context.Context, tx db.Tx, query string, args ...any) (int, error) {
	q := txExecer(tx)
	res, err := q.ExecContext(ctx, bind(query), args...)
	if err != nil {
		return 0, fmt.Errorf("redact activity entries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("redact activity entries: %w", err)
	}
	return int(n), nil
}

// redactedState is the marker that replaces an entry's value snapshot. It is
// greppable on purpose: a reader has to be able to tell an erased value from
// one that was never set.
func redactedState() any {
	return jsonbParam([]byte(`{"` + erasure.RedactedMarker + `":true}`))
}

// changeSetEraser redacts an erased entity's values from staged change-set
// mutations.
//
// flexitype_changeset.mutations is JSONB embedding the value verbatim, and a
// draft or rejected set is never pruned — so a purged value stayed readable
// there indefinitely, while the report said the erasure had succeeded. That
// is the same class as the event log and the activity log, on a table the
// first pass did not enumerate.
//
// The mutation SKELETON survives: kind, attribute, entity and scope stay, so
// the set still reports what it contains and a reviewer sees that a change
// exists. Only the value is replaced. Deleting the mutation would silently
// change what the set does when published.
type changeSetEraser struct{}

// NewChangeSetEraser builds the change-set residual eraser.
func NewChangeSetEraser() erasure.ResidualEraser { return &changeSetEraser{} }

func (e *changeSetEraser) Name() string { return "change-sets" }

// redactMutations rewrites the mutations array, dropping `value` and marking
// the entry, for every element matching the entity predicate.
const redactMutations = `(
    SELECT COALESCE(jsonb_agg(
             CASE WHEN %s
                  THEN (m - 'value') || jsonb_build_object('erased', to_jsonb(true))
                  ELSE m END
             ORDER BY ord), '[]'::jsonb)
      FROM jsonb_array_elements(mutations) WITH ORDINALITY AS t(m, ord))`

func (e *changeSetEraser) RedactEntity(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID, entityID string) (int, error) {
	return e.exec(ctx, tx,
		`UPDATE flexitype_changeset
		    SET mutations = `+fmt.Sprintf(redactMutations, `m->>'entity_id' = ?`)+`
		  WHERE tenant_id = ?
		    AND mutations @> jsonb_build_array(jsonb_build_object('entity_id', to_jsonb(?::text)))`,
		entityID, tenant.String(), entityID)
}

func (e *changeSetEraser) RedactTenant(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID) (int, error) {
	return e.exec(ctx, tx,
		`UPDATE flexitype_changeset
		    SET mutations = `+fmt.Sprintf(redactMutations, `m->>'entity_id' IS NOT NULL`)+`
		  WHERE tenant_id = ? AND jsonb_array_length(mutations) > 0`,
		tenant.String())
}

func (e *changeSetEraser) exec(ctx context.Context, tx db.Tx, query string, args ...any) (int, error) {
	q := txExecer(tx)
	res, err := q.ExecContext(ctx, bind(query), args...)
	if err != nil {
		return 0, fmt.Errorf("redact change-set mutations: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("redact change-set mutations: %w", err)
	}
	return int(n), nil
}
