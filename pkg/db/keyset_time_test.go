package db

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// TestKeysetTimeOrdersLexically is the regression test for a silent
// row-skipping bug: cursor timestamps used to be rendered with
// time.RFC3339Nano, which strips trailing zeros. Backends that compare cursor
// columns as strings then hit a shorter fraction whose next character is the
// trailing 'Z' (0x5A) rather than a digit (0x30-0x39), so an OLDER timestamp
// compared as newer — and the resulting non-monotonic comparison made a binary
// search over the page land in the wrong region and drop rows entirely.
func TestKeysetTimeOrdersLexically(t *testing.T) {
	base := time.Date(2026, 7, 18, 6, 4, 4, 0, time.UTC)

	Convey("Given timestamps whose fractions differ in trailing zeros", t, func() {
		// .365800000 renders as ".3658" under RFC3339Nano; .365821000 as
		// ".365821". These are exactly the values that reproduced the skip.
		older := base.Add(365800 * time.Microsecond)
		newer := base.Add(365821 * time.Microsecond)
		So(older.Before(newer), ShouldBeTrue)

		Convey("Then RFC3339Nano orders them WRONG as strings (the original bug)", func() {
			So(older.UTC().Format(time.RFC3339Nano) < newer.UTC().Format(time.RFC3339Nano), ShouldBeFalse)
		})

		Convey("And KeysetTime orders them chronologically", func() {
			So(KeysetTime(older) < KeysetTime(newer), ShouldBeTrue)
		})
	})

	Convey("Given a range of fractions, lexical order always matches chronological order", t, func() {
		var prev string
		for _, micros := range []int{0, 1, 10, 100, 1000, 10000, 100000, 365800, 365812, 365821, 999999} {
			cur := KeysetTime(base.Add(time.Duration(micros) * time.Microsecond))
			if prev != "" {
				So(prev < cur, ShouldBeTrue)
			}
			prev = cur
		}
	})

	Convey("Given any timestamp", t, func() {
		Convey("Then the rendered fraction is fixed-width, so comparisons stay aligned", func() {
			So(len(KeysetTime(base)), ShouldEqual, len(KeysetTime(base.Add(365821*time.Microsecond))))
			So(KeysetTime(base), ShouldEqual, "2026-07-18T06:04:04.000000000Z")
		})

		Convey("And it round-trips through RFC3339 parsing (Postgres casts it to timestamptz)", func() {
			parsed, err := time.Parse(time.RFC3339Nano, KeysetTime(base.Add(365800*time.Microsecond)))
			So(err, ShouldBeNil)
			So(parsed.Equal(base.Add(365800*time.Microsecond)), ShouldBeTrue)
		})
	})
}
