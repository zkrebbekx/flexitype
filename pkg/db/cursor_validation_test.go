package db

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

// TestValidateKeysetRejectsUnusableCursors pins the fix for issue #502.
//
// The cursor is opaque client input. PageArgs.Resolve checks its shape only
// (base64 that decodes to a JSON string array), because the ordering columns
// are not known at that layer. Two cursors passed that check and then went
// wrong further down:
//
//   - EncodeKeyset("not-a-time", "x") against a "::timestamptz" column reached
//     the cast in the compiled query, and PostgreSQL failed with SQLSTATE
//     22007. The service reported an internal error (HTTP 500).
//   - A cursor with the wrong number of values was discarded, and the query
//     ran with no keyset predicate at all, so the caller received page 1 again
//     while believing it had advanced.
//
// ValidateKeyset now rejects both as domain validation errors, which the HTTP
// layer maps to 422.
func TestValidateKeysetRejectsUnusableCursors(t *testing.T) {
	Convey("Given an ordering of a timestamp column and a text tiebreaker", t, func() {
		cols := []KeysetColumn{
			{Expr: "last_updated_at", Desc: true, Cast: "::timestamptz"},
			{Expr: "entity_id"},
		}

		Convey("When the cursor carries a value the timestamp cast cannot parse", func() {
			values, err := ValidateKeyset(cols, EncodeKeyset("not-a-time", "e1"))

			Convey("Then it is a validation error and no values come back", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(values, ShouldBeNil)
			})

			Convey("And the message does not repeat the cursor's contents", func() {
				So(err.Error(), ShouldEqual, "invalid cursor")
				So(err.Error(), ShouldNotContainSubstring, "not-a-time")
			})
		})

		Convey("When the cursor carries one value instead of two", func() {
			_, err := ValidateKeyset(cols, EncodeKeyset(KeysetTime(time.Now())))

			Convey("Then it is a validation error rather than a silent restart", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When the cursor carries three values instead of two", func() {
			_, err := ValidateKeyset(cols, EncodeKeyset(KeysetTime(time.Now()), "e1", "extra"))

			Convey("Then it is a validation error", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When the cursor is the one KeysetTime emits", func() {
			at := time.Date(2026, 8, 8, 12, 30, 0, 365800000, time.UTC)
			values, err := ValidateKeyset(cols, EncodeKeyset(KeysetTime(at), "e1"))

			Convey("Then it is accepted and decodes to its two values", func() {
				So(err, ShouldBeNil)
				So(values, ShouldResemble, []string{"2026-08-08T12:30:00.365800000Z", "e1"})
			})
		})

		Convey("When the cursor carries a plain RFC 3339 timestamp", func() {
			_, err := ValidateKeyset(cols, EncodeKeyset("2026-08-08T12:30:00Z", "e1"))

			Convey("Then it is accepted, because a hand-built cursor may use it", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When the text column carries a value that is not a timestamp", func() {
			_, err := ValidateKeyset(cols, EncodeKeyset(KeysetTime(time.Now()), "anything at all"))

			Convey("Then it is accepted, because a text column accepts any string", func() {
				So(err, ShouldBeNil)
			})
		})
	})

	Convey("Given an ordering on a numeric column", t, func() {
		cols := []KeysetColumn{{Expr: "sequence_no", Cast: "::bigint"}}

		Convey("When the cursor value is not a number", func() {
			_, err := ValidateKeyset(cols, EncodeKeyset("seventeen"))

			Convey("Then it is a validation error", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("When the cursor value is a number", func() {
			_, err := ValidateKeyset(cols, EncodeKeyset("17"))

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
			})
		})
	})

	Convey("Given an ordering on an uncast column", t, func() {
		cols := []KeysetColumn{{Expr: "id"}}

		Convey("When the cursor carries an arbitrary string", func() {
			values, err := ValidateKeyset(cols, EncodeKeyset("not-a-time"))

			Convey("Then it is accepted, because the column is text", func() {
				So(err, ShouldBeNil)
				So(values, ShouldResemble, []string{"not-a-time"})
			})
		})
	})
}

// TestKeysetPredicateValidatesBeforeBuilding proves the predicate builder
// itself refuses an unusable cursor, so no repository can bind a value the
// cast will reject.
func TestKeysetPredicateValidatesBeforeBuilding(t *testing.T) {
	cols := []KeysetColumn{
		{Expr: "occurred_at", Desc: true, Cast: "::timestamptz"},
		{Expr: "id", Desc: true},
	}

	Convey("Given the activity-log ordering", t, func() {
		Convey("When the cursor's timestamp is garbage", func() {
			pred, args, err := KeysetPredicate(cols, EncodeKeyset("not-a-time", "01J"))

			Convey("Then no predicate and no arguments are produced", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(pred, ShouldBeEmpty)
				So(args, ShouldBeNil)
			})
		})

		Convey("When the cursor is well formed", func() {
			at := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			pred, args, err := KeysetPredicate(cols, EncodeKeyset(KeysetTime(at), "01J"))

			Convey("Then the row-tuple predicate is built as before", func() {
				So(err, ShouldBeNil)
				So(pred, ShouldEqual,
					"((occurred_at < ?::timestamptz) OR (occurred_at = ?::timestamptz AND id < ?))")
				So(args, ShouldHaveLength, 3)
			})
		})

		Convey("When there is no cursor at all", func() {
			pred, args, err := KeysetPredicate(cols, "")

			Convey("Then the first page is served with no predicate", func() {
				So(err, ShouldBeNil)
				So(pred, ShouldBeEmpty)
				So(args, ShouldBeNil)
			})
		})
	})
}
