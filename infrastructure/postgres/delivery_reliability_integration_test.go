package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestOutboxRetrySchedulingIntegration covers the starvation the outbox lane
// had and the delivery lane did not.
//
// Claims are ordered by id, and a failed row kept its low id with no backoff
// and no attempt cap, so the oldest failing rows were re-claimed first on every
// 2-second pass and no newer envelope was ever reached. Because a webhook
// delivery row and feed_seq are stamped only for a fully dispatched envelope,
// that stopped pub/sub, webhook fan-out and the events feed together.
func TestOutboxRetrySchedulingIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_delivery")
	ctx := context.Background()
	transactor := db.NewTransactor(pool)
	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given one failing envelope and one healthy one behind it", t, func() {
		testdb.TruncateAll(t, pool)
		store := postgres.NewOutboxStore(transactor, postgres.WithOutboxMaxAttempts(3))
		ids := writeEnvelopes(ctx, transactor, store, 2)
		bad, good := ids[0], ids[1]

		claimAll := func() []string {
			envs, err := store.Claim(ctx, "relay-1", 10, time.Minute)
			So(err, ShouldBeNil)
			out := make([]string, 0, len(envs))
			for _, e := range envs {
				out = append(out, e.ID)
			}
			return out
		}
		failFirst := func(claimed []string) {
			results := make([]outbox.Result, 0, len(claimed))
			for _, id := range claimed {
				if id == bad {
					results = append(results, outbox.Result{EnvelopeID: id, Err: errBoom})
					continue
				}
				results = append(results, outbox.Result{EnvelopeID: id})
			}
			So(store.Finalize(ctx, results), ShouldBeNil)
		}

		Convey("When the first pass fails the older envelope and dispatches the newer", func() {
			first := claimAll()
			So(first, ShouldHaveLength, 2)
			failFirst(first)

			Convey("Then the next pass does not re-claim the failing row immediately", func() {
				// Its backoff has not elapsed. Before the fix it had none, so
				// it was claimed first on every pass, forever.
				So(claimAll(), ShouldBeEmpty)
			})

			Convey("Then the dispatched envelope is finished, not retried", func() {
				var dispatched int
				So(pool.Get(&dispatched,
					`SELECT count(*) FROM flexitype_event_outbox WHERE id = $1 AND dispatched_at IS NOT NULL`,
					good), ShouldBeNil)
				So(dispatched, ShouldEqual, 1)
			})
		})

		Convey("When a row exhausts its attempt cap", func() {
			for i := 0; i < 3; i++ {
				// Make it due again so the next claim reaches it.
				_, err := pool.Exec(
					`UPDATE flexitype_event_outbox SET next_attempt_at = now() - interval '1 hour' WHERE id = $1`, bad)
				So(err, ShouldBeNil)
				claimed := claimAll()
				if len(claimed) == 0 {
					break
				}
				failFirst(claimed)
			}

			Convey("Then it is parked rather than retried forever", func() {
				var parked int
				So(pool.Get(&parked,
					`SELECT count(*) FROM flexitype_event_outbox WHERE id = $1 AND parked_at IS NOT NULL`,
					bad), ShouldBeNil)
				So(parked, ShouldEqual, 1)
			})

			Convey("Then a parked row is no longer claimable, so it starves nothing", func() {
				_, err := pool.Exec(
					`UPDATE flexitype_event_outbox SET next_attempt_at = now() - interval '1 hour' WHERE id = $1`, bad)
				So(err, ShouldBeNil)
				So(claimAll(), ShouldBeEmpty)
			})
		})
	})
}

// TestDeliveryLeaseOwnershipIntegration covers the double-send the missing
// ownership predicate allowed.
//
// A batch can take longer to drain through the concurrency semaphore than its
// lease lasts. ReleaseExpired on any replica then returns the delivery to
// pending, another worker claims and POSTs it, and the first worker's Record —
// which matched on id alone — overwrote the new owner's row. Rewinding a
// delivered row to pending with a next_attempt_at caused a third send.
func TestDeliveryLeaseOwnershipIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_delivery")
	ctx := context.Background()
	transactor := db.NewTransactor(pool)
	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a delivery claimed by one worker", t, func() {
		testdb.TruncateAll(t, pool)
		deliveries := seedOneDelivery(ctx, t, pool, transactor)
		now := time.Now().UTC()

		first, err := deliveries.ClaimDue(ctx, 10, time.Minute, now)
		So(err, ShouldBeNil)
		So(first, ShouldHaveLength, 1)
		So(first[0].LeaseExpiresAt.IsZero(), ShouldBeFalse)

		Convey("When its lease expires and a second worker takes over", func() {
			released, err := deliveries.ReleaseExpired(ctx, now.Add(2*time.Minute))
			So(err, ShouldBeNil)
			So(released, ShouldEqual, 1)

			second, err := deliveries.ClaimDue(ctx, 10, time.Minute, now.Add(2*time.Minute))
			So(err, ShouldBeNil)
			So(second, ShouldHaveLength, 1)
			So(second[0].LeaseExpiresAt, ShouldNotEqual, first[0].LeaseExpiresAt)

			So(func() error {
				lost, err := deliveries.Record(ctx, now.Add(2*time.Minute), webhook.Outcome{
					DeliveryID: second[0].ID, LeaseExpiresAt: second[0].LeaseExpiresAt,
					Delivered: true, ResponseCode: 200,
				})
				So(lost, ShouldEqual, 0)
				return err
			}(), ShouldBeNil)

			Convey("Then the first worker's late outcome is refused, not applied", func() {
				lost, err := deliveries.Record(ctx, now.Add(3*time.Minute), webhook.Outcome{
					DeliveryID: first[0].ID, LeaseExpiresAt: first[0].LeaseExpiresAt,
					Delivered: false, ResponseCode: 500, Err: "timeout",
					NextAttemptAt: now.Add(4 * time.Minute),
				})
				So(err, ShouldBeNil)
				So(lost, ShouldEqual, 1)
			})

			Convey("Then the delivered state stands, so there is no third send", func() {
				_, err := deliveries.Record(ctx, now.Add(3*time.Minute), webhook.Outcome{
					DeliveryID: first[0].ID, LeaseExpiresAt: first[0].LeaseExpiresAt,
					Delivered: false, ResponseCode: 500, Err: "timeout",
					NextAttemptAt: now.Add(4 * time.Minute),
				})
				So(err, ShouldBeNil)

				var status string
				So(pool.Get(&status,
					`SELECT status FROM flexitype_webhook_delivery WHERE id = $1`, first[0].ID), ShouldBeNil)
				So(status, ShouldEqual, "delivered")
			})
		})

		Convey("When the owning worker records its outcome", func() {
			lost, err := deliveries.Record(ctx, now, webhook.Outcome{
				DeliveryID: first[0].ID, LeaseExpiresAt: first[0].LeaseExpiresAt,
				Delivered: true, ResponseCode: 200,
			})

			Convey("Then it is applied", func() {
				So(err, ShouldBeNil)
				So(lost, ShouldEqual, 0)

				var status string
				So(pool.Get(&status,
					`SELECT status FROM flexitype_webhook_delivery WHERE id = $1`, first[0].ID), ShouldBeNil)
				So(status, ShouldEqual, "delivered")
			})
		})
	})
}

// errBoom is the dispatch failure the retry-scheduling cases inject.
var errBoom = &dispatchError{}

type dispatchError struct{}

func (*dispatchError) Error() string { return "handler unavailable" }

// seedOneDelivery writes one envelope, one subscription and expands them into
// a single pending delivery row.
func seedOneDelivery(ctx context.Context, t *testing.T, pool *sqlx.DB, transactor db.Transactor) webhook.DeliveryStore {
	t.Helper()
	subID := ulid.New().String()
	pool.MustExec(`INSERT INTO flexitype_webhook_subscription
	  (id, tenant_id, name, url, secret, event_types, active, created_at, updated_at)
	  VALUES ($1, 'default', 'hook', 'https://example.test/hook', 's', '{}', true, now(), now())`, subID)

	store := postgres.NewOutboxStore(transactor)
	ids := writeEnvelopes(ctx, transactor, store, 1)
	claimed, err := store.Claim(ctx, "relay-seed", 10, time.Minute)
	So(err, ShouldBeNil)
	So(claimed, ShouldHaveLength, 1)
	So(store.Finalize(ctx, []outbox.Result{{EnvelopeID: ids[0]}}), ShouldBeNil)

	return postgres.NewDeliveryStore(pool)
}
