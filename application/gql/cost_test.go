package gql

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCheckQueryCost(t *testing.T) {
	Convey("A document over the field-selection budget is rejected", t, func() {
		var b strings.Builder
		b.WriteString("{")
		for i := 0; i < maxQueryFields+10; i++ {
			fmt.Fprintf(&b, " f%d", i)
		}
		b.WriteString(" }")
		err := checkQueryCost(b.String())
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "too complex")
	})

	Convey("A normal query passes", t, func() {
		So(checkQueryCost("{ product { edges { node { id name } } pageInfo { hasNextPage } } }"), ShouldBeNil)
	})

	Convey("A syntactically invalid query is left for graphql.Do", t, func() {
		So(checkQueryCost("{ this is not valid"), ShouldBeNil)
	})

	Convey("A cyclic fragment spread terminates (does not hang)", t, func() {
		q := "{ p { ...A } } fragment A on X { a ...B } fragment B on X { b ...A }"
		So(checkQueryCost(q), ShouldBeNil) // returns without hanging
	})
}
