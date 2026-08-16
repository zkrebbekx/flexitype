package postgres_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestStrandedBacklogIsBoundedPostgres covers issue #588.
//
// Deactivating a subscription RESTS its backlog: the rows stay pending so
// reactivating resumes them. Nothing owned them after that. ClaimDue skips an
// inactive subscription, the envelope prune keeps any envelope with a pending
// delivery, and the dead-letter and parked prunes look at other states — so a
// deactivated subscription pinned its backlog, and its envelopes, for ever.
// Retention stopped bounding the largest table, and the pinned set grew with
// every deactivation.
func TestStrandedBacklogIsBoundedPostgres(t *testing.T) {
	pool := testdb.Open(t, "stranded_backlog")
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	seed := func(active bool, age time.Duration) (string, string) {
		tenant := valueobjects.DefaultTenant
		at := time.Now().UTC().Add(-age)
		subID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_subscription
			(id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
			VALUES ($1,$2,'sub','https://example.test','s','', '{}', $4, $3, $3)`,
			subID, tenant.String(), at, active)
		envID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_event_outbox
			(id, tenant_id, event_type, aggregate_type, aggregate_id, payload,
			 occurred_at, recorded_at, dispatched_at, feed_seq)
			VALUES ($1,$2,'flexitype.value.updated','value','v1','{}'::jsonb,$3,$3,$3,1)`,
			envID, tenant.String(), at)
		delID := ulid.New()
		pool.MustExec(`INSERT INTO flexitype_webhook_delivery
			(id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, attempts,
			 next_attempt_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'flexitype.value.updated',1,'pending',0,$5,$5,$5)`,
			delID, subID, envID, tenant.String(), at)
		return envID.String(), delID.String()
	}

	count := func(q string, args ...any) int {
		var n int
		So(pool.Get(&n, q, args...), ShouldBeNil)
		return n
	}

	Convey("Given a pending delivery of a long-deactivated subscription", t, func() {
		testdb.TruncateAll(t, pool)
		envID, delID := seed(false, 400*24*time.Hour)
		store := postgres.NewFeedStore(pool)

		Convey("When the pruner runs its full pass", func() {
			stranded, err := store.DeadLetterStranded(ctx, time.Now().UTC().Add(-30*24*time.Hour))
			So(err, ShouldBeNil)
			// The cutoffs are a minute AHEAD, and that minute is not padding.
			//
			// DeadLetterStranded stamps updated_at with the DATABASE clock
			// (`now()`), and these two calls pass a cutoff read from the
			// APPLICATION clock microseconds later. Where the database clock
			// leads the application's — a few milliseconds is ordinary between
			// a host and a container — the row it just marked is not yet
			// "older than the cutoff" and survives a prune that the test then
			// reports as a product defect. In a deployment the two passes are
			// minutes or days apart and retention is 30 days, so the skew is
			// invisible; only a test that collapses them to one instant can
			// see it.
			soon := time.Now().UTC().Add(time.Minute)
			_, err = store.PruneDeadLetters(ctx, soon)
			So(err, ShouldBeNil)
			_, err = store.Prune(ctx, soon)
			So(err, ShouldBeNil)

			Convey("Then the delivery and its envelope are gone", func() {
				// Before this, all three pruners left them: the row was
				// pending, which none of them owns.
				So(stranded, ShouldEqual, 1)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1`, delID), ShouldEqual, 0)
				So(count(`SELECT count(*) FROM flexitype_event_outbox WHERE id = $1`, envID), ShouldEqual, 0)
			})
		})
	})

	Convey("Given a pending delivery of an ACTIVE subscription", t, func() {
		testdb.TruncateAll(t, pool)
		_, delID := seed(true, 400*24*time.Hour)
		store := postgres.NewFeedStore(pool)

		Convey("When the stranded pass runs", func() {
			n, err := store.DeadLetterStranded(ctx, time.Now().UTC().Add(-30*24*time.Hour))
			So(err, ShouldBeNil)

			Convey("Then it is left alone — the delivery worker owns it", func() {
				So(n, ShouldEqual, 0)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1 AND status = 'pending'`,
					delID), ShouldEqual, 1)
			})
		})
	})

	Convey("Given a subscription deactivated only recently", t, func() {
		testdb.TruncateAll(t, pool)
		_, delID := seed(false, time.Hour)
		store := postgres.NewFeedStore(pool)

		Convey("When the stranded pass runs", func() {
			n, err := store.DeadLetterStranded(ctx, time.Now().UTC().Add(-30*24*time.Hour))
			So(err, ShouldBeNil)

			Convey("Then the backlog rests, so reactivating still resumes it", func() {
				// The whole point of resting a backlog is that turning the
				// subscription off during an incident does not destroy it.
				So(n, ShouldEqual, 0)
				So(count(`SELECT count(*) FROM flexitype_webhook_delivery WHERE id = $1 AND status = 'pending'`,
					delID), ShouldEqual, 1)
			})
		})
	})
}
