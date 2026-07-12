package db

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }

func TestPageArgsResolve(t *testing.T) {
	Convey("Given raw client pagination args", t, func() {
		Convey("When no limit is given", func() {
			p, err := PageArgs{}.Resolve()
			Convey("Then the default page size is used", func() {
				So(err, ShouldBeNil)
				So(p.Limit, ShouldEqual, defaultPageSize)
			})
		})

		Convey("When the limit is a positive number within range", func() {
			p, err := PageArgs{Limit: intp(25)}.Resolve()
			Convey("Then it is honored", func() {
				So(err, ShouldBeNil)
				So(p.Limit, ShouldEqual, 25)
			})
		})

		Convey("When the limit exceeds the maximum", func() {
			p, err := PageArgs{Limit: intp(9999)}.Resolve()
			Convey("Then it is clamped to the maximum", func() {
				So(err, ShouldBeNil)
				So(p.Limit, ShouldEqual, maxPageSize)
			})
		})

		Convey("When the limit is zero or negative (including a non-numeric sentinel)", func() {
			_, errZero := PageArgs{Limit: intp(0)}.Resolve()
			_, errNeg := PageArgs{Limit: intp(-3)}.Resolve()
			Convey("Then it is rejected rather than silently defaulted", func() {
				So(errZero, ShouldNotBeNil)
				So(errNeg, ShouldNotBeNil)
			})
		})

		Convey("When the cursor is malformed", func() {
			_, err := PageArgs{Cursor: strp("not-a-real-cursor")}.Resolve()
			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the cursor is a valid offset cursor", func() {
			p, err := PageArgs{Cursor: strp(EncodeCursor(40))}.Resolve()
			Convey("Then the offset is recovered", func() {
				So(err, ShouldBeNil)
				So(p.Offset, ShouldEqual, 40)
			})
		})
	})
}
