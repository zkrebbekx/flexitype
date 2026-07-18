package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func TestChangeSetListing(t *testing.T) {
	Convey("Given two change-sets in one tenant", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := flexitype.NewInMemory().Interactors(ctx)

		first, err := it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "spring prices"})
		So(err, ShouldBeNil)
		_, err = it.ChangeSets().Create(ctx, appchangeset.CreateInput{Name: "autumn prices"})
		So(err, ShouldBeNil)

		Convey("When they are listed", func() {
			items, err := it.ChangeSets().List(ctx)

			Convey("Then both come back as drafts", func() {
				So(err, ShouldBeNil)
				So(items, ShouldHaveLength, 2)
				for _, cs := range items {
					So(cs.State, ShouldEqual, appchangeset.StateDraft)
				}
			})
		})

		Convey("When another tenant lists change-sets", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			items, err := it.ChangeSets().List(other)

			Convey("Then nothing crosses the tenant boundary", func() {
				So(err, ShouldBeNil)
				So(items, ShouldBeEmpty)
			})
		})

		Convey("When one is rejected", func() {
			rejected, err := it.ChangeSets().Reject(ctx, first.ID.String())
			So(err, ShouldBeNil)

			Convey("Then its state is reflected in the listing", func() {
				So(rejected.State, ShouldEqual, appchangeset.StateRejected)
				items, err := it.ChangeSets().List(ctx)
				So(err, ShouldBeNil)
				states := map[string]appchangeset.State{}
				for _, cs := range items {
					states[cs.ID.String()] = cs.State
				}
				So(states[first.ID.String()], ShouldEqual, appchangeset.StateRejected)
			})
		})

		Convey("When a change-set with no mutations is submitted", func() {
			_, err := it.ChangeSets().Submit(ctx, first.ID.String())

			Convey("Then it is refused as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "no mutations")
			})
		})

		Convey("When an unknown mutation kind is added", func() {
			_, err := it.ChangeSets().AddMutation(ctx, first.ID.String(),
				appvalue.Mutation{Kind: "rename", EntityID: "p1"})

			Convey("Then it is refused as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown mutation kind")
			})
		})

		Convey("When a malformed or unknown change-set is fetched", func() {
			_, badErr := it.ChangeSets().Get(ctx, "nope")
			_, missingErr := it.ChangeSets().Get(ctx, ulid.New().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})
	})
}

func TestDedupRuleManagement(t *testing.T) {
	Convey("Given a product type with a name attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := flexitype.NewInMemory().Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name",
			DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		rule, err := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
			TypeDefinitionID:      product.ID.String(),
			AttributeDefinitionID: name.ID.String(),
			Strategy:              appdedup.StrategyExact,
		})
		So(err, ShouldBeNil)

		Convey("When the type's rules are listed", func() {
			rules, err := it.Dedup().ListRules(ctx, product.ID.String())

			Convey("Then the stored rule comes back", func() {
				So(err, ShouldBeNil)
				So(rules, ShouldHaveLength, 1)
				So(rules[0].ID.String(), ShouldEqual, rule.ID.String())
				So(rules[0].Strategy, ShouldEqual, appdedup.StrategyExact)
			})
		})

		Convey("When another tenant lists the rules", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			rules, err := it.Dedup().ListRules(other, product.ID.String())

			Convey("Then nothing crosses the tenant boundary", func() {
				So(err, ShouldBeNil)
				So(rules, ShouldBeEmpty)
			})
		})

		Convey("When the rule is deleted", func() {
			err := it.Dedup().DeleteRule(ctx, rule.ID.String())

			Convey("Then it disappears from the listing", func() {
				So(err, ShouldBeNil)
				rules, err := it.Dedup().ListRules(ctx, product.ID.String())
				So(err, ShouldBeNil)
				So(rules, ShouldBeEmpty)
			})
		})

		Convey("When a delete or dismiss names a malformed rule", func() {
			deleteErr := it.Dedup().DeleteRule(ctx, "nope")
			dismissErr := it.Dedup().Dismiss(ctx, "nope", "a", "b")

			Convey("Then both are rejected as validation", func() {
				So(domainerrors.IsValidation(deleteErr), ShouldBeTrue)
				So(domainerrors.IsValidation(dismissErr), ShouldBeTrue)
			})
		})

		Convey("When a dismiss names an unknown rule", func() {
			err := it.Dedup().Dismiss(ctx, ulid.New().String(), "a", "b")

			Convey("Then it is a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a rule names an attribute outside the type's schema", func() {
			other, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
				InternalName: "vendor", DisplayName: "Vendor",
			})
			So(err, ShouldBeNil)
			foreign, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: other.ID.String(), InternalName: "tier",
				DisplayName: "Tier", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
				TypeDefinitionID:      product.ID.String(),
				AttributeDefinitionID: foreign.ID.String(),
				Strategy:              appdedup.StrategyExact,
			})

			Convey("Then the rule is refused", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a rule carries a malformed type or attribute ID", func() {
			_, typeErr := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
				TypeDefinitionID: "nope", AttributeDefinitionID: name.ID.String(),
				Strategy: appdedup.StrategyExact,
			})
			_, attrErr := it.Dedup().CreateRule(ctx, appdedup.CreateRuleInput{
				TypeDefinitionID: product.ID.String(), AttributeDefinitionID: "nope",
				Strategy: appdedup.StrategyExact,
			})

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(attrErr), ShouldBeTrue)
			})
		})
	})
}

func TestTypeCompleteness(t *testing.T) {
	Convey("Given a product type with one required attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := flexitype.NewInMemory().Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		attr := func(name string, required bool) string {
			snap, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
				DataType: "string", Required: required,
			})
			So(e, ShouldBeNil)
			return snap.ID.String()
		}
		sku := attr("sku", true)
		note := attr("note", false)

		set := func(attrID, entityID, v string) {
			raw, e := json.Marshal(v)
			So(e, ShouldBeNil)
			_, e = it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entityID,
				TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}

		// p1 is complete; p2 holds only an optional value.
		set(sku, "p1", "ABC")
		set(note, "p2", "just a note")

		Convey("When the type is scored", func() {
			out, err := it.Dependencies().TypeCompleteness(ctx, typeID)

			Convey("Then complete and incomplete entities are split and averaged", func() {
				So(err, ShouldBeNil)
				So(out.TypeDefinitionID, ShouldEqual, typeID)
				So(out.Count, ShouldEqual, 2)
				So(out.Scored, ShouldEqual, 2)
				So(out.Truncated, ShouldBeFalse)
				So(out.Complete, ShouldEqual, 1)
				So(out.Incomplete, ShouldEqual, 1)
				So(out.AverageScore, ShouldAlmostEqual, 0.5)
				So(out.Entities, ShouldHaveLength, 2)

				scores := map[string]float64{}
				for _, e := range out.Entities {
					scores[e.EntityID] = e.Score
				}
				So(scores["p1"], ShouldAlmostEqual, 1.0)
				So(scores["p2"], ShouldAlmostEqual, 0.0)
			})
		})

		Convey("When the type ID is malformed or unknown", func() {
			_, badErr := it.Dependencies().TypeCompleteness(ctx, "nope")
			_, missingErr := it.Dependencies().TypeCompleteness(ctx,
				valueobjects.NewTypeDefinitionID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant scores the type", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := it.Dependencies().TypeCompleteness(other, typeID)

			Convey("Then it is refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})
}
