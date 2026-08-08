package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// TestOutboxParkedRecoveryIntegration covers issue #478: a parked envelope
// used to be permanently undeliverable (Claim skipped it and nothing cleared
// parked_at), invisible (no feed_seq, so no feed entry), unprunable (the
// envelope prune requires feed_seq) and counted as pending for ever. These
// tests pin the recovery surface: the parked listing, the redrive that makes
// the envelope deliver, the metric split and the parked retention bound.
func TestOutboxParkedRecoveryIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	transactor := db.NewTransactor(pool)

	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := postgres.NewOutboxStore(transactor)
	ops, ok := store.(outbox.OpsStore)
	if !ok {
		t.Fatal("postgres outbox store must implement outbox.OpsStore")
	}
	tenant := valueobjects.TenantID("default")

	park := func(ids ...string) {
		for _, id := range ids {
			pool.MustExec(
				`UPDATE flexitype_event_outbox
				  SET parked_at = now(), attempts = 25, last_error = 'handler kept failing',
				      next_attempt_at = now(), claimed_at = NULL, claimed_by = NULL
				  WHERE id = $1`, id)
		}
	}

	Convey("Given an outbox with two parked envelopes among pending ones", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)
		ids := writeEnvelopes(ctx, transactor, store, 4)
		park(ids[0], ids[1])

		var mu sync.Mutex
		delivered := map[string]int{}
		dispatcher := events.NewDispatcher()
		dispatcher.RegisterFunc("counter", func(_ context.Context, e events.Envelope) error {
			mu.Lock()
			delivered[e.ID]++
			mu.Unlock()
			return nil
		})
		relay := outbox.NewRelay(store, dispatcher, outbox.WithRelayID("relay-parked"))

		Convey("When the relay drains", func() {
			relay.DrainOnce(ctx)

			Convey("Then the parked envelopes are not claimed and stay undispatched", func() {
				mu.Lock()
				So(delivered, ShouldNotContainKey, ids[0])
				So(delivered, ShouldNotContainKey, ids[1])
				So(delivered[ids[2]], ShouldEqual, 1)
				So(delivered[ids[3]], ShouldEqual, 1)
				mu.Unlock()

				var stillParked int
				So(pool.Get(&stillParked,
					`SELECT count(*) FROM flexitype_event_outbox
					  WHERE parked_at IS NOT NULL AND dispatched_at IS NULL`), ShouldBeNil)
				So(stillParked, ShouldEqual, 2)
			})
		})

		Convey("When the parked set is listed", func() {
			rows, total, err := ops.ListParked(ctx, outbox.ParkedFilter{TenantID: tenant}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then exactly the parked envelopes return, oldest first, with their evidence", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(rows, ShouldHaveLength, 2)
				So(rows[0].ID, ShouldEqual, ids[0])
				So(rows[1].ID, ShouldEqual, ids[1])
				So(rows[0].Attempts, ShouldEqual, 25)
				So(rows[0].LastError, ShouldEqual, "handler kept failing")
				So(rows[0].ParkedAt.IsZero(), ShouldBeFalse)
			})
		})

		Convey("When the listing is narrowed to one envelope id", func() {
			rows, total, err := ops.ListParked(ctx, outbox.ParkedFilter{TenantID: tenant, ID: ids[1]}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only that envelope returns", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(rows, ShouldHaveLength, 1)
				So(rows[0].ID, ShouldEqual, ids[1])
			})
		})

		Convey("When the listing is narrowed to a foreign event type", func() {
			rows, total, err := ops.ListParked(ctx, outbox.ParkedFilter{TenantID: tenant, EventType: "flexitype.other.event"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then nothing returns", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 0)
				So(rows, ShouldBeEmpty)
			})
		})

		Convey("When the parked envelopes are redriven and the relay drains", func() {
			n, err := ops.Redrive(ctx, transactor, outbox.ParkedFilter{TenantID: tenant})
			relay.DrainOnce(ctx)

			Convey("Then both formerly parked envelopes deliver with a feed_seq", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 2)

				mu.Lock()
				So(delivered[ids[0]], ShouldEqual, 1)
				So(delivered[ids[1]], ShouldEqual, 1)
				mu.Unlock()

				var parked, undispatched, seqless int
				So(pool.Get(&parked,
					`SELECT count(*) FROM flexitype_event_outbox WHERE parked_at IS NOT NULL`), ShouldBeNil)
				So(pool.Get(&undispatched,
					`SELECT count(*) FROM flexitype_event_outbox WHERE dispatched_at IS NULL`), ShouldBeNil)
				So(pool.Get(&seqless,
					`SELECT count(*) FROM flexitype_event_outbox WHERE feed_seq IS NULL`), ShouldBeNil)
				So(parked, ShouldEqual, 0)
				So(undispatched, ShouldEqual, 0)
				So(seqless, ShouldEqual, 0)
			})

			Convey("Then the redrive reset the retry budget", func() {
				// Every attempt is counted, success included, so a redriven
				// envelope that delivers on its first fresh try shows exactly
				// 1 — not 26, which is what a redrive without the reset would
				// leave.
				var maxAttempts int
				So(pool.Get(&maxAttempts,
					`SELECT coalesce(max(attempts), -1) FROM flexitype_event_outbox
					  WHERE id IN ($1, $2)`, ids[0], ids[1]), ShouldBeNil)
				So(maxAttempts, ShouldEqual, 1)
			})
		})

		Convey("When a redrive targets one envelope id", func() {
			n, err := ops.Redrive(ctx, transactor, outbox.ParkedFilter{TenantID: tenant, ID: ids[0]})

			Convey("Then the sibling stays parked", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 1)

				var parked int
				So(pool.Get(&parked,
					`SELECT count(*) FROM flexitype_event_outbox WHERE parked_at IS NOT NULL`), ShouldBeNil)
				So(parked, ShouldEqual, 1)
			})
		})

		Convey("When a redrive targets a foreign event type", func() {
			n, err := ops.Redrive(ctx, transactor, outbox.ParkedFilter{TenantID: tenant, EventType: "flexitype.other.event"})

			Convey("Then nothing moves", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 0)
			})
		})

		Convey("When the delivery stats snapshot runs", func() {
			depth, err := postgres.NewDeliveryStats(pool).Snapshot(ctx)

			Convey("Then parked envelopes are out of the pending depth and on their own gauge", func() {
				So(err, ShouldBeNil)
				So(depth.OutboxPending, ShouldEqual, 2)
				So(depth.OutboxParked, ShouldEqual, 2)
			})
		})

		Convey("When everything pending is dispatched and only parked rows remain", func() {
			relay.DrainOnce(ctx)
			depth, err := postgres.NewDeliveryStats(pool).Snapshot(ctx)

			Convey("Then the pending age is zero rather than pinned by the parked rows", func() {
				So(err, ShouldBeNil)
				So(depth.OutboxPending, ShouldEqual, 0)
				So(depth.OutboxParked, ShouldEqual, 2)
				So(depth.OldestPendingAge, ShouldEqual, time.Duration(0))
			})
		})
	})

	Convey("Given one envelope parked long ago and one parked recently", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)
		ids := writeEnvelopes(ctx, transactor, store, 2)
		park(ids[0], ids[1])
		pool.MustExec(
			`UPDATE flexitype_event_outbox SET parked_at = now() - interval '31 days' WHERE id = $1`, ids[0])

		feedStore := postgres.NewFeedStore(pool)

		Convey("When the parked prune runs with a 30-day cutoff", func() {
			removed, err := feedStore.PruneParked(ctx, time.Now().UTC().Add(-30*24*time.Hour))

			Convey("Then only the envelope past retention is deleted", func() {
				So(err, ShouldBeNil)
				So(removed, ShouldEqual, 1)

				var remaining []string
				So(pool.Select(&remaining, `SELECT id FROM flexitype_event_outbox`), ShouldBeNil)
				So(remaining, ShouldResemble, []string{ids[1]})
			})
		})

		Convey("When the envelope prune runs instead", func() {
			removed, err := feedStore.Prune(ctx, time.Now().UTC())

			Convey("Then parked rows are untouched — only the parked prune may delete them", func() {
				So(err, ShouldBeNil)
				So(removed, ShouldEqual, 0)

				var parked int
				So(pool.Get(&parked,
					`SELECT count(*) FROM flexitype_event_outbox WHERE parked_at IS NOT NULL`), ShouldBeNil)
				So(parked, ShouldEqual, 2)
			})
		})
	})
}

// TestOutboxParkKnobsIntegration pins that the attempt budget and the retry
// ceiling are real knobs on the adapter (#478): the facade exposes them as
// WithOutboxMaxAttempts / WithOutboxRetryCeiling and the service reads
// FLEXITYPE_OUTBOX_MAX_ATTEMPTS / FLEXITYPE_OUTBOX_RETRY_CEILING.
func TestOutboxParkKnobsIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	transactor := db.NewTransactor(pool)

	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given an outbox store with a budget of two attempts and a low retry ceiling", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery`)
		store := postgres.NewOutboxStore(transactor,
			postgres.WithOutboxMaxAttempts(2),
			postgres.WithOutboxRetryCeiling(2*time.Second))
		ids := writeEnvelopes(ctx, transactor, store, 1)

		fail := func() {
			envs, err := store.Claim(ctx, "relay-knobs", 10, time.Minute)
			So(err, ShouldBeNil)
			So(envs, ShouldHaveLength, 1)
			So(store.Finalize(ctx, []outbox.Result{
				{EnvelopeID: ids[0], Err: context.DeadlineExceeded},
			}), ShouldBeNil)
			// The backoff schedules the next attempt in the future; make the
			// row due again so the next claim can reach it.
			pool.MustExec(`UPDATE flexitype_event_outbox SET next_attempt_at = now(), claimed_at = NULL`)
		}

		Convey("When the envelope fails once", func() {
			fail()

			Convey("Then it is not parked yet and the backoff respects the ceiling", func() {
				var parked int
				So(pool.Get(&parked,
					`SELECT count(*) FROM flexitype_event_outbox WHERE parked_at IS NOT NULL`), ShouldBeNil)
				So(parked, ShouldEqual, 0)
			})
		})

		Convey("When the envelope fails twice", func() {
			fail()
			fail()

			Convey("Then the second failure parks it — the budget is the knob, not the constant", func() {
				var parked, attempts int
				So(pool.Get(&parked,
					`SELECT count(*) FROM flexitype_event_outbox WHERE parked_at IS NOT NULL`), ShouldBeNil)
				So(pool.Get(&attempts,
					`SELECT attempts FROM flexitype_event_outbox`), ShouldBeNil)
				So(parked, ShouldEqual, 1)
				So(attempts, ShouldEqual, 2)
			})
		})

		Convey("When a high-attempt failure is finalized", func() {
			pool.MustExec(`UPDATE flexitype_event_outbox SET attempts = 20`)
			_, err := store.Claim(ctx, "relay-knobs", 10, time.Minute)
			So(err, ShouldBeNil)
			So(store.Finalize(ctx, []outbox.Result{
				{EnvelopeID: ids[0], Err: context.DeadlineExceeded},
			}), ShouldBeNil)

			Convey("Then the retry ceiling caps the backoff", func() {
				var secondsAway float64
				So(pool.Get(&secondsAway,
					`SELECT EXTRACT(EPOCH FROM (next_attempt_at - now())) FROM flexitype_event_outbox`), ShouldBeNil)
				// power(4, ...) would be days out; the 2s ceiling keeps it near now.
				So(secondsAway, ShouldBeLessThanOrEqualTo, 2.5)
			})
		})
	})
}
