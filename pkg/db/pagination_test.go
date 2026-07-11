package db

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParsePageArgs(t *testing.T) {
	Convey("Given raw pagination query strings", t, func() {
		Convey("A non-integer limit defers a validation error to Resolve", func() {
			_, err := ParsePageArgs("abc", "").Resolve()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "limit must be an integer")
		})

		Convey("An out-of-range limit clamps rather than errors", func() {
			page, err := ParsePageArgs("9999", "").Resolve()
			So(err, ShouldBeNil)
			So(page.Limit, ShouldEqual, maxPageSize)

			page, err = ParsePageArgs("0", "").Resolve()
			So(err, ShouldBeNil)
			So(page.Limit, ShouldEqual, defaultPageSize)
		})

		Convey("An empty limit uses the default", func() {
			page, err := ParsePageArgs("", "").Resolve()
			So(err, ShouldBeNil)
			So(page.Limit, ShouldEqual, defaultPageSize)
			So(page.Offset, ShouldEqual, 0)
		})

		Convey("A cursor round-trips through offset encoding", func() {
			cursor := EncodeCursor(40)
			page, err := ParsePageArgs("10", cursor).Resolve()
			So(err, ShouldBeNil)
			So(page.Offset, ShouldEqual, 40)
			So(page.Limit, ShouldEqual, 10)
		})

		Convey("A malformed cursor errors at Resolve", func() {
			_, err := ParsePageArgs("10", "not-base64!!").Resolve()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "cursor")
		})
	})
}
