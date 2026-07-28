package flexitype_test

import (
	"testing"

	"github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"
)

// TestDriverAPISurface pins the slice of lib/pq this module depends on.
//
// go.mod requires v1.12.3, but a host monorepo commonly `replace`s lib/pq
// with a fork patched for connection-pooler behaviour — and a replace wins
// over minimal version selection, so the build succeeds against whatever the
// host pinned, with no warning and no stated contract to check against.
//
// This is that contract. It compiles against exactly the APIs the code uses,
// so a fork that drops or changes one fails here rather than at runtime in
// the host's deployment.
func TestDriverAPISurface(t *testing.T) {
	Convey("Given the lib/pq APIs this module depends on", t, func() {
		Convey("Then array binding is available", func() {
			// Used by the relationship and query stores to bind id lists.
			So(pq.Array([]string{"a", "b"}), ShouldNotBeNil)

			var arr pq.StringArray
			So(arr.Scan([]byte("{a,b}")), ShouldBeNil)
			So([]string(arr), ShouldResemble, []string{"a", "b"})
		})

		Convey("Then a driver error exposes its SQLSTATE code", func() {
			// Constraint-violation mapping reads this to turn a unique
			// violation into a CONFLICT rather than an INTERNAL.
			err := &pq.Error{Code: "23505", Message: "duplicate key"}
			So(string(err.Code), ShouldEqual, "23505")
			So(err.Error(), ShouldContainSubstring, "duplicate key")
		})

		Convey("Then bulk copy is available for the stress harness", func() {
			// pq.CopyIn is deprecated upstream but still the API the stress
			// harness uses, so a fork that removes it breaks that harness.
			// Naming it here is the point of this test.
			//nolint:staticcheck // SA1019: pinning the surface the code uses
			So(pq.CopyIn("t", "a", "b"), ShouldContainSubstring, "COPY")
		})
	})
}
