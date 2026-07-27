package erasure

import (
	"context"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// ResidualEraser redacts an erased entity's values from the records that
// copied them.
//
// PurgeEntity and PurgeTenant hard-delete values, links, revisions, search
// documents and media blobs — and reported success while the same values
// stayed readable in two other places:
//
//   - The event log. Value set/updated/removed payloads embed the value, and
//     the feed API serves them until retention pruning: 7 days by default,
//     arbitrarily long by configuration, and FOREVER for rows never expanded,
//     because pruning requires a feed_seq. Webhook delivery rows read the same
//     payload through their envelope reference.
//   - The activity log, which persists a value snapshot in every entry's
//     before/after state. PurgeTenant deliberately keeps that log, so the
//     erasure remains provable.
//
// Redaction rather than deletion, in both places. The activity log has to
// survive for the erasure to be provable, and the event feed's sequence is
// gapless by design — deleting rows would break the one guarantee a consumer
// relies on. So the row stays, its identifiers stay, and the value content is
// replaced with a marker.
type ResidualEraser interface {
	// Name identifies the store in a report and in an error.
	Name() string

	// RedactEntity redacts one entity's values inside the caller's
	// transaction and returns how many records it changed.
	RedactEntity(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID, entityID string) (int, error)

	// RedactTenant redacts every entity's values for one tenant.
	RedactTenant(ctx context.Context, tx db.Tx, tenant valueobjects.TenantID) (int, error)
}

// RedactedMarker replaces the value content of a redacted record. It is a
// deliberate, greppable marker: a reader has to be able to tell an erased
// value from one that was never set.
const RedactedMarker = "erased"
