package postgres_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestDeadLetterRecoveryPostgres covers what happens to a dead letter between
// the outage that created it and the redrive that recovers it.
//
// Two things used to work against that recovery. Retention pruning deleted an
// envelope once no PENDING or INFLIGHT delivery referenced it — dead ones did
// not count — and the delivery row cascaded with it, so the evidence an
// operator needs in order to redrive disappeared on a timer. And redriving was
// one API call per delivery, which after a real outage means thousands.
func TestDeadLetterRecoveryPostgres(t *testing.T) {
	pool := testdb.Open(t, "delivery_recovery")
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an old envelope whose delivery went dead", t, func() {
		testdb.TruncateAll(t, pool)
		tenant := valueobjects.DefaultTenant
		old := time.Now().UTC().Add(-30 * 24 * time.Hour)

		subID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_subscription
			(id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
			VALUES ($1,$2,'sub','https://example.test','s','', '{}', true, $3, $3)`,
			subID, tenant.String(), old)

		envID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_event_outbox
			(id, tenant_id, event_type, aggregate_type, aggregate_id, payload,
			 occurred_at, recorded_at, dispatched_at, feed_seq)
			VALUES ($1,$2,'flexitype.value.updated','value','v1','{}'::jsonb,$3,$3,$3,1)`,
			envID, tenant.String(), old)

		deadID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_delivery
			(id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, attempts,
			 next_attempt_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'flexitype.value.updated',1,'dead',25,$5,$5,$5)`,
			deadID, subID, envID, tenant.String(), old)

		count := func(q string, args ...any) int {
			var n int
			So(pool.Get(&n, q, args...), ShouldBeNil)
			return n
		}

		Convey("When retention prunes everything older than the cutoff", func() {
			n, err := postgres.NewFeedStore(pool).Prune(ctx, time.Now().UTC())
			So(err, ShouldBeNil)

			Convey("Then the dead letter and its envelope survive", func() {
				So(n, ShouldEqual, 0)
				So(count(`SELECT count(*) FROM flexitype_event_outbox WHERE id = $1`, envID), ShouldEqual, 1)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1`, deadID), ShouldEqual, 1)
			})
		})

		Convey("When the dead letters are redriven in bulk", func() {
			store := postgres.NewDeliveryStore(pool)
			moved, err := store.RedeliverMatching(ctx,
				appwebhook.DeliveryFilter{TenantID: tenant}, time.Now().UTC())

			Convey("Then the delivery returns to pending with a fresh budget", func() {
				So(err, ShouldBeNil)
				So(moved, ShouldEqual, 1)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery
				          WHERE id = $1 AND status = 'pending' AND attempts = 0`, deadID), ShouldEqual, 1)
			})
		})

		Convey("When a redrive names a different subscription", func() {
			store := postgres.NewDeliveryStore(pool)
			moved, err := store.RedeliverMatching(ctx,
				appwebhook.DeliveryFilter{TenantID: tenant, SubscriptionID: ulid.New()}, time.Now().UTC())

			Convey("Then nothing moves", func() {
				So(err, ShouldBeNil)
				So(moved, ShouldEqual, 0)
			})
		})

		Convey("When the delivery has settled and pruning runs", func() {
			pool.MustExec(`UPDATE flexitype_webhook_delivery SET status = 'delivered' WHERE id = $1`, deadID)
			n, err := postgres.NewFeedStore(pool).Prune(ctx, time.Now().UTC())

			Convey("Then the envelope is pruned and the delivery cascades", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 1)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1`, deadID), ShouldEqual, 0)
			})
		})
	})
}

// TestDeadLetterRetentionIntegration covers the bound that stops a dead
// delivery pinning its envelope for ever.
func TestDeadLetterRetentionIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_deadletter")
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an old dead delivery pinning an expired envelope", t, func() {
		testdb.TruncateTables(t, pool, "flexitype_webhook_delivery", "flexitype_webhook_subscription", "flexitype_event_outbox")
		tenant := valueobjects.DefaultTenant
		old := time.Now().UTC().Add(-90 * 24 * time.Hour)

		subID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_subscription
			(id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
			VALUES ($1,$2,'sub','https://example.test','s','', '{}', true, $3, $3)`,
			subID, tenant.String(), old)
		envID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_event_outbox
			(id, tenant_id, event_type, aggregate_type, aggregate_id, payload,
			 occurred_at, recorded_at, dispatched_at, feed_seq)
			VALUES ($1,$2,'flexitype.value.updated','value','v1','{}'::jsonb,$3,$3,$3,1)`,
			envID, tenant.String(), old)
		deadID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_delivery
			(id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, attempts,
			 next_attempt_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'flexitype.value.updated',1,'dead',25,$5,$5,$5)`,
			deadID, subID, envID, tenant.String(), old)

		count := func(q string, args ...any) int {
			var n int
			So(pool.Get(&n, q, args...), ShouldBeNil)
			return n
		}
		store := postgres.NewFeedStore(pool)

		Convey("When the dead-letter cutoff has not been reached", func() {
			removed, err := store.PruneDeadLetters(ctx, time.Now().UTC().Add(-365*24*time.Hour))

			Convey("Then it survives, so it can still be redriven", func() {
				So(err, ShouldBeNil)
				So(removed, ShouldEqual, 0)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1`, deadID),
					ShouldEqual, 1)
			})
		})

		Convey("When the dead letter is past its retention", func() {
			removed, err := store.PruneDeadLetters(ctx, time.Now().UTC().Add(-30*24*time.Hour))
			So(err, ShouldBeNil)

			Convey("Then it is removed", func() {
				So(removed, ShouldEqual, 1)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1`, deadID),
					ShouldEqual, 0)
			})

			Convey("Then its envelope becomes prunable, so retention bounds the outbox again", func() {
				n, perr := store.Prune(ctx, time.Now().UTC())
				So(perr, ShouldBeNil)
				So(n, ShouldEqual, 1)
				So(count(`SELECT count(*) FROM flexitype_event_outbox WHERE id = $1`, envID),
					ShouldEqual, 0)
			})
		})

		Convey("When a delivery is dead but recent", func() {
			pool.MustExec(`UPDATE flexitype_webhook_delivery SET updated_at = now() WHERE id = $1`, deadID)
			removed, err := store.PruneDeadLetters(ctx, time.Now().UTC().Add(-30*24*time.Hour))

			Convey("Then it is kept: the clock starts when the row went dead", func() {
				So(err, ShouldBeNil)
				So(removed, ShouldEqual, 0)
			})
		})
	})
}

// TestRedeliverRampsTheBacklogIntegration covers the bulk redrive's batching:
// the statement is bounded, as the pruner's is, so a large backlog is not one
// long transaction holding locks over a slice of the delivery table.
func TestRedeliverRampsTheBacklogIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_redrive")
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a backlog of dead deliveries larger than one batch", t, func() {
		testdb.TruncateTables(t, pool, "flexitype_webhook_delivery", "flexitype_webhook_subscription", "flexitype_event_outbox")
		tenant := valueobjects.DefaultTenant
		now := time.Now().UTC()

		subID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_subscription
			(id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
			VALUES ($1,$2,'sub','https://example.test','s','', '{}', true, $3, $3)`,
			subID, tenant.String(), now)
		envID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_event_outbox
			(id, tenant_id, event_type, aggregate_type, aggregate_id, payload,
			 occurred_at, recorded_at, dispatched_at, feed_seq)
			VALUES ($1,$2,'flexitype.value.updated','value','v1','{}'::jsonb,$3,$3,$3,1)`,
			envID, tenant.String(), now)

		// 600 rows: past the 500-row redrive batch, so the loop is exercised.
		const rows = 600
		pool.MustExec(`
			INSERT INTO flexitype_webhook_delivery
			  (id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, attempts,
			   next_attempt_at, created_at, updated_at)
			SELECT lpad(g::text, 26, '0'), $1, $2, $3, 'flexitype.value.updated', 1, 'dead', 25, $4, $4, $4
			  FROM generate_series(1, $5) g`,
			subID, envID, tenant.String(), now, rows)

		Convey("When they are redriven in bulk", func() {
			moved, err := postgres.NewDeliveryStore(pool).RedeliverMatching(ctx,
				appwebhook.DeliveryFilter{TenantID: tenant}, now)

			Convey("Then every one moves, across batches", func() {
				So(err, ShouldBeNil)
				So(moved, ShouldEqual, rows)
				var pending int
				So(pool.Get(&pending,
					`SELECT count(*) FROM flexitype_webhook_delivery WHERE status = 'pending'`), ShouldBeNil)
				So(pending, ShouldEqual, rows)
			})

			// The ramp is PER SUBSCRIPTION. Spreading one subscription's rows
			// over the window was head-of-line blocking rather than
			// smoothing: ClaimDue takes only the lowest feed_seq, and only
			// if it is due, so the head's offset gated everything behind it.
			// See TestRedriveRampIsPerSubscriptionIntegration.
			Convey("Then one subscription's rows share one instant, so the head is not blocked", func() {
				var distinct int
				So(pool.Get(&distinct,
					`SELECT count(DISTINCT next_attempt_at) FROM flexitype_webhook_delivery`), ShouldBeNil)
				So(distinct, ShouldEqual, 1)

				var offset float64
				So(pool.Get(&offset,
					`SELECT EXTRACT(EPOCH FROM (min(next_attempt_at) - $1::timestamptz))
					   FROM flexitype_webhook_delivery`, now), ShouldBeNil)
				So(offset, ShouldBeGreaterThanOrEqualTo, 0.0)
				So(offset, ShouldBeLessThan, 300.0) // inside the ramp window
			})

			Convey("Then none is scheduled before the redrive itself", func() {
				var early int
				So(pool.Get(&early,
					`SELECT count(*) FROM flexitype_webhook_delivery WHERE next_attempt_at < $1`,
					now), ShouldBeNil)
				So(early, ShouldEqual, 0)
			})
		})
	})
}
