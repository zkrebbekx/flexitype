package flexitype_test

import (
	"context"
	"encoding/json"
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
)

// TestDependencyContextValues covers a rule that depends on a fact the host
// owns.
//
// An embedder anchors flexitype values to its own entities by an opaque
// entity_id, and those entities' primary fields live in host tables — a
// customer's tier, an order's channel, a document's workflow state. A
// condition could only name another flexitype attribute, so a rule that
// depended on one of those had to be expressed by copying the field into
// flexitype and keeping the copy in step: the duplication soft typing exists
// to avoid.
func TestDependencyContextValues(t *testing.T) {
	Convey("Given a rule keyed on the host's customer tier", t, func() {
		base := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(base)

		order, err := ia.TypeDefinitions().Create(base, apptypedef.CreateInput{
			InternalName: "order", DisplayName: "Order",
		})
		So(err, ShouldBeNil)
		attr := func(name string) string {
			a, aerr := svc.Interactors(base).Attributes().Create(base, appattribute.CreateInput{
				TypeDefinitionID: order.ID.String(), InternalName: name,
				DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		note := attr("note")
		poNumber := attr("po_number")

		// "when the caller's customer_tier is enterprise, a PO number is
		// required" — customer_tier is never stored here.
		_, err = svc.Interactors(base).Dependencies().Create(base, appdependency.CreateInput{
			SourceAttributeID: note,
			TargetAttributeID: poNumber,
			Conditions: json.RawMessage(`[{"kind":"equals","context_key":"customer_tier",` +
				`"value":{"type":"string","value":"enterprise"}}]`),
			Effect: json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		raw, _ := json.Marshal("first order")
		_, err = svc.Interactors(base).Values().Set(base, appvalue.SetInput{
			AttributeDefinitionID: note, EntityID: "o1",
			TypeDefinitionID: order.ID.String(), Value: raw,
		})
		So(err, ShouldBeNil)

		required := func(ctx context.Context) bool {
			eff, eerr := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, poNumber, "o1")
			So(eerr, ShouldBeNil)
			return eff.Required
		}

		Convey("When the caller supplies the matching tier", func() {
			ctx := uow.WithContextValues(base, map[string]valueobjects.Value{
				"customer_tier": valueobjects.NewStringValue("enterprise"),
			})

			Convey("Then the rule fires, with nothing about the tier stored here", func() {
				So(required(ctx), ShouldBeTrue)
			})
		})

		Convey("When the caller supplies a different tier", func() {
			ctx := uow.WithContextValues(base, map[string]valueobjects.Value{
				"customer_tier": valueobjects.NewStringValue("self-serve"),
			})

			Convey("Then the rule does not fire", func() {
				So(required(ctx), ShouldBeFalse)
			})
		})

		Convey("When the caller supplies nothing", func() {
			Convey("Then the rule does not fire: an absent fact is not a false one", func() {
				// "Not supplied" and "supplied as the zero value" mean
				// different things, and only the caller knows which happened.
				So(required(base), ShouldBeFalse)
			})
		})

		Convey("When the caller mutates the map afterwards", func() {
			supplied := map[string]valueobjects.Value{
				"customer_tier": valueobjects.NewStringValue("enterprise"),
			}
			ctx := uow.WithContextValues(base, supplied)
			supplied["customer_tier"] = valueobjects.NewStringValue("self-serve")

			Convey("Then evaluation still sees what was stamped", func() {
				So(required(ctx), ShouldBeTrue)
			})
		})
	})
}

// TestTenantTimeZone covers the calendar day a dynamic rule resolves against.
//
// `today` and `now` resolved against UTC and there was no tenant time zone
// anywhere in the domain, so a tenant outside UTC had a date-boundary rule
// that was wrong for part of every day.
func TestTenantTimeZone(t *testing.T) {
	Convey("Given a context with no time zone", t, func() {
		ctx := context.Background()

		Convey("Then it reads as UTC — what the system did before", func() {
			So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "UTC")
			So(uow.HasTimeZone(ctx), ShouldBeFalse)
		})
	})

	Convey("Given a tenant in a zone ahead of UTC", t, func() {
		loc, err := time.LoadLocation("Australia/Adelaide")
		So(err, ShouldBeNil)
		ctx := uow.WithTimeZone(context.Background(), loc)

		Convey("Then the zone is carried and reported as set", func() {
			So(uow.HasTimeZone(ctx), ShouldBeTrue)
			So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "Australia/Adelaide")
		})

		Convey("Then the local clock is the same instant in that zone", func() {
			local := uow.LocalNow(ctx)
			So(local.Location().String(), ShouldEqual, "Australia/Adelaide")
			So(local.Sub(uow.UTCNow()) < time.Second, ShouldBeTrue)
		})

		Convey("Then a date built from it names the LOCAL calendar day", func() {
			// The hours where the two zones disagree are the whole point: a
			// date value is a calendar date, so which day it names has to be
			// the tenant's day.
			atUTCEvening := time.Date(2026, 7, 27, 20, 30, 0, 0, time.UTC)
			So(valueobjects.NewDateValue(atUTCEvening).String(), ShouldEqual, "2026-07-27")
			So(valueobjects.NewDateValue(atUTCEvening.In(loc)).String(), ShouldEqual, "2026-07-28")
		})
	})

	Convey("Given a nil zone", t, func() {
		ctx := uow.WithTimeZone(context.Background(), nil)

		Convey("Then it is ignored rather than stamping nothing usable", func() {
			So(uow.HasTimeZone(ctx), ShouldBeFalse)
			So(uow.TimeZoneFromContext(ctx).String(), ShouldEqual, "UTC")
		})
	})
}
