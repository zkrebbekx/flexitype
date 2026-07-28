package uow

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestContextValuesIsolation covers the copy the stamp makes.
//
// The values are read at evaluation time, which is after the caller has moved
// on. A caller that reuses or mutates its map — a pooled buffer, a loop
// variable — must not be able to change what a later evaluation sees, because
// the result would depend on timing rather than on what was supplied.
func TestContextValuesIsolation(t *testing.T) {
	Convey("Given values stamped onto a context", t, func() {
		supplied := map[string]valueobjects.Value{
			"tier": valueobjects.NewStringValue("enterprise"),
		}
		ctx := WithContextValues(context.Background(), supplied)

		Convey("When the caller mutates its own map afterwards", func() {
			supplied["tier"] = valueobjects.NewStringValue("self-serve")
			delete(supplied, "tier")

			Convey("Then the stamped values are unchanged", func() {
				got := ContextValuesFromContext(ctx)
				So(got["tier"].String(), ShouldEqual, "enterprise")
			})
		})
	})

	Convey("Given an empty set of values", t, func() {
		ctx := WithContextValues(context.Background(), nil)

		Convey("Then nothing is stamped, so an absent key stays absent", func() {
			So(ContextValuesFromContext(ctx), ShouldBeEmpty)
		})
	})

	Convey("Given a context with no values", t, func() {
		Convey("Then reading them is nil rather than a panic", func() {
			So(ContextValuesFromContext(context.Background()), ShouldBeNil)
		})
	})
}

// TestLocalNowMatchesZone pins that the local clock is the same instant, in a
// different zone — not a different instant.
func TestLocalNowMatchesZone(t *testing.T) {
	Convey("Given a tenant zone", t, func() {
		loc, err := time.LoadLocation("America/New_York")
		So(err, ShouldBeNil)
		ctx := WithTimeZone(context.Background(), loc)

		Convey("Then LocalNow is the same moment in that zone", func() {
			local := LocalNow(ctx)
			So(local.Location().String(), ShouldEqual, "America/New_York")
			// Same instant: converting back must land within the sampling gap.
			So(local.UTC().Sub(UTCNow()) < time.Second, ShouldBeTrue)
		})

		Convey("Then UTCNow is unaffected: an instant has no zone", func() {
			So(UTCNow().Location().String(), ShouldEqual, "UTC")
		})
	})
}
