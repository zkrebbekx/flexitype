package dependency

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestContextTypeKeySurvivesRoundTrip pins the rollback contract for the
// v1.5 context_type key: this release does not evaluate it, but it must not
// strip it either. A decode followed by a re-encode keeps the key, so an
// Update, a clone or an export by this binary preserves a v1.5 rule.
func TestContextTypeKeySurvivesRoundTrip(t *testing.T) {
	Convey("Given a v1.5 rule with a typed context condition", t, func() {
		raw := []byte(`{"kind":"equals","context_key":"customer_tier","context_type":"string",` +
			`"value":{"type":"string","value":"enterprise"}}`)

		Convey("When this release decodes and re-encodes it", func() {
			var c Condition
			So(json.Unmarshal(raw, &c), ShouldBeNil)
			out, err := json.Marshal(c)
			So(err, ShouldBeNil)

			Convey("Then the context_type key survives", func() {
				So(c.ContextType, ShouldEqual, "string")
				So(string(out), ShouldContainSubstring, `"context_type":"string"`)
			})
		})
	})
}
