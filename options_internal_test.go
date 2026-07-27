package flexitype

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestWithFailClosedACLOption checks the option wiring without constructing a
// Service. uow.RequireAccessPolicy is deliberately one-way and process-wide,
// so calling New with the option here would change the default for every other
// test in this binary. The semantics of the flag itself are covered in
// application/uow/access_test.go.
func TestWithFailClosedACLOption(t *testing.T) {
	Convey("Given the default option set", t, func() {
		o := &options{}

		Convey("Then the field ACL is fail-open, as the standalone service expects", func() {
			So(o.failClosedACL, ShouldBeFalse)
		})

		Convey("When the embedder selects WithFailClosedACL", func() {
			WithFailClosedACL()(o)

			Convey("Then construction will require an access policy per request", func() {
				So(o.failClosedACL, ShouldBeTrue)
			})
		})
	})
}
