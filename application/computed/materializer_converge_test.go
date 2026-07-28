package computed

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestBackgroundRebuildNeverClears pins the rule that keeps a background
// rebuild from undoing a concurrent write.
//
// Clearing is what makes an overlapping write destructive. A rebuild that
// reads an entity mid-write sees half its inputs, computes an undefined
// result, and would clear a value that is about to be correct — and it cannot
// tell that apart from a formula that has genuinely become undefined. The
// write path can: it runs inside the writing request with the entity's whole
// value set.
//
// So the rule is: a rebuild converges values FORWARD and never clears. A
// computed value that becomes undefined for an entity is cleared by that
// entity's next write, or by the tenant-wide recompute.
func TestBackgroundRebuildNeverClears(t *testing.T) {
	Convey("Given the two recompute entry points", t, func() {
		m := &Materializer{}

		Convey("Then the exported one is allowed to clear", func() {
			// Recompute is the write path's entry point: it runs with the
			// entity's whole value set, so an undefined result means the
			// value really is undefined.
			So(m.clearsStaleValues(true), ShouldBeTrue)
		})

		Convey("Then the background one is not", func() {
			So(m.clearsStaleValues(false), ShouldBeFalse)
		})
	})
}
