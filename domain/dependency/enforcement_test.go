package dependency

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// enforcedDep builds a rule that demands a value, with an explicit
// enforcement mode.
func enforcedDep(source, target *attribute.Definition, match string, mode Enforcement) *Dependency {
	want := true
	v := valueobjects.NewStringValue(match)
	d, _, err := New(NewInput{
		TenantID:   valueobjects.DefaultTenant,
		Source:     source,
		Target:     target,
		Conditions: []Condition{{Kind: CondEquals, Value: &v}},
		Effect:     Effect{Required: &want, Enforce: mode},
	}, time.Now().UTC())
	So(err, ShouldBeNil)
	return d
}

// values wraps one source value in the map the resolver takes.
func sourceOf(a *attribute.Definition, raw string) map[valueobjects.AttributeDefinitionID][]valueobjects.Value {
	return map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
		a.ID(): {valueobjects.NewStringValue(raw)},
	}
}

// TestEnforcementIsDeclared covers where a required override is enforced.
//
// A dependency describes what an entity needs. Whether that need refuses a
// write or is reported for the caller to act on is a policy the schema author
// chooses, and before this it was neither stated nor selectable.
func TestEnforcementIsDeclared(t *testing.T) {
	Convey("Given a rule that requires a disposal plan for asbestos", t, func() {
		source := stringAttr("certifications", false)
		target := stringAttr("disposal_plan", false)
		now := time.Now().UTC()

		Convey("When the rule does not say how it is enforced", func() {
			dep := requiredDep(source, target, "asbestos", true)
			schema, err := ResolveEffectiveWithContext(
				target, []*Dependency{dep}, sourceOf(source, "asbestos"), nil, now)
			So(err, ShouldBeNil)

			Convey("Then it reports rather than blocks", func() {
				// The default has to be what the service already did. Reading
				// an absent mode as on_write would have made every rule stored
				// before this feature start refusing writes on upgrade.
				So(schema.Required, ShouldBeTrue)
				So(schema.RequiredByDependency, ShouldBeTrue)
				So(schema.RequiredEnforcement, ShouldEqual, EnforceOnRead)
			})
		})

		Convey("When the rule asks for on_write", func() {
			dep := enforcedDep(source, target, "asbestos", EnforceOnWrite)
			schema, err := ResolveEffectiveWithContext(
				target, []*Dependency{dep}, sourceOf(source, "asbestos"), nil, now)
			So(err, ShouldBeNil)

			Convey("Then the requirement is enforced on the write path", func() {
				So(schema.Required, ShouldBeTrue)
				So(schema.RequiredEnforcement, ShouldEqual, EnforceOnWrite)
			})
		})

		Convey("When the rule does not match the entity's values", func() {
			dep := enforcedDep(source, target, "asbestos", EnforceOnWrite)
			schema, err := ResolveEffectiveWithContext(
				target, []*Dependency{dep}, sourceOf(source, "timber"), nil, now)
			So(err, ShouldBeNil)

			Convey("Then nothing is required and nothing is enforced", func() {
				So(schema.Required, ShouldBeFalse)
				So(schema.RequiredByDependency, ShouldBeFalse)
				So(schema.RequiredEnforcement, ShouldEqual, EnforceOnRead)
			})
		})
	})

	Convey("Given an attribute that is required by its own declaration", t, func() {
		target := stringAttr("sku", true)
		source := stringAttr("status", false)
		dep := requiredDep(source, target, "never", true)
		now := time.Now().UTC()

		Convey("When no dependency matches", func() {
			schema, err := ResolveEffectiveWithContext(
				target, []*Dependency{dep}, sourceOf(source, "draft"), nil, now)
			So(err, ShouldBeNil)

			Convey("Then the requirement is not attributed to a dependency", func() {
				// The distinction is what stops the write gate from making an
				// entity impossible to create: the first value written always
				// leaves the other declared-required attributes empty.
				So(schema.Required, ShouldBeTrue)
				So(schema.RequiredByDependency, ShouldBeFalse)
			})
		})
	})

	Convey("Given two matched rules demanding the same value", t, func() {
		target := stringAttr("disposal_plan", false)
		hazardous := stringAttr("hazardous", false)
		regulated := stringAttr("regulated", false)
		now := time.Now().UTC()

		blocking := enforcedDep(hazardous, target, "yes", EnforceOnWrite)
		reporting := enforcedDep(regulated, target, "yes", EnforceOnRead)
		sources := map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
			hazardous.ID(): {valueobjects.NewStringValue("yes")},
			regulated.ID(): {valueobjects.NewStringValue("yes")},
		}

		Convey("When one blocks and the other only reports", func() {
			forward, err := ResolveEffectiveWithContext(
				target, []*Dependency{blocking, reporting}, sources, nil, now)
			So(err, ShouldBeNil)
			backward, err := ResolveEffectiveWithContext(
				target, []*Dependency{reporting, blocking}, sources, nil, now)
			So(err, ShouldBeNil)

			Convey("Then the blocking rule wins, whichever order they resolve in", func() {
				// Same direction the required conflict itself resolves in: the
				// permissive answer lets a record past a gate someone
				// configured to stop it. Order-independence matters because the
				// two backends return dependencies in different orders.
				So(forward.RequiredEnforcement, ShouldEqual, EnforceOnWrite)
				So(backward.RequiredEnforcement, ShouldEqual, EnforceOnWrite)
			})
		})

		Convey("When the blocking rule is the one that does NOT match", func() {
			partial := map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
				hazardous.ID(): {valueobjects.NewStringValue("no")},
				regulated.ID(): {valueobjects.NewStringValue("yes")},
			}
			schema, err := ResolveEffectiveWithContext(
				target, []*Dependency{blocking, reporting}, partial, nil, now)
			So(err, ShouldBeNil)

			Convey("Then only the matched rule's mode counts", func() {
				So(schema.Required, ShouldBeTrue)
				So(schema.RequiredEnforcement, ShouldEqual, EnforceOnRead)
			})
		})
	})

	Convey("Given a rule that RELAXES a requirement", t, func() {
		target := stringAttr("sku", true)
		source := stringAttr("status", false)
		now := time.Now().UTC()
		relax := requiredDep(source, target, "draft", false)

		Convey("When it matches", func() {
			schema, err := ResolveEffectiveWithContext(
				target, []*Dependency{relax}, sourceOf(source, "draft"), nil, now)
			So(err, ShouldBeNil)

			Convey("Then nothing is required and no mode is claimed", func() {
				// A rule that removes a demand has no enforcement to
				// contribute; reading one from it would let a relaxation turn
				// into a block.
				So(schema.Required, ShouldBeFalse)
				So(schema.RequiredEnforcement, ShouldEqual, EnforceOnRead)
			})
		})
	})
}

// TestEnforcementIsValidated covers what the schema author may declare.
func TestEnforcementIsValidated(t *testing.T) {
	Convey("Given a rule being defined", t, func() {
		source := stringAttr("status", false)
		target := stringAttr("sku", false)
		want := true
		now := time.Now().UTC()
		v := valueobjects.NewStringValue("active")
		conditions := []Condition{{Kind: CondEquals, Value: &v}}

		Convey("When enforce names a mode that does not exist", func() {
			_, _, err := New(NewInput{
				TenantID: valueobjects.DefaultTenant, Source: source, Target: target,
				Conditions: conditions,
				Effect:     Effect{Required: &want, Enforce: Enforcement("on_publish")},
			}, now)

			Convey("Then it is refused, and says what is allowed", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "on_write or on_read")
			})
		})

		Convey("When enforce is set on an effect with no required override", func() {
			allowed := valueobjects.NewStringValue("a")
			_, _, err := New(NewInput{
				TenantID: valueobjects.DefaultTenant, Source: source, Target: target,
				Conditions: conditions,
				Effect: Effect{
					AllowedValues: []valueobjects.Value{allowed},
					Enforce:       EnforceOnWrite,
				},
			}, now)

			Convey("Then it is refused rather than stored and ignored", func() {
				// A stored setting that changes nothing reads as a configured
				// rule. Allowed values and constraints are checked against a
				// value the caller submitted, so there is nothing to defer.
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "required override")
			})
		})

		Convey("When enforce is omitted", func() {
			d, _, err := New(NewInput{
				TenantID: valueobjects.DefaultTenant, Source: source, Target: target,
				Conditions: conditions, Effect: Effect{Required: &want},
			}, now)

			Convey("Then the rule is accepted and defaults to on_read", func() {
				So(err, ShouldBeNil)
				So(d.Effect().Enforcement(), ShouldEqual, EnforceOnRead)
			})
		})
	})
}

// TestEnforcementSurvivesStorage pins the JSON round-trip, because the effect
// is stored as a document rather than as columns.
func TestEnforcementSurvivesStorage(t *testing.T) {
	Convey("Given an effect that blocks writes", t, func() {
		want := true
		effect := Effect{Required: &want, Enforce: EnforceOnWrite}

		Convey("When it is marshalled and read back", func() {
			raw, err := effect.MarshalJSON()
			So(err, ShouldBeNil)
			var back Effect
			So(back.UnmarshalJSON(raw), ShouldBeNil)

			Convey("Then the mode is still there", func() {
				// Losing it here would silently downgrade every stored rule to
				// reporting, which is a rule that looks configured and stops
				// nothing.
				So(string(raw), ShouldContainSubstring, `"enforce":"on_write"`)
				So(back.Enforcement(), ShouldEqual, EnforceOnWrite)
			})
		})
	})

	Convey("Given an effect stored before enforcement existed", t, func() {
		var back Effect
		So(back.UnmarshalJSON([]byte(`{"required":true}`)), ShouldBeNil)

		Convey("Then it reads as on_read, and is not written back with a mode", func() {
			So(back.Enforcement(), ShouldEqual, EnforceOnRead)
			raw, err := back.MarshalJSON()
			So(err, ShouldBeNil)
			So(string(raw), ShouldNotContainSubstring, "enforce")
		})
	})
}
