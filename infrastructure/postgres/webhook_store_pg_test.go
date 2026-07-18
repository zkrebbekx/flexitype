package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// errNotDurable is the sentinel a test returns from inside a transaction to
// force a rollback without pretending a real failure occurred.
var errNotDurable = errors.New("rollback for test")

// truncateWebhooks clears every table the webhook suites touch. Deliveries
// reference both subscriptions and outbox envelopes, so all four go together.
func truncateWebhooks(pool *sqlx.DB) {
	pool.MustExec(`TRUNCATE flexitype_webhook_delivery, flexitype_webhook_subscription,
		flexitype_event_outbox CASCADE`)
}

func newSubscription(tenant, name string, active bool, types []string) webhook.Subscription {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return webhook.Subscription{
		ID: ulid.New(), TenantID: valueobjects.TenantID(tenant), Name: name,
		URL: "https://example.test/" + name, Secret: "sec-" + name,
		EventTypes: types, Active: active, CreatedAt: now, UpdatedAt: now,
	}
}

func TestSubscriptionStoreIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	store := postgres.NewSubscriptionStore(pool)

	Convey("Given subscriptions across two tenants, one inactive", t, func() {
		truncateWebhooks(pool)
		zebra := newSubscription("default", "zebra", true, []string{"flexitype.entity.created"})
		apple := newSubscription("default", "apple", true, nil)
		dormant := newSubscription("default", "dormant", false, nil)
		foreign := newSubscription("other", "outsider", true, nil)
		for _, s := range []webhook.Subscription{zebra, apple, dormant, foreign} {
			So(store.Create(ctx, s), ShouldBeNil)
		}

		Convey("When a subscription is fetched by id", func() {
			got, err := store.Get(ctx, "default", zebra.ID)

			Convey("Then every field round-trips, including its event-type filter", func() {
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "zebra")
				So(got.URL, ShouldEqual, "https://example.test/zebra")
				So(got.Secret, ShouldEqual, "sec-zebra")
				So(got.Active, ShouldBeTrue)
				So(got.EventTypes, ShouldResemble, []string{"flexitype.entity.created"})
			})
		})

		Convey("When a subscription created with no event types is fetched", func() {
			got, err := store.Get(ctx, "default", apple.ID)

			Convey("Then the nil slice round-trips as an empty array, not NULL", func() {
				So(err, ShouldBeNil)
				So(got.EventTypes, ShouldBeEmpty)
			})
		})

		Convey("When another tenant's subscription is fetched by id", func() {
			_, err := store.Get(ctx, "default", foreign.ID)

			Convey("Then the tenant predicate hides it behind a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, foreign.ID.String())
			})
		})

		Convey("When a subscription is fetched by name", func() {
			got, err := store.GetByName(ctx, "default", "apple")

			Convey("Then the right row is returned", func() {
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, apple.ID)
			})
		})

		Convey("When an unknown name is fetched", func() {
			_, err := store.GetByName(ctx, "default", "ghost")

			Convey("Then a not-found error names the subscription", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "ghost")
			})
		})

		Convey("When a name that exists only on another tenant is fetched", func() {
			_, err := store.GetByName(ctx, "default", "outsider")

			Convey("Then it stays invisible across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When one tenant's subscriptions are listed", func() {
			subs, err := store.List(ctx, "default")

			Convey("Then only its subscriptions return by name, active and inactive alike", func() {
				So(err, ShouldBeNil)
				So(subs, ShouldHaveLength, 3)
				So(subs[0].Name, ShouldEqual, "apple")
				So(subs[1].Name, ShouldEqual, "dormant")
				So(subs[2].Name, ShouldEqual, "zebra")
			})
		})

		Convey("When the relay lists active subscriptions across all tenants", func() {
			subs, err := store.ListActive(ctx)

			Convey("Then inactive subscriptions are excluded but tenants are not", func() {
				So(err, ShouldBeNil)
				names := map[string]string{}
				for _, s := range subs {
					names[s.Name] = s.TenantID.String()
				}
				So(names, ShouldContainKey, "zebra")
				So(names, ShouldContainKey, "apple")
				So(names, ShouldContainKey, "outsider")
				So(names, ShouldNotContainKey, "dormant")
				So(names["outsider"], ShouldEqual, "other")
			})
		})

		Convey("When a subscription is updated", func() {
			updated := zebra
			updated.URL = "https://example.test/moved"
			updated.Secret = "rotated"
			updated.PreviousSecret = "sec-zebra"
			updated.EventTypes = []string{"a", "b"}
			updated.Active = false
			updated.UpdatedAt = zebra.UpdatedAt.Add(time.Hour)
			So(store.Update(ctx, updated), ShouldBeNil)

			Convey("Then the mutable fields change and the immutable name does not", func() {
				got, err := store.Get(ctx, "default", zebra.ID)
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "zebra")
				So(got.URL, ShouldEqual, "https://example.test/moved")
				So(got.Secret, ShouldEqual, "rotated")
				So(got.PreviousSecret, ShouldEqual, "sec-zebra")
				So(got.EventTypes, ShouldResemble, []string{"a", "b"})
				So(got.Active, ShouldBeFalse)
				So(got.UpdatedAt.UTC(), ShouldHappenWithin, time.Second, updated.UpdatedAt)
			})

			Convey("And it drops out of the active listing", func() {
				subs, err := store.ListActive(ctx)
				So(err, ShouldBeNil)
				for _, s := range subs {
					So(s.ID, ShouldNotEqual, zebra.ID)
				}
			})
		})

		Convey("When an update names the wrong tenant", func() {
			stray := zebra
			stray.TenantID = "other"
			stray.URL = "https://example.test/hijacked"
			So(store.Update(ctx, stray), ShouldBeNil)

			Convey("Then the tenant predicate leaves the row untouched", func() {
				got, err := store.Get(ctx, "default", zebra.ID)
				So(err, ShouldBeNil)
				So(got.URL, ShouldEqual, "https://example.test/zebra")
			})
		})

		Convey("When a subscription is deleted", func() {
			So(store.Delete(ctx, "default", zebra.ID), ShouldBeNil)

			Convey("Then it is gone and its siblings survive", func() {
				_, err := store.Get(ctx, "default", zebra.ID)
				So(domainerrors.IsNotFound(err), ShouldBeTrue)

				remaining, listErr := store.List(ctx, "default")
				So(listErr, ShouldBeNil)
				So(remaining, ShouldHaveLength, 2)
			})
		})

		Convey("When a delete names the wrong tenant", func() {
			So(store.Delete(ctx, "other", zebra.ID), ShouldBeNil)

			Convey("Then the row is protected by the tenant predicate", func() {
				got, err := store.Get(ctx, "default", zebra.ID)
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, zebra.ID)
			})
		})

		Convey("When a duplicate name is created for the same tenant", func() {
			dup := newSubscription("default", "zebra", true, nil)
			err := store.Create(ctx, dup)

			Convey("Then the (tenant, name) uniqueness constraint rejects it", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "insert subscription")
			})
		})

		Convey("When the same name is created for a different tenant", func() {
			twin := newSubscription("other", "zebra", true, nil)

			Convey("Then uniqueness is scoped per tenant and the insert succeeds", func() {
				So(store.Create(ctx, twin), ShouldBeNil)
				got, err := store.GetByName(ctx, "other", "zebra")
				So(err, ShouldBeNil)
				So(got.ID, ShouldEqual, twin.ID)
			})
		})
	})

	Convey("Given a store bound to an open transaction", t, func() {
		truncateWebhooks(pool)
		sub := newSubscription("default", "txbound", true, nil)

		Convey("When a subscription is created inside a transaction that rolls back", func() {
			boom := errNotDurable
			err := transactor.InTransaction(ctx, func(tx db.Transactor) error {
				txStore := store.WithTx(tx)
				if createErr := txStore.Create(ctx, sub); createErr != nil {
					return createErr
				}
				// Visible to the transaction that wrote it...
				inside, getErr := txStore.Get(ctx, "default", sub.ID)
				So(getErr, ShouldBeNil)
				So(inside.Name, ShouldEqual, "txbound")
				return boom
			})

			Convey("Then the write is discarded with the transaction", func() {
				So(err, ShouldEqual, boom)
				_, getErr := store.Get(ctx, "default", sub.ID)
				So(domainerrors.IsNotFound(getErr), ShouldBeTrue)
			})
		})

		Convey("When a subscription is created inside a transaction that commits", func() {
			So(transactor.InTransaction(ctx, func(tx db.Transactor) error {
				return store.WithTx(tx).Create(ctx, sub)
			}), ShouldBeNil)

			Convey("Then the write is durable on the pool", func() {
				got, err := store.Get(ctx, "default", sub.ID)
				So(err, ShouldBeNil)
				So(got.Name, ShouldEqual, "txbound")
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewSubscriptionStore(closed)
		sub := newSubscription("default", "unreachable", true, nil)

		Convey("When each subscription operation runs", func() {
			_, getErr := broken.Get(ctx, "default", sub.ID)
			_, nameErr := broken.GetByName(ctx, "default", "unreachable")
			_, listErr := broken.List(ctx, "default")
			_, activeErr := broken.ListActive(ctx)
			createErr := broken.Create(ctx, sub)
			updateErr := broken.Update(ctx, sub)
			deleteErr := broken.Delete(ctx, "default", sub.ID)

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(getErr.Error(), ShouldContainSubstring, "get subscription")
				So(nameErr.Error(), ShouldContainSubstring, "get subscription by name")
				So(listErr.Error(), ShouldContainSubstring, "list subscriptions")
				So(activeErr.Error(), ShouldContainSubstring, "list active subscriptions")
				So(createErr.Error(), ShouldContainSubstring, "insert subscription")
				So(updateErr.Error(), ShouldContainSubstring, "update subscription")
				So(deleteErr.Error(), ShouldContainSubstring, "delete subscription")
			})
		})
	})
}

func TestDeliveryStoreListIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	outboxStore := postgres.NewOutboxStore(transactor)
	subs := postgres.NewSubscriptionStore(pool)
	deliveries := postgres.NewDeliveryStore(pool)

	Convey("Given deliveries in mixed states across two subscriptions and two tenants", t, func() {
		truncateWebhooks(pool)
		subA := newSubscription("default", "sub-a", true, nil)
		subB := newSubscription("default", "sub-b", true, nil)
		subOther := newSubscription("other", "sub-c", true, nil)
		for _, s := range []webhook.Subscription{subA, subB, subOther} {
			So(subs.Create(ctx, s), ShouldBeNil)
		}
		envelopes := writeEnvelopesFor(ctx, transactor, outboxStore, 6, "default", "flexitype.entity.created")
		foreignEnvelopes := writeEnvelopesFor(ctx, transactor, outboxStore, 1, "other", "flexitype.entity.created")

		// Ids are ULIDs generated in order, so the DESC listing reverses
		// insertion order; keep the created ids to assert exactly that.
		aPending := insertDelivery(pool, subA.ID, envelopes[0], "default", "pending", 1)
		aDelivered := insertDelivery(pool, subA.ID, envelopes[1], "default", "delivered", 2)
		aDead := insertDelivery(pool, subA.ID, envelopes[2], "default", "dead", 3)
		bPending := insertDelivery(pool, subB.ID, envelopes[3], "default", "pending", 4)
		bDelivered := insertDelivery(pool, subB.ID, envelopes[4], "default", "delivered", 5)
		otherTenant := insertDelivery(pool, subOther.ID, foreignEnvelopes[0], "other", "pending", 6)

		Convey("When a tenant's deliveries are listed with a total", func() {
			got, total, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then only that tenant's deliveries return newest-id-first with the full count", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 5)
				So(got, ShouldHaveLength, 5)
				ids := make([]ulid.ID, len(got))
				for i, d := range got {
					ids[i] = d.ID
					So(d.TenantID, ShouldEqual, "default")
					So(d.ID, ShouldNotEqual, otherTenant)
				}
				So(ids, ShouldResemble, []ulid.ID{bDelivered, bPending, aDead, aDelivered, aPending})
			})
		})

		Convey("When deliveries are filtered by subscription", func() {
			got, total, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default", SubscriptionID: subA.ID},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then only that subscription's deliveries return and the total narrows", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 3)
				So(got, ShouldHaveLength, 3)
				for _, d := range got {
					So(d.SubscriptionID, ShouldEqual, subA.ID)
				}
			})
		})

		Convey("When deliveries are filtered by status", func() {
			got, total, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default", Status: webhook.StatusDelivered},
				db.Page{Limit: 10, WantTotal: true})

			Convey("Then only delivered rows return", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 2)
				So(got, ShouldHaveLength, 2)
				for _, d := range got {
					So(d.Status, ShouldEqual, webhook.StatusDelivered)
				}
			})
		})

		Convey("When subscription and status filters are combined", func() {
			got, total, err := deliveries.List(ctx,
				webhook.DeliveryFilter{
					TenantID: "default", SubscriptionID: subA.ID, Status: webhook.StatusDead,
				}, db.Page{Limit: 10, WantTotal: true})

			Convey("Then both predicates apply", func() {
				So(err, ShouldBeNil)
				So(total, ShouldEqual, 1)
				So(got, ShouldHaveLength, 1)
				So(got[0].ID, ShouldEqual, aDead)
			})
		})

		Convey("When no total is requested", func() {
			got, total, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default"}, db.Page{Limit: 10})

			Convey("Then the rows come back and the count query is skipped", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 5)
				So(total, ShouldEqual, 0)
			})
		})

		Convey("When the listing is paged with a keyset cursor", func() {
			// FetchLimit is Limit+1, so a limit of 2 returns 3 rows and the
			// caller cuts the lookahead off before minting a cursor.
			first, _, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default"}, db.Page{Limit: 2})
			So(err, ShouldBeNil)
			So(first, ShouldHaveLength, 3)
			cursor := db.EncodeKeyset(first[1].ID.String())
			second, _, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default"}, db.Page{Limit: 2, Cursor: cursor})

			Convey("Then the next page continues strictly after the cursor with no overlap", func() {
				So(err, ShouldBeNil)
				So(second, ShouldNotBeEmpty)
				So(second[0].ID, ShouldEqual, aDead)
				for _, d := range second {
					So(d.ID.String(), ShouldBeLessThan, first[1].ID.String())
				}
			})
		})

		Convey("When the full delivery row is read back", func() {
			got, _, err := deliveries.List(ctx,
				webhook.DeliveryFilter{TenantID: "default", SubscriptionID: subA.ID, Status: "pending"},
				db.Page{Limit: 10})

			Convey("Then its scalar columns carry through the row mapping", func() {
				So(err, ShouldBeNil)
				So(got, ShouldHaveLength, 1)
				d := got[0]
				So(d.ID, ShouldEqual, aPending)
				So(d.EnvelopeID, ShouldEqual, envelopes[0])
				So(d.EventType, ShouldEqual, "flexitype.entity.created")
				So(d.FeedSeq, ShouldEqual, int64(1))
				So(d.Attempts, ShouldEqual, 0)
				So(d.LastError, ShouldBeBlank)
				So(d.ResponseCode, ShouldEqual, 0)
				So(d.NextAttemptAt.IsZero(), ShouldBeFalse)
			})
		})
	})
}

func TestDeliveryStoreRedeliverIntegration(t *testing.T) {
	pool, transactor := controlPlaneFixture(t)
	ctx := context.Background()
	outboxStore := postgres.NewOutboxStore(transactor)
	subs := postgres.NewSubscriptionStore(pool)
	deliveries := postgres.NewDeliveryStore(pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	Convey("Given one delivery in each terminal and each in-flight state", t, func() {
		truncateWebhooks(pool)
		sub := newSubscription("default", "sub-a", true, nil)
		So(subs.Create(ctx, sub), ShouldBeNil)
		envelopes := writeEnvelopesFor(ctx, transactor, outboxStore, 4, "default", "flexitype.entity.created")
		dead := insertDelivery(pool, sub.ID, envelopes[0], "default", "dead", 1)
		delivered := insertDelivery(pool, sub.ID, envelopes[1], "default", "delivered", 2)
		pending := insertDelivery(pool, sub.ID, envelopes[2], "default", "pending", 3)
		inflight := insertDelivery(pool, sub.ID, envelopes[3], "default", "inflight", 4)
		pool.MustExec(`UPDATE flexitype_webhook_delivery SET lease_expires_at = now() WHERE id = $1`,
			inflight.String())

		statusOf := func(id ulid.ID) string {
			var s string
			So(pool.Get(&s, `SELECT status FROM flexitype_webhook_delivery WHERE id = $1`, id.String()), ShouldBeNil)
			return s
		}

		Convey("When a dead delivery is requeued", func() {
			err := deliveries.Redeliver(ctx, "default", dead, now)

			Convey("Then it returns to pending with a fresh attempt time and no lease", func() {
				So(err, ShouldBeNil)
				So(statusOf(dead), ShouldEqual, webhook.StatusPending)

				var lease *time.Time
				var next time.Time
				So(pool.Get(&lease,
					`SELECT lease_expires_at FROM flexitype_webhook_delivery WHERE id = $1`,
					dead.String()), ShouldBeNil)
				So(lease, ShouldBeNil)
				So(pool.Get(&next,
					`SELECT next_attempt_at FROM flexitype_webhook_delivery WHERE id = $1`,
					dead.String()), ShouldBeNil)
				So(next.UTC(), ShouldHappenWithin, time.Second, now)
			})
		})

		Convey("When a delivered delivery is requeued", func() {
			err := deliveries.Redeliver(ctx, "default", delivered, now)

			Convey("Then replaying a successful delivery is allowed", func() {
				So(err, ShouldBeNil)
				So(statusOf(delivered), ShouldEqual, webhook.StatusPending)
			})
		})

		Convey("When an already-pending delivery is requeued", func() {
			err := deliveries.Redeliver(ctx, "default", pending, now)

			Convey("Then it is refused as a conflict and left untouched", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "already queued")
				So(statusOf(pending), ShouldEqual, webhook.StatusPending)
			})
		})

		Convey("When an inflight delivery is requeued", func() {
			err := deliveries.Redeliver(ctx, "default", inflight, now)

			Convey("Then the in-flight worker is not undercut", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(statusOf(inflight), ShouldEqual, webhook.StatusInflight)
			})
		})

		Convey("When an unknown delivery is requeued", func() {
			missing := ulid.New()
			err := deliveries.Redeliver(ctx, "default", missing, now)

			Convey("Then a not-found error names the delivery", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, missing.String())
			})
		})

		Convey("When another tenant tries to requeue the delivery", func() {
			err := deliveries.Redeliver(ctx, "other", dead, now)

			Convey("Then the tenant predicate hides it behind a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				So(statusOf(dead), ShouldEqual, webhook.StatusDead)
			})
		})
	})

	Convey("Given a closed pool", t, func() {
		closed := openIntegrationDB(t)
		So(closed.Close(), ShouldBeNil)
		broken := postgres.NewDeliveryStore(closed)

		Convey("When deliveries are listed and requeued", func() {
			_, _, listErr := broken.List(ctx,
				webhook.DeliveryFilter{TenantID: "default"}, db.Page{Limit: 10, WantTotal: true})
			redeliverErr := broken.Redeliver(ctx, "default", ulid.New(), now)

			Convey("Then each wraps the driver failure with its own operation", func() {
				So(listErr, ShouldNotBeNil)
				So(listErr.Error(), ShouldContainSubstring, "list deliveries")
				So(redeliverErr, ShouldNotBeNil)
				So(redeliverErr.Error(), ShouldContainSubstring, "load delivery")
			})
		})
	})
}
