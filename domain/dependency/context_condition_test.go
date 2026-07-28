package dependency

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestContextKeyRoundTrip pins that a context-keyed condition survives the
// wire.
//
// A rule is stored as JSON, so a field that marshals but does not unmarshal
// (or the reverse) produces a rule that behaves differently after a restart
// than it did when it was written — and nothing reports it, because both
// halves succeed.
func TestContextKeyRoundTrip(t *testing.T) {
	Convey("Given a condition keyed on a caller-supplied fact", t, func() {
		v := valueobjects.NewStringValue("enterprise")
		c := Condition{Kind: CondEquals, Value: &v, ContextKey: "customer_tier"}

		raw, err := json.Marshal(c)
		So(err, ShouldBeNil)

		Convey("Then the key is on the wire", func() {
			So(string(raw), ShouldContainSubstring, `"context_key":"customer_tier"`)
		})

		Convey("Then it survives a round trip", func() {
			var back Condition
			So(json.Unmarshal(raw, &back), ShouldBeNil)
			So(back.ContextKey, ShouldEqual, "customer_tier")
			So(back.Kind, ShouldEqual, CondEquals)
			So(back.Value, ShouldNotBeNil)
			So(back.Value.String(), ShouldEqual, "enterprise")
		})
	})

	Convey("Given an ordinary condition", t, func() {
		v := valueobjects.NewStringValue("active")
		raw, err := json.Marshal(Condition{Kind: CondEquals, Value: &v})
		So(err, ShouldBeNil)

		Convey("Then no context key appears, so the wire form is unchanged", func() {
			So(string(raw), ShouldNotContainSubstring, "context_key")
		})
	})
}

// TestMatchesAnyWithContext covers evaluation against the caller's facts.
func TestMatchesAnyWithContext(t *testing.T) {
	now := time.Now().UTC()

	Convey("Given a rule that fires on a caller-supplied tier", t, func() {
		source := stringAttr("note", false)
		target := stringAttr("po_number", false)
		want := valueobjects.NewStringValue("enterprise")

		d, _, err := New(NewInput{
			TenantID: valueobjects.DefaultTenant, Source: source, Target: target,
			Conditions: []Condition{{Kind: CondEquals, Value: &want, ContextKey: "customer_tier"}},
			Effect:     Effect{Required: boolPtr(true)},
		}, now)
		So(err, ShouldBeNil)

		Convey("When the fact matches", func() {
			ok, merr := d.MatchesAnyWithContext(nil, map[string]valueobjects.Value{
				"customer_tier": valueobjects.NewStringValue("enterprise"),
			}, now)
			So(merr, ShouldBeNil)
			So(ok, ShouldBeTrue)
		})

		Convey("When the fact differs", func() {
			ok, merr := d.MatchesAnyWithContext(nil, map[string]valueobjects.Value{
				"customer_tier": valueobjects.NewStringValue("self-serve"),
			}, now)
			So(merr, ShouldBeNil)
			So(ok, ShouldBeFalse)
		})

		Convey("When the fact is absent", func() {
			Convey("Then it does not match: absent is not false", func() {
				ok, merr := d.MatchesAnyWithContext(nil, nil, now)
				So(merr, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})

			Convey("Then passing no context facts is the same as having none", func() {
				// There is no context-free variant any more: one existed, the
				// write validator called it, and a condition naming a context
				// key silently never matched there while both read paths
				// reported the restriction.
				ok, merr := d.MatchesAnyWithContext([]valueobjects.Value{
					valueobjects.NewStringValue("enterprise"),
				}, nil, now)
				So(merr, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When source values are also present", func() {
			Convey("Then the context key wins for that condition", func() {
				// The rule names a context key, so the source value is not
				// what it tests — otherwise a rule would fire on the wrong
				// subject entirely.
				ok, merr := d.MatchesAnyWithContext(
					[]valueobjects.Value{valueobjects.NewStringValue("enterprise")},
					map[string]valueobjects.Value{
						"customer_tier": valueobjects.NewStringValue("self-serve"),
					}, now)
				So(merr, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})
	})
}
