package dependency

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestExclusiveBoundKeysSurviveRoundTrip pins the rollback contract for the
// v1.4 strict-bound keys: this release does not evaluate them, but it must
// not strip them either. A decode followed by a re-encode keeps both keys,
// so an Update, a clone or an export by this binary preserves a v1.4 rule.
func TestExclusiveBoundKeysSurviveRoundTrip(t *testing.T) {
	Convey("Given a v1.4 rule with an exclusive min bound", t, func() {
		raw := []byte(`{"kind":"range","min":{"type":"integer","value":50000},"min_exclusive":true,"max_exclusive":true}`)

		Convey("When this release decodes and re-encodes it", func() {
			var c Condition
			So(json.Unmarshal(raw, &c), ShouldBeNil)
			out, err := json.Marshal(c)
			So(err, ShouldBeNil)

			Convey("Then both strict-bound keys survive", func() {
				So(c.MinExclusive, ShouldBeTrue)
				So(c.MaxExclusive, ShouldBeTrue)
				So(string(out), ShouldContainSubstring, `"min_exclusive":true`)
				So(string(out), ShouldContainSubstring, `"max_exclusive":true`)
			})
		})
	})
}
