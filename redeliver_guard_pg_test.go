package flexitype_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestRedeliverDoesNotRewindAnInflightDelivery is the regression for #504.
//
// Single-row Redeliver checked the status precondition in a separate SELECT,
// but its UPDATE had no status guard. A worker that claimed the row between
// the two statements was rewound to pending mid-send, and the endpoint
// received the payload twice. The guard now lives inside the UPDATE, and
// zero rows affected is a conflict.
//
// The race is forced deterministically: a raw transaction locks the row
// (the claim in progress), Redeliver reads the committed 'failed' status and
// blocks on the locked UPDATE, the raw transaction flips the row to
// 'inflight' and commits, and Redeliver's UPDATE re-checks its predicate on
// the new row version.
func TestRedeliverDoesNotRewindAnInflightDelivery(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := postgres.NewDeliveryStore(pool)
	tenant := valueobjects.DefaultTenant

	seed := func(id ulid.ID, status string) {
		subID := ulid.New()
		_, err := pool.Exec(`INSERT INTO flexitype_webhook_subscription
			(id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
			VALUES ($1, $2, 'sub', 'https://example.invalid/hook', 'shh', '', '{}', true, now(), now())`,
			subID.String(), tenant.String())
		So(err, ShouldBeNil)
		envID := ulid.New()
		_, err = pool.Exec(`INSERT INTO flexitype_event_outbox
			(id, tenant_id, actor, event_type, aggregate_type, aggregate_id, payload,
			 occurred_at, recorded_at, dispatched_at, attempts, last_error, feed_seq, next_attempt_at)
			VALUES ($1, $2, 'test', 'value.set', 'attribute_value', 'e1', '{}',
			 now(), now(), now(), 1, '', 1, now())`,
			envID.String(), tenant.String())
		So(err, ShouldBeNil)
		_, err = pool.Exec(`INSERT INTO flexitype_webhook_delivery
			(id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status,
			 attempts, next_attempt_at, lease_expires_at, last_error, response_code, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'value.set', 1, $5, 1, now(), NULL, '', 0, now(), now())`,
			id.String(), subID.String(), envID.String(), tenant.String(), status)
		So(err, ShouldBeNil)
	}
	statusOf := func(id ulid.ID) string {
		var s string
		So(pool.Get(&s, `SELECT status FROM flexitype_webhook_delivery WHERE id = $1`, id.String()), ShouldBeNil)
		return s
	}

	Convey("Given a failed delivery", t, func() {
		truncateAll(t, pool)

		Convey("When it is redriven with no concurrent claim", func() {
			id := ulid.New()
			seed(id, "failed")
			err := store.Redeliver(context.Background(), tenant, id, time.Now().UTC())

			Convey("Then it returns to pending", func() {
				So(err, ShouldBeNil)
				So(statusOf(id), ShouldEqual, webhook.StatusPending)
			})
		})

		Convey("When a worker claims it between the read and the rewind", func() {
			id := ulid.New()
			seed(id, "failed")

			tx, err := pool.Beginx()
			So(err, ShouldBeNil)
			// The claim in progress: hold the row lock.
			_, err = tx.Exec(`SELECT id FROM flexitype_webhook_delivery WHERE id = $1 FOR UPDATE`, id.String())
			So(err, ShouldBeNil)

			done := make(chan error, 1)
			go func() {
				// Reads 'failed' (committed), then blocks on the UPDATE.
				done <- store.Redeliver(context.Background(), tenant, id, time.Now().UTC())
			}()
			time.Sleep(300 * time.Millisecond)
			_, err = tx.Exec(`UPDATE flexitype_webhook_delivery SET status = 'inflight' WHERE id = $1`, id.String())
			So(err, ShouldBeNil)
			So(tx.Commit(), ShouldBeNil)
			redeliverErr := <-done

			Convey("Then the rewind is refused and the delivery stays inflight", func() {
				So(domainerrors.CodeOf(redeliverErr), ShouldEqual, domainerrors.CodeConflict)
				So(statusOf(id), ShouldEqual, webhook.StatusInflight)
			})
		})
	})
}
