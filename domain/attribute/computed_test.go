package attribute

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
)

func TestComputedValidate(t *testing.T) {
	Convey("Given a computed attribute's derivation spec", t, func() {
		Convey("When it derives from a formula", func() {
			Convey("Then a well-formed formula validates and reports its references", func() {
				refs, err := (&Computed{Kind: ComputedFormula, Formula: "width * height"}).Validate()
				So(err, ShouldBeNil)
				So(refs, ShouldResemble, []string{"width", "height"})
			})

			Convey("Then an empty formula is rejected", func() {
				_, err := (&Computed{Kind: ComputedFormula}).Validate()
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "required")
			})

			Convey("Then an unparseable formula is rejected", func() {
				_, err := (&Computed{Kind: ComputedFormula, Formula: "width *"}).Validate()
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "invalid formula")
			})
		})

		Convey("When it derives from a relationship rollup", func() {
			Convey("Then count needs no target attribute", func() {
				refs, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
					Relationship: "uses", Direction: "child", Aggregate: RollupCount,
				}}).Validate()
				So(err, ShouldBeNil)
				So(refs, ShouldBeEmpty) // rollups declare no formula references
			})

			Convey("Then sum, min and max validate with a target attribute", func() {
				for _, agg := range []RollupAggregate{RollupSum, RollupMin, RollupMax} {
					_, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
						Relationship: "uses", Direction: "parent", Aggregate: agg, Target: "weight",
					}}).Validate()
					So(err, ShouldBeNil)
				}
			})

			Convey("Then sum, min and max without a target are rejected", func() {
				for _, agg := range []RollupAggregate{RollupSum, RollupMin, RollupMax} {
					_, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
						Relationship: "uses", Direction: "child", Aggregate: agg,
					}}).Validate()
					So(domainerrors.IsValidation(err), ShouldBeTrue)
					So(err.Error(), ShouldContainSubstring, "target")
				}
			})

			Convey("Then a missing rollup spec is rejected", func() {
				_, err := (&Computed{Kind: ComputedRollup}).Validate()
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})

			Convey("Then a missing relationship name is rejected", func() {
				_, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
					Direction: "child", Aggregate: RollupCount,
				}}).Validate()
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "relationship")
			})

			Convey("Then every documented traversal direction is accepted", func() {
				for _, dir := range []string{"child", "parent", "linked"} {
					_, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
						Relationship: "uses", Direction: dir, Aggregate: RollupCount,
					}}).Validate()
					So(err, ShouldBeNil)
				}
			})

			Convey("Then an unknown direction is rejected", func() {
				_, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
					Relationship: "uses", Direction: "sideways", Aggregate: RollupCount,
				}}).Validate()
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "direction")
			})

			Convey("Then an unknown aggregate is rejected", func() {
				_, err := (&Computed{Kind: ComputedRollup, Rollup: &Rollup{
					Relationship: "uses", Direction: "child", Aggregate: RollupAggregate("median"),
				}}).Validate()
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "aggregate")
			})
		})

		Convey("When the computed kind is unknown", func() {
			_, err := (&Computed{Kind: ComputedKind("astrology")}).Validate()

			Convey("Then it is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "computed kind")
			})
		})
	})
}
