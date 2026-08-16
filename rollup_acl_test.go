package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestRollupCannotReadWhatTheCallerCannotRead covers issue #585.
//
// A rollup is materialized under system access and its result is stored as an
// ordinary attribute, born with no restriction of its own. So a principal
// denied an attribute could aggregate it and read the answer: sum leaks the
// total, and min and max republish an exact hidden value verbatim.
//
// The same hole was closed on the formula path in #509 and reopened here when
// rollups arrived, which is why the fix is the formula path's shape.
func TestRollupCannotReadWhatTheCallerCannotRead(t *testing.T) {
	Convey("Given a line type whose cost the caller may not read", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		dish, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "dish", DisplayName: "Dish",
		})
		So(err, ShouldBeNil)
		line, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "line", DisplayName: "Line",
		})
		So(err, ShouldBeNil)
		_, err = ia.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: line.ID.String(), InternalName: "cost",
			DisplayName: "Cost", DataType: "decimal",
		})
		So(err, ShouldBeNil)
		_, err = ia.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "has_line", DisplayName: "Has line",
			ParentTypeID: dish.ID.String(), ChildTypeID: line.ID.String(),
		})
		So(err, ShouldBeNil)

		restricted := uow.WithAccess(ctx, uow.Access{
			Attr:    map[string]uow.Perm{"cost": uow.PermNone},
			Default: uow.PermWrite,
		})

		rollup := func(aggregate string) error {
			_, cerr := svc.Interactors(restricted).Attributes().Create(restricted, appattribute.CreateInput{
				TypeDefinitionID: dish.ID.String(),
				InternalName:     "total_" + aggregate, DisplayName: "Total",
				DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line",` +
					`"direction":"child","aggregate":"` + aggregate + `","target":"cost"}}`),
			})
			return cerr
		}

		Convey("When that caller defines a rollup over it", func() {
			Convey("Then sum is refused", func() {
				So(rollup("sum"), ShouldNotBeNil)
			})

			Convey("Then min is refused — it would republish an exact hidden value", func() {
				So(rollup("min"), ShouldNotBeNil)
			})

			Convey("Then max is refused", func() {
				So(rollup("max"), ShouldNotBeNil)
			})
		})

		Convey("When that caller defines a count rollup, which reads no field", func() {
			_, cerr := svc.Interactors(restricted).Attributes().Create(restricted, appattribute.CreateInput{
				TypeDefinitionID: dish.ID.String(), InternalName: "line_count",
				DisplayName: "Lines", DataType: "integer",
				Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line",` +
					`"direction":"child","aggregate":"count"}}`),
			})

			Convey("Then it is allowed — cardinality is not a field value", func() {
				So(cerr, ShouldBeNil)
			})
		})

		Convey("When an unrestricted caller defines the same rollup", func() {
			err := rollup("sum")

			Convey("Then it is allowed", func() {
				_, aerr := ia.Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: dish.ID.String(), InternalName: "admin_total",
					DisplayName: "Total", DataType: "decimal",
					Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line",` +
						`"direction":"child","aggregate":"sum","target":"cost"}}`),
				})
				So(aerr, ShouldBeNil)
				So(err, ShouldNotBeNil) // the restricted one still refused
			})
		})

		Convey("When a restricted caller names the WRONG DIRECTION over a real relationship", func() {
			// The direction branches returned their own message while every
			// other way to fail collapsed into one. That told a restricted
			// caller which relationships exist and which way round they run —
			// a real relationship with the wrong direction answered
			// differently from a relationship that is not there at all.
			_, wrongWay := svc.Interactors(restricted).Attributes().Create(restricted, appattribute.CreateInput{
				TypeDefinitionID: dish.ID.String(), InternalName: "total_reversed",
				DisplayName: "Total", DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line",` +
					`"direction":"parent","aggregate":"sum","target":"cost"}}`),
			})
			_, noSuchRel := svc.Interactors(restricted).Attributes().Create(restricted, appattribute.CreateInput{
				TypeDefinitionID: dish.ID.String(), InternalName: "total_absent",
				DisplayName: "Total", DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"no_such_relationship",` +
					`"direction":"child","aggregate":"sum","target":"cost"}}`),
			})

			Convey("Then it reads the same as a relationship that does not exist", func() {
				So(wrongWay, ShouldNotBeNil)
				So(noSuchRel, ShouldNotBeNil)
				So(wrongWay.Error(), ShouldEqual, noSuchRel.Error())
			})
		})

		Convey("When a restricted caller names a target that does not exist", func() {
			_, missing := svc.Interactors(restricted).Attributes().Create(restricted, appattribute.CreateInput{
				TypeDefinitionID: dish.ID.String(), InternalName: "total_ghost",
				DisplayName: "Total", DataType: "decimal",
				Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line",` +
					`"direction":"child","aggregate":"sum","target":"no_such_attribute"}}`),
			})
			hidden := rollup("sum")

			Convey("Then it cannot tell that from a target it may not read", func() {
				// Distinguishable outcomes would let it enumerate the
				// counterpart type's attribute names.
				So(missing, ShouldNotBeNil)
				So(hidden, ShouldNotBeNil)
				So(hidden.Error(), ShouldEqual, missing.Error())
			})
		})
	})
}
