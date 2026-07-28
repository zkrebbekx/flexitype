package value

import (
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestObserveCleanup covers the report a swallowed media-GC failure makes.
//
// Blob deletion is best effort on purpose: a storage hiccup must not fail the
// surrounding write, because the value is already committed and failing the
// request would tell the caller their write did not happen when it did. The
// cost is that the failure is invisible unless something reports it — which
// is what the observer is for. Without one it stays best effort and silent,
// which is the pre-existing behaviour an embedder gets by wiring nothing.
func TestObserveCleanup(t *testing.T) {
	Convey("Given an interactor with no cleanup observer", t, func() {
		i := &Interactor{}

		Convey("Then reporting a failure is a no-op rather than a panic", func() {
			So(func() { i.observeCleanup(errors.New("blob store unreachable")) }, ShouldNotPanic)
		})
	})

	Convey("Given an interactor with a cleanup observer", t, func() {
		var seen []error
		i := &Interactor{onCleanupError: func(err error) { seen = append(seen, err) }}

		Convey("When a cleanup fails", func() {
			boom := errors.New("blob store unreachable")
			i.observeCleanup(boom)

			Convey("Then the observer sees the cause", func() {
				So(seen, ShouldHaveLength, 1)
				So(errors.Is(seen[0], boom), ShouldBeTrue)
			})
		})
	})
}

// TestQuantityValueRefusals covers the ways a quantity write is refused
// rather than stored in a form nothing can compare.
//
// A quantity is stored against its family's base unit, so a value that cannot
// be rebased has no meaning: it would compare against every bound and match
// none of them. Each refusal names what is missing, because "invalid value"
// tells a schema author nothing about which of the three things is wrong.
func TestQuantityValueRefusals(t *testing.T) {
	Convey("Given a deployment with no unit families configured", t, func() {
		i := &Interactor{}

		Convey("Then a quantity write is refused, naming the missing feature", func() {
			_, err := i.quantityValue(t.Context(), nil, nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "unit families are not configured")
		})
	})
}
