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
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestExclusiveRangeBound proves a strict bound through the whole stack: a
// rule with an exclusive min must not fire on the bound value itself, and
// must fire just above it.
//
// The scenario is the one that motivated the flags: a pool whose
// `needs_inspection` becomes required — and pinned to true — once `capacity`
// is OVER 50000. An inclusive min cannot say "over": min 50000 fires AT
// 50000, and on a continuous type there is no next value to name instead.
func TestExclusiveRangeBound(t *testing.T) {
	Convey("Given a pool type with a capacity-over-50000 inspection rule", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		td, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "pool", DisplayName: "Pool"})
		So(err, ShouldBeNil)
		capacity, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: td.ID.String(), InternalName: "capacity",
			DisplayName: "Capacity", DataType: "integer",
		})
		So(err, ShouldBeNil)
		inspection, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: td.ID.String(), InternalName: "needs_inspection",
			DisplayName: "Needs inspection", DataType: "bool",
		})
		So(err, ShouldBeNil)

		_, err = it.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: capacity.ID.String(),
			TargetAttributeID: inspection.ID.String(),
			Conditions: json.RawMessage(
				`[{"kind":"range","min":{"type":"integer","value":50000},"min_exclusive":true}]`),
			Effect: json.RawMessage(`{"required":true,"allowed_values":[{"type":"bool","value":true}]}`),
		})
		So(err, ShouldBeNil)

		setCapacity := func(n string) {
			_, serr := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: capacity.ID.String(), EntityID: "p1",
				TypeDefinitionID: td.ID.String(), Value: json.RawMessage(n),
			})
			So(serr, ShouldBeNil)
		}

		Convey("When the capacity sits exactly on the bound", func() {
			setCapacity("50000")
			eff, eerr := it.Dependencies().EffectiveSchema(ctx, inspection.ID.String(), "p1")

			Convey("Then the rule does not fire", func() {
				So(eerr, ShouldBeNil)
				So(eff.Required, ShouldBeFalse)

				_, serr := it.Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: inspection.ID.String(), EntityID: "p1",
					TypeDefinitionID: td.ID.String(), Value: json.RawMessage(`false`),
				})
				So(serr, ShouldBeNil)
			})
		})

		Convey("When the capacity is one over the bound", func() {
			setCapacity("50001")
			eff, eerr := it.Dependencies().EffectiveSchema(ctx, inspection.ID.String(), "p1")

			Convey("Then the target is required and pinned to true", func() {
				So(eerr, ShouldBeNil)
				So(eff.Required, ShouldBeTrue)

				_, serr := it.Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: inspection.ID.String(), EntityID: "p1",
					TypeDefinitionID: td.ID.String(), Value: json.RawMessage(`false`),
				})
				So(serr, ShouldNotBeNil)

				_, serr = it.Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: inspection.ID.String(), EntityID: "p1",
					TypeDefinitionID: td.ID.String(), Value: json.RawMessage(`true`),
				})
				So(serr, ShouldBeNil)
			})
		})
	})
}
