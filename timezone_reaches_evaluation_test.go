package flexitype_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// evalInstant is the pinned instant every assertion resolves against. At
// 20:00 UTC the Etc/GMT-14 zone (UTC+14) is at 10:00 on the NEXT day, so the
// UTC day and the local day always differ. A wall-clock instant only gave
// that difference for part of the day: before 10:00 UTC the two zones share a
// date, and the test failed there. See TestTimeZoneReachesEvaluation.
var evalInstant = time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)

func evalClock() time.Time { return evalInstant }

// zoneFixture is a type whose `channel` attribute becomes required as soon as
// `due_on` is on or after TODAY — a date-boundary rule, and the configured
// zone is what decides which day that is.
type zoneFixture struct {
	typeID  string
	dueOn   string
	channel string
}

func seedZoneFixture(ctx context.Context, svc *flexitype.Service) zoneFixture {
	it := svc.Interactors(ctx)
	td, err := it.TypeDefinitions().Create(ctx,
		apptypedef.CreateInput{InternalName: "order", DisplayName: "Order"})
	So(err, ShouldBeNil)
	dueOn, err := it.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: td.ID.String(), InternalName: "due_on",
		DisplayName: "Due on", DataType: "date",
	})
	So(err, ShouldBeNil)
	channel, err := it.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: td.ID.String(), InternalName: "channel",
		DisplayName: "Channel", DataType: "string",
	})
	So(err, ShouldBeNil)
	_, err = it.Dependencies().Create(ctx, appdependency.CreateInput{
		SourceAttributeID: dueOn.ID.String(),
		TargetAttributeID: channel.ID.String(),
		Conditions:        json.RawMessage(`[{"kind":"dynamic","op":"on_or_after","dynamic":{"kind":"today"}}]`),
		Effect:            json.RawMessage(`{"required":true}`),
	})
	So(err, ShouldBeNil)

	// The entity's due date is the pinned instant's UTC day.
	raw, merr := json.Marshal(valueobjects.NewDateValue(evalInstant).String())
	So(merr, ShouldBeNil)
	_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
		AttributeDefinitionID: dueOn.ID.String(), EntityID: "o1",
		TypeDefinitionID: td.ID.String(), Value: raw,
	})
	So(serr, ShouldBeNil)

	return zoneFixture{typeID: td.ID.String(), dueOn: dueOn.ID.String(), channel: channel.ID.String()}
}

// TestTimeZoneReachesEvaluation proves the configured zone decides the day a
// `today` rule fires on, through both supported entry points.
//
// Service.Interactors stamped the zone onto a context it then DISCARDED, and
// the HTTP middleware never stamped it at all. An interactor set carries no
// context of its own — every method takes one from its caller — so
// FLEXITYPE_TIMEZONE reached nothing and every `today`/`now` rule and dynamic
// default resolved in UTC. The read and write paths agreed only because both
// were wrong, which removed the contradiction that would have exposed it.
//
// The clock is pinned to 20:00 UTC (WithClock), where Etc/GMT-14 (UTC+14) is
// already on the NEXT day. So `due_on on_or_after today` over an entity dated
// on the pinned UTC day is true in UTC and false in the zone — a constant,
// not a function of when the test runs. The wall clock gave the opposite
// answer before 10:00 UTC, when the two zones share a date.
func TestTimeZoneReachesEvaluation(t *testing.T) {
	ahead, err := time.LoadLocation("Etc/GMT-14")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}

	Convey("Given a service configured 14 hours ahead of UTC with a pinned clock", t, func() {
		svc := flexitype.NewInMemory(flexitype.WithTimeZone(ahead), flexitype.WithClock(evalClock))
		// The bare context carries the pinned clock but NOT the zone: the
		// zone's absence is the behaviour under test, the clock's presence
		// only removes the wall clock from the outcome.
		plain := uow.WithClock(uow.WithTenant(context.Background(), valueobjects.DefaultTenant), evalClock)
		fx := seedZoneFixture(plain, svc)

		Convey("When the caller passes a bare context", func() {
			eff, eerr := svc.Interactors(plain).Dependencies().EffectiveSchema(plain, fx.channel, "o1")

			Convey("Then the rule resolves on the UTC day", func() {
				So(eerr, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
			})
		})

		Convey("When the caller passes its context through Service.Context", func() {
			ctx := svc.Context(plain)
			eff, eerr := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, fx.channel, "o1")

			Convey("Then the zone is on the context each method receives", func() {
				So(uow.HasTimeZone(ctx), ShouldBeTrue)
				So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "Etc/GMT-14")
			})

			Convey("Then the rule resolves against the configured day, not UTC", func() {
				So(eerr, ShouldBeNil)
				So(eff.Required, ShouldBeFalse)
			})
		})

		Convey("When a request supplies its own zone", func() {
			newYork, lerr := time.LoadLocation("America/New_York")
			So(lerr, ShouldBeNil)
			ctx := svc.Context(uow.WithTimeZone(plain, newYork))

			Convey("Then the request's zone wins over the deployment default", func() {
				So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "America/New_York")
			})
		})
	})

	Convey("Given the standalone API configured in that zone with a pinned clock", t, func() {
		svc := flexitype.NewInMemory(flexitype.WithTimeZone(ahead), flexitype.WithClock(evalClock))
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		fx := seedZoneFixture(ctx, svc)

		handler, herr := svc.NewAPIHandler(flexitype.APIConfig{
			AllowAnonymous: true, Logger: logger.New(logger.Config{Level: "error"}),
		})
		So(herr, ShouldBeNil)
		srv := httptest.NewServer(handler)
		defer srv.Close()

		Convey("When the effective schema is read over HTTP", func() {
			resp, gerr := srv.Client().Get(srv.URL + "/api/v1/entities/" + fx.typeID +
				"/o1/attributes/" + fx.channel + "/effective-schema")
			So(gerr, ShouldBeNil)
			defer func() { _ = resp.Body.Close() }()
			var out struct {
				Required bool `json:"required"`
			}
			So(json.NewDecoder(resp.Body).Decode(&out), ShouldBeNil)

			Convey("Then the middleware stamped the zone, so the rule reads the local day", func() {
				So(resp.StatusCode, ShouldEqual, http.StatusOK)
				So(out.Required, ShouldBeFalse)
			})
		})
	})
}
