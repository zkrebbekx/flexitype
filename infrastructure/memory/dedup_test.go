package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestDuplicateDetection(t *testing.T) {
	Convey("Given products whose names nearly match", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		setName := func(entity, v string) {
			raw, _ := json.Marshal(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: name.ID.String(), EntityID: entity, TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		setName("e1", "Trail Bike 500")
		setName("e2", "trail bike 500 ") // trailing space + different case
		setName("e3", "Road Helmet XL")  // unrelated

		rule, err := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
			TypeDefinitionID:      typeID,
			AttributeDefinitionID: name.ID.String(),
			Strategy:              appdedup.StrategyTrigram,
			Threshold:             0.7,
		})
		So(err, ShouldBeNil)

		Convey("When the rule is scanned", func() {
			out, err := it.Dedup().Scan(ctx, rule.ID.String())

			Convey("Then the near-duplicate pairs and only they are reported", func() {
				So(err, ShouldBeNil)
				So(out.Candidates, ShouldHaveLength, 1)
				So(out.Candidates[0].EntityA, ShouldEqual, "e1")
				So(out.Candidates[0].EntityB, ShouldEqual, "e2")
				So(out.Candidates[0].Score, ShouldBeGreaterThanOrEqualTo, 0.7)
			})

			Convey("Then a second scan is idempotent", func() {
				So(err, ShouldBeNil)
				again, err := it.Dedup().Scan(ctx, rule.ID.String())
				So(err, ShouldBeNil)
				So(again.Candidates, ShouldResemble, out.Candidates)
			})
		})

		Convey("When a candidate pair is dismissed", func() {
			So(it.Dedup().Dismiss(ctx, rule.ID.String(), "e2", "e1"), ShouldBeNil)
			out, err := it.Dedup().Scan(ctx, rule.ID.String())

			Convey("Then it stays dismissed on re-scan", func() {
				So(err, ShouldBeNil)
				So(out.Candidates, ShouldBeEmpty)
			})
		})

		Convey("When an exact-match rule is scanned against differing case", func() {
			exact, err := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
				TypeDefinitionID: typeID, AttributeDefinitionID: name.ID.String(), Strategy: appdedup.StrategyExact,
			})
			So(err, ShouldBeNil)
			out, err := it.Dedup().Scan(ctx, exact.ID.String())

			Convey("Then no pair matches (values differ by case and space)", func() {
				So(err, ShouldBeNil)
				So(out.Candidates, ShouldBeEmpty)
			})
		})

		Convey("When two content-free values are trigram-scanned", func() {
			setName("e4", "!!!")
			setName("e5", "@@@")
			out, err := it.Dedup().Scan(ctx, rule.ID.String())

			Convey("Then they are not flagged as a perfect duplicate", func() {
				So(err, ShouldBeNil)
				for _, c := range out.Candidates {
					pair := c.EntityA + "," + c.EntityB
					So(pair, ShouldNotContainSubstring, "e4")
					So(pair, ShouldNotContainSubstring, "e5")
				}
			})
		})
	})
}
