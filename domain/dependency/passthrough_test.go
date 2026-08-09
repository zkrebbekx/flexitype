package dependency

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestEffectCarriesTheV18EnforceKey pins the rollback contract for the effect
// payload.
//
// v1.8 stores `enforce` inside the effect JSONB to say where a required
// override is enforced. This release gives it no meaning, but it must not lose
// it: the effect document is re-encoded in full on every Save, so a v1.7 pod
// still serving during a rolling deploy — or a deliberate rollback — would
// otherwise rewrite a blocking rule as a reporting one, permanently. Rolling
// forward would not bring it back, and the failure is silent in the dangerous
// direction: the wall quietly becomes a suggestion.
//
// docs/upgrades.md states the rule this test enforces: a release that adds a
// payload key ships the key one release EARLIER as a decode-and-re-encode
// passthrough, with no behavior.
func TestEffectCarriesTheV18EnforceKey(t *testing.T) {
	Convey("Given an effect written by a later release", t, func() {
		stored := []byte(`{"required":true,"enforce":"on_write"}`)

		Convey("When this release decodes and re-encodes it", func() {
			var effect Effect
			So(effect.UnmarshalJSON(stored), ShouldBeNil)
			out, err := effect.MarshalJSON()
			So(err, ShouldBeNil)

			Convey("Then the mode survives untouched", func() {
				So(effect.Enforce, ShouldEqual, "on_write")

				var back map[string]any
				So(json.Unmarshal(out, &back), ShouldBeNil)
				So(back["enforce"], ShouldEqual, "on_write")
				So(back["required"], ShouldEqual, true)
			})
		})
	})

	Convey("Given an effect this release authored", t, func() {
		want := true
		effect := Effect{Required: &want}

		Convey("When it is encoded", func() {
			out, err := effect.MarshalJSON()
			So(err, ShouldBeNil)

			Convey("Then no mode is invented", func() {
				// An absent key must stay absent. Writing a default would
				// change the schema-bundle idempotency key for every rule
				// this release touches.
				So(string(out), ShouldNotContainSubstring, "enforce")
			})
		})
	})

	Convey("Given a rule carrying the later release's mode", t, func() {
		source := stringAttr("status", false)
		target := stringAttr("sku", false)
		v := valueobjects.NewStringValue("active")
		want := true

		d, _, err := New(NewInput{
			TenantID:   valueobjects.DefaultTenant,
			Source:     source,
			Target:     target,
			Conditions: []Condition{{Kind: CondEquals, Value: &v}},
			Effect:     Effect{Required: &want, Enforce: "on_write"},
		}, time.Now().UTC())
		So(err, ShouldBeNil)

		Convey("When this release evaluates it", func() {
			schema, rerr := ResolveEffectiveWithContext(
				target, []*Dependency{d},
				map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
					source.ID(): {valueobjects.NewStringValue("active")},
				}, nil, time.Now().UTC())
			So(rerr, ShouldBeNil)

			Convey("Then the requirement is reported, exactly as before", func() {
				// The key is carried, not obeyed. A v1.7 binary must behave
				// identically whether or not a rule has been given a mode by a
				// newer one, or a rolling deploy would flip enforcement back
				// and forth as requests land on different pods.
				So(schema.Required, ShouldBeTrue)
			})
		})

		Convey("When it round-trips through a snapshot, as Update and Archive do", func() {
			raw, merr := json.Marshal(d.Snapshot())
			So(merr, ShouldBeNil)
			var back Snapshot
			So(json.Unmarshal(raw, &back), ShouldBeNil)

			Convey("Then the mode is still on the effect", func() {
				So(back.Effect.Enforce, ShouldEqual, "on_write")
			})
		})
	})
}
