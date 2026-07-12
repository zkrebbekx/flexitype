package db

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func intp(n int) *int { return &n }

func TestPageArgsResolve(t *testing.T) {
	Convey("Given raw client pagination args", t, func() {
		Convey("When no limit is given", func() {
			p, err := PageArgs{}.Resolve()
			Convey("Then the default page size is used", func() {
				So(err, ShouldBeNil)
				So(p.Limit, ShouldEqual, defaultPageSize)
			})
		})

		Convey("When the limit exceeds the maximum", func() {
			p, err := PageArgs{Limit: intp(9999)}.Resolve()
			Convey("Then it is clamped", func() {
				So(err, ShouldBeNil)
				So(p.Limit, ShouldEqual, maxPageSize)
			})
		})

		Convey("When the limit is zero or negative", func() {
			_, errZero := PageArgs{Limit: intp(0)}.Resolve()
			_, errNeg := PageArgs{Limit: intp(-3)}.Resolve()
			Convey("Then it is rejected rather than silently defaulted", func() {
				So(errZero, ShouldNotBeNil)
				So(errNeg, ShouldNotBeNil)
			})
		})

		Convey("When total is requested", func() {
			p, err := PageArgs{WantTotal: true}.Resolve()
			Convey("Then the page carries WantTotal", func() {
				So(err, ShouldBeNil)
				So(p.WantTotal, ShouldBeTrue)
			})
		})
	})
}

func TestKeyset(t *testing.T) {
	Convey("Given a single ascending id ordering", t, func() {
		cols := []KeysetColumn{{Expr: "id"}}

		Convey("When there is no cursor", func() {
			pred, args, err := KeysetPredicate(cols, "")
			Convey("Then no predicate is produced (first page)", func() {
				So(err, ShouldBeNil)
				So(pred, ShouldEqual, "")
				So(args, ShouldBeEmpty)
			})
		})

		Convey("When a cursor encodes the last id", func() {
			cursor := EncodeKeyset("abc")
			pred, args, err := KeysetPredicate(cols, cursor)
			Convey("Then it selects rows after that id", func() {
				So(err, ShouldBeNil)
				So(pred, ShouldContainSubstring, "id > ?")
				So(args, ShouldResemble, []any{"abc"})
			})
		})

		Convey("When the cursor is malformed", func() {
			_, _, err := KeysetPredicate(cols, "not-base64!!")
			Convey("Then it errors", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})

	Convey("Given a composite descending-then-ascending ordering", t, func() {
		cols := []KeysetColumn{{Expr: "updated_at", Desc: true, Cast: "::timestamptz"}, {Expr: "entity_id"}}
		cursor := EncodeKeyset("2026-01-01T00:00:00Z", "e5")

		Convey("When building the predicate", func() {
			pred, args, err := KeysetPredicate(cols, cursor)
			Convey("Then it is the row-tuple comparison with casts and mixed directions", func() {
				So(err, ShouldBeNil)
				So(pred, ShouldContainSubstring, "updated_at < ?::timestamptz")
				So(pred, ShouldContainSubstring, "updated_at = ?::timestamptz AND entity_id > ?")
				So(len(args), ShouldEqual, 3)
			})
		})
	})
}

func TestKeysetPage(t *testing.T) {
	Convey("Given an over-fetched page (limit+1)", t, func() {
		page := Page{Limit: 2}
		cursorOf := func(s string) string { return EncodeKeyset(s) }

		Convey("When the sentinel row is present", func() {
			items, info := KeysetPage(page, []string{"a", "b", "c"}, nil, cursorOf)
			Convey("Then it trims to the page and reports more with a next cursor", func() {
				So(items, ShouldResemble, []string{"a", "b"})
				So(info.HasNextPage, ShouldBeTrue)
				So(info.NextCursor, ShouldNotBeNil)
				So(info.TotalCount, ShouldBeNil) // not requested
			})
		})

		Convey("When the page is the last one", func() {
			items, info := KeysetPage(page, []string{"a"}, nil, cursorOf)
			Convey("Then there is no next page or cursor", func() {
				So(items, ShouldResemble, []string{"a"})
				So(info.HasNextPage, ShouldBeFalse)
				So(info.NextCursor, ShouldBeNil)
			})
		})

		Convey("When a total is provided", func() {
			total := 42
			_, info := KeysetPage(page, []string{"a", "b", "c"}, &total, cursorOf)
			Convey("Then it rides along in PageInfo", func() {
				So(info.TotalCount, ShouldNotBeNil)
				So(*info.TotalCount, ShouldEqual, 42)
			})
		})
	})
}
