package gql

import (
	"errors"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/location"
	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

// TestSanitize covers the asymmetry between the two API surfaces.
//
// REST maps a domain error to its client message and collapses everything else
// into "internal error" with a 500. GraphQL returned graphql.Do's result
// verbatim, so a resolver that wrapped an infrastructure error surfaced its
// message intact — SQL fragments, table and column names, constraint names. The
// same failure was therefore reconnaissance through one surface and nothing
// through the other, and hardening either gave false confidence about the pair.
func TestSanitize(t *testing.T) {
	Convey("Given a result carrying errors of each kind", t, func() {
		var observed []error
		onError := func(err error) { observed = append(observed, err) }

		infra := errors.New(`pq: column "value_txt" does not exist (42703)`)
		domain := domainerrors.NewNotFound("type_definition", "01ABC")

		res := &graphql.Result{Errors: []gqlerrors.FormattedError{
			gqlerrors.FormatError(infra),
			gqlerrors.FormatError(domain),
		}}
		// FormatError does not carry Path, which is how a resolver error is
		// distinguished from a parse error; set it so these read as execution
		// errors.
		res.Errors[0].Path = []any{"products"}
		res.Errors[1].Path = []any{"products"}

		out := sanitize(res, onError)

		Convey("Then the infrastructure detail never reaches the client", func() {
			So(out.Errors[0].Message, ShouldEqual, "internal error")
			So(out.Errors[0].Message, ShouldNotContainSubstring, "value_txt")
			So(out.Errors[0].Message, ShouldNotContainSubstring, "42703")
		})

		Convey("Then the detail is reported to the observer instead of lost", func() {
			So(observed, ShouldHaveLength, 1)
			So(observed[0].Error(), ShouldContainSubstring, "value_txt")
		})

		Convey("Then a domain error keeps its client-facing message", func() {
			So(out.Errors[1].Message, ShouldContainSubstring, "type_definition")
		})
	})

	Convey("Given a query the caller wrote wrongly", t, func() {
		// A parse or validation error describes the caller's own query, so
		// masking it would protect nothing and make the API unusable.
		res := &graphql.Result{Errors: []gqlerrors.FormattedError{{
			Message:   `Cannot query field "nosuch" on type "Query".`,
			Locations: []location.SourceLocation{{Line: 1, Column: 3}},
		}}}

		out := sanitize(res, nil)

		Convey("Then the message survives so the client can fix the query", func() {
			So(out.Errors[0].Message, ShouldContainSubstring, "Cannot query field")
		})
	})

	Convey("Given a nil result", t, func() {
		Convey("Then sanitizing it is a no-op rather than a panic", func() {
			So(func() { sanitize(nil, nil) }, ShouldNotPanic)
			So(sanitize(nil, nil), ShouldBeNil)
		})
	})

	Convey("Given a result with no errors", t, func() {
		res := &graphql.Result{Data: map[string]any{"ok": true}}
		out := sanitize(res, nil)

		Convey("Then it passes through untouched", func() {
			So(out.Errors, ShouldBeEmpty)
			So(out.Data, ShouldNotBeNil)
		})
	})

	Convey("Given no observer is configured", t, func() {
		res := &graphql.Result{Errors: []gqlerrors.FormattedError{
			gqlerrors.FormatError(errors.New("pq: boom")),
		}}
		res.Errors[0].Path = []any{"products"}

		Convey("Then masking still happens, silently", func() {
			out := sanitize(res, nil)
			So(out.Errors[0].Message, ShouldEqual, "internal error")
		})
	})
}
