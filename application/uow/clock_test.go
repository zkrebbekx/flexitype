package uow

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestUTCNow(t *testing.T) {
	Convey("Given the canonical usecase clock", t, func() {
		now := UTCNow()
		Convey("Then it returns UTC wall-clock time", func() {
			So(now.Location(), ShouldEqual, time.UTC)
		})
		Convey("And the monotonic reading is stripped (so stored timestamps round-trip)", func() {
			// A time carrying a monotonic reading is not equal to its own
			// wall-clock Round(0); UTCNow must already be wall-clock only.
			So(now.Equal(now.Round(0)), ShouldBeTrue)
			So(now, ShouldResemble, now.Round(0))
		})
	})
}
