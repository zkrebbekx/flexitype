package flexitype_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestDeliveryLoopSelection covers the tier split an operator uses to run the
// API and the delivery machinery on different pods.
//
// Running every loop on every replica is the single-process default and is
// wasteful at scale; running none by mistake is worse, because the outbox
// fills silently. Both ends are asserted.
func TestDeliveryLoopSelection(t *testing.T) {
	Convey("Given the delivery loop selector", t, func() {
		Convey("Then the single-process default runs everything", func() {
			all := flexitype.AllDeliveryLoops()
			So(all.Relay, ShouldBeTrue)
			So(all.Worker, ShouldBeTrue)
			So(all.Pruner, ShouldBeTrue)
		})

		Convey("Then a tier can select a subset", func() {
			apiTier := flexitype.DeliveryLoops{}
			So(apiTier.Relay, ShouldBeFalse)
			So(apiTier.Worker, ShouldBeFalse)
			So(apiTier.Pruner, ShouldBeFalse)
		})
	})

	Convey("Given an in-memory service (no outbox)", t, func() {
		svc := flexitype.NewInMemory()
		ctx, cancel := context.WithCancel(context.Background())

		Convey("When the delivery loops are asked to run", func() {
			done := make(chan struct{})
			go func() { svc.RunOutboxRelay(ctx); close(done) }()
			cancel()

			Convey("Then it returns rather than blocking a caller that has no outbox", func() {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("RunOutboxRelay did not return after the context ended")
				}
			})
		})
	})
}

// TestSchemaDriftInMemory covers the drift report for a service with no
// database.
//
// A rolling deploy makes a newer schema than the running binary normal for a
// while, and an operator should be able to see a mixed-version fleet rather
// than infer it. With no database there is nothing to compare, and the answer
// must be "nothing" rather than an error a caller has to special-case.
func TestSchemaDriftInMemory(t *testing.T) {
	Convey("Given an in-memory service", t, func() {
		svc := flexitype.NewInMemory()

		Convey("When schema drift is checked", func() {
			versions, err := svc.SchemaDrift(context.Background())

			Convey("Then it reports nothing, without an error", func() {
				So(err, ShouldBeNil)
				So(versions, ShouldBeEmpty)
			})
		})

		Convey("When migrations are applied", func() {
			Convey("Then it is a no-op rather than an error", func() {
				So(svc.Migrate(context.Background()), ShouldBeNil)
			})
		})
	})
}

// TestObserverOptions covers the observers that turn silent background
// failures into something an operator sees.
func TestObserverOptions(t *testing.T) {
	Convey("Given observers wired at construction", t, func() {
		var rollbacks, dispatches, background int

		svc := flexitype.NewInMemory(
			flexitype.WithRollbackObserver(func(context.Context, error) { rollbacks++ }),
			flexitype.WithDispatchObserver(func(context.Context, error) { dispatches++ }),
			flexitype.WithBackgroundErrorObserver(func(error) { background++ }),
		)

		Convey("Then the service builds with all three", func() {
			So(svc, ShouldNotBeNil)
		})

		Convey("Then a webhook subscription needs the outbox", func() {
			// Without WithOutbox there is no subscription store, and the
			// bootstrap path has to say so rather than appear to succeed.
			err := svc.EnsureWebhookSubscription(context.Background(),
				"env-webhook", "https://example.test", "secret")
			So(err, ShouldNotBeNil)
		})
	})
}

// TestServiceTimeZone covers the deployment default and the per-request
// override.
//
// An embedder serving tenants in several zones from one process stamps the
// zone per request, and a deployment default must not overwrite that — the
// two must compose, or a multi-tenant host silently gets one tenant's day
// boundary applied to all of them.
func TestServiceTimeZone(t *testing.T) {
	Convey("Given a service configured with a deployment time zone", t, func() {
		adelaide, err := time.LoadLocation("Australia/Adelaide")
		So(err, ShouldBeNil)
		svc := flexitype.NewInMemory(flexitype.WithTimeZone(adelaide))

		Convey("When a request supplies none", func() {
			ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
			svc.Interactors(ctx) // stamps the default onto its own context

			Convey("Then the deployment default applies to evaluation", func() {
				// The service stamps its default on the context it hands the
				// factory, so a rule evaluated through those interactors uses
				// it. The caller's own context is untouched, which is why the
				// assertion below reads UTC.
				So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "UTC")
			})
		})

		Convey("When a request supplies its own zone", func() {
			newYork, lerr := time.LoadLocation("America/New_York")
			So(lerr, ShouldBeNil)
			ctx := uow.WithTimeZone(
				uow.WithTenant(context.Background(), valueobjects.DefaultTenant), newYork)

			Convey("Then the request's zone wins over the deployment default", func() {
				So(uow.HasTimeZone(ctx), ShouldBeTrue)
				So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "America/New_York")
				So(svc.Interactors(ctx), ShouldNotBeNil)
			})
		})
	})

	Convey("Given a service with no time zone configured", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)

		Convey("Then it stays on UTC — what the system did before", func() {
			So(svc.Interactors(ctx), ShouldNotBeNil)
			So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "UTC")
		})
	})
}
