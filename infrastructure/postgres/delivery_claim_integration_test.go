package postgres_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestDeliveryClaimIntegration exercises the delivery worker's claim/lease/
// reaper SQL against Postgres: one-inflight-per-subscription claiming and
// lease reclamation of an abandoned inflight row.
func TestDeliveryClaimIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	transactor := db.NewTransactor(pool)

	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	outboxStore := postgres.NewOutboxStore(transactor)
	subs := postgres.NewSubscriptionStore(pool)
	deliveries := postgres.NewDeliveryStore(pool)

	Convey("Given a subscription and three fanned-out deliveries", t, func() {
		pool.MustExec(`TRUNCATE flexitype_event_outbox, flexitype_webhook_delivery, flexitype_webhook_subscription`)

		sub := webhook.Subscription{
			ID:        ulid.New(),
			TenantID:  valueobjects.TenantID("default"),
			Name:      "consumer",
			URL:       "http://receiver.internal/hook",
			Secret:    "s3cr3t",
			Active:    true,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		So(subs.Create(ctx, sub), ShouldBeNil)

		// Write three envelopes and run one drain pass to fan out one
		// pending delivery row per envelope for this subscription.
		ids := writeEnvelopes(ctx, transactor, outboxStore, 3)
		relay := outbox.NewRelay(outboxStore, events.NewDispatcher(), outbox.WithBatchSize(10))
		relay.DrainOnce(ctx)

		var pending int
		So(pool.Get(&pending,
			`SELECT count(*) FROM flexitype_webhook_delivery WHERE status = 'pending'`), ShouldBeNil)
		So(pending, ShouldEqual, len(ids))

		now := time.Now().UTC()

		Convey("When the worker claims due deliveries", func() {
			claimed, err := deliveries.ClaimDue(ctx, 10, time.Minute, now)

			Convey("Then only one is claimed (one inflight per subscription) in feed order", func() {
				So(err, ShouldBeNil)
				So(claimed, ShouldHaveLength, 1)
				So(claimed[0].URL, ShouldEqual, sub.URL)

				var inflight int
				So(pool.Get(&inflight,
					`SELECT count(*) FROM flexitype_webhook_delivery WHERE status = 'inflight'`), ShouldBeNil)
				So(inflight, ShouldEqual, 1)
			})

			Convey("And a second claim returns nothing while the first is inflight", func() {
				again, err := deliveries.ClaimDue(ctx, 10, time.Minute, now)
				So(err, ShouldBeNil)
				So(again, ShouldBeEmpty)
			})

			Convey("And an expired inflight lease is released back to pending and reclaimable", func() {
				released, err := deliveries.ReleaseExpired(ctx, now.Add(2*time.Minute))
				So(err, ShouldBeNil)
				So(released, ShouldEqual, 1)

				reclaimed, err := deliveries.ClaimDue(ctx, 10, time.Minute, now.Add(2*time.Minute))
				So(err, ShouldBeNil)
				So(reclaimed, ShouldHaveLength, 1)
			})
		})

		Convey("When the claimed delivery is recorded delivered, the next one becomes claimable", func() {
			first, err := deliveries.ClaimDue(ctx, 10, time.Minute, now)
			So(err, ShouldBeNil)
			So(first, ShouldHaveLength, 1)

			So(deliveries.Record(ctx, now, webhook.Outcome{
				DeliveryID: first[0].ID, Delivered: true, ResponseCode: 200,
			}), ShouldBeNil)

			second, err := deliveries.ClaimDue(ctx, 10, time.Minute, now)
			So(err, ShouldBeNil)
			So(second, ShouldHaveLength, 1)
			So(second[0].ID, ShouldNotEqual, first[0].ID)
		})
	})
}
