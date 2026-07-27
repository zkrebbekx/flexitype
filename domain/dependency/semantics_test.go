package dependency

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// stringAttr builds a minimal string attribute for resolver tests. It is
// separate from the package's newAttr helper because these cases need the
// required flag, which that helper does not take.
func stringAttr(name string, required bool) *attribute.Definition {
	def, _, err := attribute.New(attribute.NewInput{
		TenantID:         valueobjects.DefaultTenant,
		TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
		InternalName:     name,
		DisplayName:      name,
		DataType:         valueobjects.DataTypeString,
		Required:         required,
	}, time.Now().UTC())
	So(err, ShouldBeNil)
	return def
}

// requiredDep builds a dependency whose effect sets Required to want.
func requiredDep(source, target *attribute.Definition, match string, want bool) *Dependency {
	v := valueobjects.NewStringValue(match)
	d, _, err := New(NewInput{
		TenantID:   valueobjects.DefaultTenant,
		Source:     source,
		Target:     target,
		Conditions: []Condition{{Kind: CondEquals, Value: &v}},
		Effect:     Effect{Required: &want},
	}, time.Now().UTC())
	So(err, ShouldBeNil)
	return d
}

// TestMatchesAny covers the collapse of a multi-valued or scoped source.
//
// Every consumer built a map of ONE value per attribute, so a multi-valued
// attribute reduced to an arbitrary member and a scoped one to whichever
// variant was written last. A rule that fired correctly yesterday could stop
// firing with no schema or data change to explain it.
func TestMatchesAny(t *testing.T) {
	Convey("Given a rule that fires when the source equals asbestos", t, func() {
		source := stringAttr("certifications", false)
		target := stringAttr("disposal_plan", false)
		dep := requiredDep(source, target, "asbestos", true)
		now := time.Now().UTC()

		Convey("When the entity holds only the matching value", func() {
			ok, err := dep.MatchesAny([]valueobjects.Value{valueobjects.NewStringValue("asbestos")}, now)
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
		})

		Convey("When the matching value is not the last one written", func() {
			ok, err := dep.MatchesAny([]valueobjects.Value{
				valueobjects.NewStringValue("asbestos"),
				valueobjects.NewStringValue("electrical"),
			}, now)

			Convey("Then it still matches", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
			})
		})

		Convey("When no member matches", func() {
			ok, err := dep.MatchesAny([]valueobjects.Value{
				valueobjects.NewStringValue("electrical"),
				valueobjects.NewStringValue("gas"),
			}, now)
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
		})

		Convey("When the entity holds no value for the source", func() {
			Convey("Then it is evaluated once against the zero value, as before", func() {
				ok, err := dep.MatchesAny(nil, now)
				So(err, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})
	})
}

// TestRequiredConflictResolution covers the combination rule for two matched
// dependencies that disagree.
//
// Plain last-writer-wins resolved by repository return order, so the outcome
// depended on dependency creation order: the two backends could disagree for
// identical schemas, and editing one rule changed the behaviour of a rule its
// author never touched.
func TestRequiredConflictResolution(t *testing.T) {
	Convey("Given a target attribute and two rules over different sources", t, func() {
		hazardous := stringAttr("hazardous", false)
		indoor := stringAttr("indoor", false)
		target := stringAttr("permit", false)
		now := time.Now().UTC()

		demand := requiredDep(hazardous, target, "yes", true)
		relax := requiredDep(indoor, target, "yes", false)

		match := map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
			hazardous.ID(): {valueobjects.NewStringValue("yes")},
			indoor.ID():    {valueobjects.NewStringValue("yes")},
		}

		Convey("When both match, in either order", func() {
			forward, err := ResolveEffective(target, []*Dependency{demand, relax}, match, now)
			So(err, ShouldBeNil)
			reverse, err := ResolveEffective(target, []*Dependency{relax, demand}, match, now)
			So(err, ShouldBeNil)

			Convey("Then the result is required, and does not depend on the order", func() {
				So(forward.Required, ShouldBeTrue)
				So(reverse.Required, ShouldBeTrue)
			})
		})

		Convey("When only the relaxing rule matches", func() {
			only := map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
				indoor.ID(): {valueobjects.NewStringValue("yes")},
			}
			schema, err := ResolveEffective(target, []*Dependency{demand, relax}, only, now)

			Convey("Then it is not required", func() {
				So(err, ShouldBeNil)
				So(schema.Required, ShouldBeFalse)
			})
		})

		Convey("When the attribute is required by its own definition", func() {
			own := stringAttr("permit", true)
			relaxOwn := requiredDep(indoor, own, "yes", false)
			only := map[valueobjects.AttributeDefinitionID][]valueobjects.Value{
				indoor.ID(): {valueobjects.NewStringValue("yes")},
			}

			Convey("Then a matched rule can still relax it", func() {
				// A single override replaces the attribute's own flag; only
				// CONFLICTING overrides resolve to required.
				schema, err := ResolveEffective(own, []*Dependency{relaxOwn}, only, now)
				So(err, ShouldBeNil)
				So(schema.Required, ShouldBeFalse)
			})

			Convey("Then no matched rule leaves the definition's flag alone", func() {
				schema, err := ResolveEffective(own, []*Dependency{relaxOwn},
					map[valueobjects.AttributeDefinitionID][]valueobjects.Value{}, now)
				So(err, ShouldBeNil)
				So(schema.Required, ShouldBeTrue)
			})
		})
	})
}
