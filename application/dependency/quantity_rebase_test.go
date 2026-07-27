package dependency

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	appunit "github.com/zkrebbekx/flexitype/application/unit"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// stubUnits serves one mass family, or an error for an unknown id.
type stubUnits struct {
	id     ulid.ID
	family appunit.Family
}

func (s *stubUnits) Get(_ context.Context, _ valueobjects.TenantID, id ulid.ID) (appunit.Family, error) {
	if id != s.id {
		return appunit.Family{}, domainerrors.NewNotFound("unit_family", id.String())
	}
	return s.family, nil
}

// quantityAttr builds a quantity attribute pinned to a unit family.
func quantityAttr(name, familyID string) *domainattribute.Definition {
	def, _, err := domainattribute.New(domainattribute.NewInput{
		TenantID:         valueobjects.DefaultTenant,
		TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
		InternalName:     name,
		DisplayName:      name,
		DataType:         valueobjects.DataTypeQuantity,
		UnitFamilyID:     familyID,
	}, time.Now().UTC())
	So(err, ShouldBeNil)
	return def
}

// textAttr builds a plain string attribute, which needs no rebasing.
func textAttr(name string) *domainattribute.Definition {
	def, _, err := domainattribute.New(domainattribute.NewInput{
		TenantID:         valueobjects.DefaultTenant,
		TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
		InternalName:     name,
		DisplayName:      name,
		DataType:         valueobjects.DataTypeString,
	}, time.Now().UTC())
	So(err, ShouldBeNil)
	return def
}

// TestNormalizeQuantityOperands covers the rebase that attribute constraints
// had and dependency rules did not.
//
// ParseValue stores whatever base the caller supplied — 0 by default — and
// condition matching compares on that base. So a bound written as "5 kg"
// through the API compared as 0 and matched every weight, while a wrong base
// made the rule never fire. Both directions are silent.
func TestNormalizeQuantityOperands(t *testing.T) {
	famID := ulid.New()
	units := &stubUnits{id: famID, family: appunit.Family{
		Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
	}}

	// q builds a quantity value with a deliberately wrong base, the way an
	// API client's JSON arrives.
	q := func(magnitude, unit string, base float64) valueobjects.Value {
		v, err := valueobjects.NewQuantityValue(magnitude, unit, base)
		So(err, ShouldBeNil)
		return v
	}

	Convey("Given a rule over a quantity source and a quantity target", t, func() {
		i := &Interactor{units: units}
		source := quantityAttr("weight", famID.String())
		target := quantityAttr("shipping_weight", famID.String())

		min5kg := q("5", "kg", 0)
		max9kg := q("9", "kg", 999999)
		conditions := []domaindependency.Condition{{
			Kind: domaindependency.CondRange, Min: &min5kg, Max: &max9kg,
		}}
		effect := domaindependency.Effect{
			Constraints: domainattribute.Constraints{
				domainattribute.MaxValue{Max: q("2", "kg", 0)},
			},
		}

		err := i.normalizeQuantityOperands(context.Background(), source, target, conditions, &effect)

		Convey("Then a zero base is computed from the magnitude and unit", func() {
			So(err, ShouldBeNil)
			So(conditions[0].Min.Quantity().Base, ShouldEqual, 5000.0)
		})

		Convey("Then a caller-supplied base is discarded, not trusted", func() {
			So(conditions[0].Max.Quantity().Base, ShouldEqual, 9000.0)
		})

		Convey("Then the effect's nested constraints are rebased against the target", func() {
			maxV, ok := effect.Constraints[0].(domainattribute.MaxValue)
			So(ok, ShouldBeTrue)
			So(maxV.Max.Quantity().Base, ShouldEqual, 2000.0)
		})
	})

	Convey("Given a rule whose effect restricts allowed values", t, func() {
		i := &Interactor{units: units}
		source := textAttr("grade")
		target := quantityAttr("weight", famID.String())
		effect := domaindependency.Effect{AllowedValues: []valueobjects.Value{q("1", "kg", 0)}}

		err := i.normalizeQuantityOperands(context.Background(), source, target, nil, &effect)

		Convey("Then each allowed value is rebased", func() {
			So(err, ShouldBeNil)
			So(effect.AllowedValues[0].Quantity().Base, ShouldEqual, 1000.0)
		})
	})

	Convey("Given a rule over non-quantity attributes", t, func() {
		i := &Interactor{units: nil} // no unit store configured at all
		v := valueobjects.NewStringValue("x")
		conditions := []domaindependency.Condition{{Kind: domaindependency.CondEquals, Value: &v}}

		err := i.normalizeQuantityOperands(context.Background(), textAttr("attr_a"), textAttr("attr_b"),
			conditions, &domaindependency.Effect{})

		Convey("Then nothing needs rebasing and the rule is accepted", func() {
			So(err, ShouldBeNil)
		})
	})

	Convey("Given a quantity attribute in a deployment with no unit families", t, func() {
		i := &Interactor{units: nil}

		err := i.normalizeQuantityOperands(context.Background(),
			quantityAttr("weight", famID.String()), textAttr("note"), nil, &domaindependency.Effect{})

		Convey("Then the rule is refused rather than stored un-rebased", func() {
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "unit families are not configured")
		})
	})

	Convey("Given a quantity attribute pinned to no unit family", t, func() {
		i := &Interactor{units: units}

		err := i.normalizeQuantityOperands(context.Background(),
			quantityAttr("weight", ""), textAttr("note"), nil, &domaindependency.Effect{})

		Convey("Then the rule is refused, naming the attribute", func() {
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "requires a unit family")
		})
	})

	Convey("Given a quantity attribute pinned to a family that does not exist", t, func() {
		i := &Interactor{units: units}

		err := i.normalizeQuantityOperands(context.Background(),
			quantityAttr("weight", ulid.New().String()), textAttr("note"), nil, &domaindependency.Effect{})

		Convey("Then the lookup failure surfaces rather than being swallowed", func() {
			So(err, ShouldNotBeNil)
		})
	})

	Convey("Given a rule with no effect to normalize", t, func() {
		i := &Interactor{units: units}

		err := i.normalizeQuantityOperands(context.Background(),
			textAttr("attr_a"), textAttr("attr_b"), nil, nil)

		Convey("Then it is a no-op rather than a nil dereference", func() {
			So(err, ShouldBeNil)
		})
	})
}
