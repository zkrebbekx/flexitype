package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestDependencySemanticsPostgres covers the four ways a dependency evaluated
// against something other than what its author wrote.
//
// All four are silent: no error at definition time, none at evaluation time,
// and a rule that behaves correctly on the day it is written can change
// behaviour later with no schema or data change to explain it.
func TestDependencySemanticsPostgres(t *testing.T) {
	pool := openTestDB(t)
	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a product type (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		attr := func(name, dataType string, multi bool, family string) string {
			a, err := ia.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
				DataType: dataType, MultiValued: multi, UnitFamilyID: family,
			})
			So(err, ShouldBeNil)
			return a.ID.String()
		}
		set := func(attrID, entity string, v any) error {
			raw, _ := json.Marshal(v)
			_, err := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity,
				TypeDefinitionID: typeID, Value: raw,
			})
			return err
		}

		Convey("A pattern constraint matches the whole value, not a substring", func() {
			code := attr("code", "string", false, "")
			_, err := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID:          code,
				DisplayName: "code",
				Constraints: json.RawMessage(`[{"kind":"pattern","expr":"[A-Z]{2}[0-9]{6}"}]`),
			})
			So(err, ShouldBeNil)

			Convey("Then a value that merely contains a match is rejected", func() {
				err := set(code, "p1", "junk XX123456 junk")
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})

			Convey("Then an exact value is accepted", func() {
				So(set(code, "p2", "XX123456"), ShouldBeNil)
			})

			Convey("Then an author who wants substring matching can say so", func() {
				_, err := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
					ID:          code,
					DisplayName: "code",
					Constraints: json.RawMessage(`[{"kind":"pattern","expr":"[A-Z]{2}[0-9]{6}","substring":true}]`),
				})
				So(err, ShouldBeNil)
				So(set(code, "p3", "junk XX123456 junk"), ShouldBeNil)
			})
		})

		Convey("A quantity operand in a rule is rebased against the unit family", func() {
			mass, err := ia.Units().Create(ctx, appunit.CreateInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
			})
			So(err, ShouldBeNil)
			weight := attr("weight", "quantity", false, mass.ID.String())
			hazard := attr("hazard_class", "string", false, "")

			// "if weight > 5 kg then hazard_class is required", written through
			// the API with no base — which is what a client can express.
			_, err = svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
				SourceAttributeID: weight,
				TargetAttributeID: hazard,
				Conditions: json.RawMessage(
					`[{"kind":"range","min":{"type":"quantity","value":{"magnitude":"5","unit":"kg"}},` +
						`"max":{"type":"quantity","value":{"magnitude":"1000","unit":"kg"}}}]`),
				Effect: json.RawMessage(`{"required":true}`),
			})
			So(err, ShouldBeNil)

			Convey("Then a light entity does not trigger the rule", func() {
				// With an un-rebased bound of 0 this fired for every weight.
				So(set(weight, "p1", map[string]any{"magnitude": "1", "unit": "kg"}), ShouldBeNil)
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, hazard, "p1")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeFalse)
			})

			Convey("Then a heavy entity does, whatever unit it was written in", func() {
				So(set(weight, "p2", map[string]any{"magnitude": "9000", "unit": "g"}), ShouldBeNil)
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, hazard, "p2")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
			})
		})

		Convey("An effect's quantity operands are rebased against the TARGET's family", func() {
			mass, err := ia.Units().Create(ctx, appunit.CreateInput{
				Name: "mass2", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
			})
			So(err, ShouldBeNil)
			fragile := attr("fragile", "bool", false, "")
			shipping := attr("shipping_weight", "quantity", false, mass.ID.String())

			// "if fragile then shipping_weight must be at most 2 kg" — the
			// bound lives in the effect, against the target's family.
			_, err = svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
				SourceAttributeID: fragile,
				TargetAttributeID: shipping,
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
				Effect: json.RawMessage(
					`{"constraints":[{"kind":"max_value","value":{"type":"quantity","value":{"magnitude":"2","unit":"kg"}}}]}`),
			})
			So(err, ShouldBeNil)
			So(set(fragile, "p1", true), ShouldBeNil)

			Convey("Then a value inside the bound is accepted", func() {
				So(set(shipping, "p1", map[string]any{"magnitude": "1500", "unit": "g"}), ShouldBeNil)
			})

			Convey("Then a value beyond it is rejected", func() {
				// With the bound's base left at 0 every weight exceeded it.
				err := set(shipping, "p1", map[string]any{"magnitude": "3", "unit": "kg"})
				So(err, ShouldNotBeNil)
			})
		})

		Convey("A quantity attribute with no unit family is refused in a rule", func() {
			bare := attr("bare_weight", "string", false, "")
			target := attr("note", "string", false, "")

			Convey("Then a rule over it is still accepted, because no operand needs rebasing", func() {
				_, err := svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
					SourceAttributeID: bare,
					TargetAttributeID: target,
					Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"x"}}]`),
					Effect:            json.RawMessage(`{"required":true}`),
				})
				So(err, ShouldBeNil)
			})
		})

		Convey("A multi-valued source matches when ANY member matches", func() {
			certs := attr("certifications", "string", true, "")
			plan := attr("disposal_plan", "string", false, "")

			_, err := svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
				SourceAttributeID: certs,
				TargetAttributeID: plan,
				Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"asbestos"}}]`),
				Effect:            json.RawMessage(`{"required":true}`),
			})
			So(err, ShouldBeNil)

			So(set(certs, "p1", "asbestos"), ShouldBeNil)

			Convey("Then the rule fires on the matching member", func() {
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, plan, "p1")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
			})

			Convey("Then adding a second, non-matching certification does not stop it", func() {
				// The source collapsed to the newest member, so adding an
				// unrelated certification silently switched the rule off.
				So(set(certs, "p1", "electrical"), ShouldBeNil)
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, plan, "p1")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
			})
		})

		Convey("Conflicting Required overrides resolve to required", func() {
			hazardous := attr("hazardous", "bool", false, "")
			indoor := attr("indoor", "bool", false, "")
			permit := attr("permit", "string", false, "")

			mk := func(source string, required bool) {
				effect := `{"required":true}`
				if !required {
					effect = `{"required":false}`
				}
				_, err := svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
					SourceAttributeID: source,
					TargetAttributeID: permit,
					Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
					Effect:            json.RawMessage(effect),
				})
				So(err, ShouldBeNil)
			}
			mk(hazardous, true)
			mk(indoor, false)

			So(set(hazardous, "p1", true), ShouldBeNil)
			So(set(indoor, "p1", true), ShouldBeNil)

			Convey("Then the entity matching both rules requires the permit", func() {
				// Last-writer-wins resolved by store order, so the outcome
				// depended on which dependency was created last — and editing
				// one rule changed the behaviour of the other.
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, permit, "p1")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)
			})

			Convey("Then an entity matching only the relaxing rule does not", func() {
				So(set(indoor, "p2", true), ShouldBeNil)
				eff, err := svc.Interactors(ctx).Dependencies().EffectiveSchema(ctx, permit, "p2")
				So(err, ShouldBeNil)
				So(eff.Required, ShouldBeFalse)
			})
		})
	})
}
