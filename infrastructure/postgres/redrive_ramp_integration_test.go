package postgres_test

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestRedriveRampIsPerSubscriptionIntegration proves a redrive makes the head
// of each subscription's backlog due immediately.
//
// The ramp gave every revived delivery an independent random offset in a
// 5-minute window. ClaimDue takes a subscription's single lowest-feed_seq
// pending row, and only if that row is due, so the head's offset gated
// everything behind it: measured over 20 revived rows, the head drew +4m26s
// and NOTHING was claimable for four and a half minutes while 19 later
// deliveries were already due. The operator sees a redrive that reported
// success move zero deliveries.
//
// The per-subscription serialization is already the per-endpoint rate limit.
// What the ramp can still do is stop many recovered subscriptions firing at
// one instant, so the offset is per subscription and every row of one
// subscription shares it.
func TestRedriveRampIsPerSubscriptionIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres")
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given two subscriptions with a backlog of dead deliveries", t, func() {
		testdb.TruncateAll(t, pool)
		tenant := valueobjects.DefaultTenant
		at := time.Now().UTC().Add(-time.Hour)

		newSub := func(name string) ulid.ID {
			id := ulid.New()
			pool.MustExec(`INSERT INTO flexitype_webhook_subscription
				(id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at)
				VALUES ($1,$2,$3,'https://example.test','s','', '{}', true, $4, $4)`,
				id, tenant.String(), name, at)
			return id
		}
		subA, subB := newSub("sub-a"), newSub("sub-b")

		dead := func(sub ulid.ID, seq int) {
			envID := ulid.New()
			pool.MustExec(`INSERT INTO flexitype_event_outbox
				(id, tenant_id, event_type, aggregate_type, aggregate_id, payload,
				 occurred_at, recorded_at, dispatched_at, feed_seq)
				VALUES ($1,$2,'flexitype.value.updated','value','v1','{}'::jsonb,$3,$3,$3,$4)`,
				envID, tenant.String(), at, seq)
			pool.MustExec(`INSERT INTO flexitype_webhook_delivery
				(id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, attempts,
				 next_attempt_at, created_at, updated_at)
				VALUES ($1,$2,$3,$4,'flexitype.value.updated',$5,'dead',25,$6,$6,$6)`,
				ulid.New(), sub, envID, tenant.String(), seq, at)
		}
		for seq := 1; seq <= 20; seq++ {
			dead(subA, seq)
		}
		for seq := 21; seq <= 25; seq++ {
			dead(subB, seq)
		}

		Convey("When the backlog is redriven", func() {
			now := time.Now().UTC()
			moved, err := postgres.NewDeliveryStore(pool).RedeliverMatching(ctx,
				appwebhook.DeliveryFilter{TenantID: tenant}, now)
			So(err, ShouldBeNil)
			So(moved, ShouldEqual, 25)

			offsets := func(sub ulid.ID) []float64 {
				var out []float64
				So(pool.Select(&out, `SELECT extract(epoch FROM (next_attempt_at - $2::timestamptz))
					  FROM flexitype_webhook_delivery
					 WHERE subscription_id = $1 ORDER BY feed_seq`, sub, now), ShouldBeNil)
				return out
			}

			Convey("Then every row of one subscription shares one offset", func() {
				for _, sub := range []ulid.ID{subA, subB} {
					got := offsets(sub)
					So(got, ShouldNotBeEmpty)
					for _, o := range got {
						So(o, ShouldEqual, got[0])
					}
				}
			})

			Convey("Then the head is due no later than anything behind it", func() {
				// This is what the per-row jitter broke: the head could draw
				// the largest offset and block the whole subscription.
				for _, sub := range []ulid.ID{subA, subB} {
					got := offsets(sub)
					for _, o := range got {
						So(got[0], ShouldBeLessThanOrEqualTo, o)
					}
				}
			})

			Convey("Then the offset stays inside the ramp window", func() {
				for _, sub := range []ulid.ID{subA, subB} {
					So(offsets(sub)[0], ShouldBeGreaterThanOrEqualTo, 0.0)
					So(offsets(sub)[0], ShouldBeLessThan, 300.0)
				}
			})
		})
	})
}
