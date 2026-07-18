package memory_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
)

// These tests drive the in-memory FQL evaluator through the query interactor.
// Each leaf asserts the exact entity set a predicate selects, so the operator
// semantics (not merely the code path) are pinned.

func TestFQLComparisonOperators(t *testing.T) {
	Convey("Given two products priced 1499 and 799", t, func() {
		h := newHarness()
		productID := h.createType("product", "")
		nameAttr := h.createAttr(productID, "name", "string")
		priceAttr := h.createAttr(productID, "price", "integer")

		h.setValue(nameAttr, "sku-1", productID, "Trail Bike")
		h.setValue(priceAttr, "sku-1", productID, 1499)
		h.setValue(nameAttr, "sku-2", productID, "City Bike")
		h.setValue(priceAttr, "sku-2", productID, 799)

		Convey("When each ordering operator is applied to the integer attribute", func() {
			Convey("Then every operator selects exactly the matching side of the boundary", func() {
				So(entityIDs(h.query("product", `price > 799`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `price >= 1499`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `price < 1499`)), ShouldResemble, []string{"sku-2"})
				So(entityIDs(h.query("product", `price <= 799`)), ShouldResemble, []string{"sku-2"})
				So(entityIDs(h.query("product", `price = 1499`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `price != 1499`)), ShouldResemble, []string{"sku-2"})
			})
		})

		Convey("When an ordering operator is applied to a string attribute", func() {
			// The binder rejects it up front, so the evaluator's lexical
			// fallback is never reached through a validated query.
			err := h.interactors().Query().Validate(h.ctx, "product", `name > "D"`)

			Convey("Then the query is rejected rather than silently ordering text", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "ordering comparisons")
			})
		})

		Convey("When count() is compared with each operator", func() {
			// sku-3 has a name but no price: count(price) is 0 for it and 1 for
			// the others — enough to exercise every integer operator.
			h.setValue(nameAttr, "sku-3", productID, "Gravel Bike")

			Convey("Then count() is a definite integer comparison, never NULL", func() {
				So(entityIDs(h.query("product", `count(price) = 0`)), ShouldResemble, []string{"sku-3"})
				So(entityIDs(h.query("product", `count(price) != 0`)), ShouldHaveLength, 2)
				So(entityIDs(h.query("product", `count(price) > 0`)), ShouldHaveLength, 2)
				So(entityIDs(h.query("product", `count(price) >= 1`)), ShouldHaveLength, 2)
				So(entityIDs(h.query("product", `count(price) < 1`)), ShouldResemble, []string{"sku-3"})
				So(entityIDs(h.query("product", `count(price) <= 0`)), ShouldResemble, []string{"sku-3"})
			})
		})

		Convey("When length() is compared with each operator", func() {
			// "Trail Bike" is 10 characters, "City Bike" is 9.
			Convey("Then the value's character length drives the comparison", func() {
				So(entityIDs(h.query("product", `length(name) = 10`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `length(name) != 10`)), ShouldResemble, []string{"sku-2"})
				So(entityIDs(h.query("product", `length(name) > 9`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `length(name) >= 10`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `length(name) < 10`)), ShouldResemble, []string{"sku-2"})
				So(entityIDs(h.query("product", `length(name) <= 9`)), ShouldResemble, []string{"sku-2"})
			})
		})

		Convey("When max() is compared", func() {
			Convey("Then it behaves like the scalar subquery over the entity's rows", func() {
				So(entityIDs(h.query("product", `max(price) >= 1499`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `max(price) < 1000`)), ShouldResemble, []string{"sku-2"})
			})
		})
	})
}

func TestFQLPredicateForms(t *testing.T) {
	Convey("Given products with names, prices and optional descriptions", t, func() {
		h := newHarness()
		productID := h.createType("product", "")
		nameAttr := h.createAttr(productID, "name", "string")
		priceAttr := h.createAttr(productID, "price", "integer")
		descAttr := h.createAttr(productID, "description", "string")

		h.setValue(nameAttr, "sku-1", productID, "Trail Bike")
		h.setValue(priceAttr, "sku-1", productID, 1499)
		h.setValue(descAttr, "sku-1", productID, "A rugged mountain bike")

		h.setValue(nameAttr, "sku-2", productID, "City Bike")
		h.setValue(priceAttr, "sku-2", productID, 799)
		// sku-2 deliberately has no description.

		Convey("When an in() set membership test runs", func() {
			Convey("Then only entities whose value is a listed member match", func() {
				So(entityIDs(h.query("product", `price in (799, 500)`)), ShouldResemble, []string{"sku-2"})
				So(h.query("product", `price in (1, 2)`).Items, ShouldBeEmpty)
			})
		})

		Convey("When an inclusive range() test runs", func() {
			Convey("Then both bounds are inclusive and outside values are excluded", func() {
				So(entityIDs(h.query("product", `range(price, 799, 799)`)), ShouldResemble, []string{"sku-2"})
				So(entityIDs(h.query("product", `range(price, 800, 2000)`)), ShouldResemble, []string{"sku-1"})
				So(h.query("product", `range(price, 0, 100)`).Items, ShouldBeEmpty)
			})
		})

		Convey("When has() tests for the presence of a live value", func() {
			Convey("Then only the entity actually holding a value matches", func() {
				So(entityIDs(h.query("product", `has(description)`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `not has(description)`)), ShouldResemble, []string{"sku-2"})
			})
		})

		Convey("When the textual predicates run", func() {
			Convey("Then contains is case-sensitive while icontains and iequals are not", func() {
				So(entityIDs(h.query("product", `contains(name, "Trail")`)), ShouldResemble, []string{"sku-1"})
				So(h.query("product", `contains(name, "trail")`).Items, ShouldBeEmpty)
				So(entityIDs(h.query("product", `icontains(name, "TRAIL")`)), ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `iequals(name, "city bike")`)), ShouldResemble, []string{"sku-2"})
				So(h.query("product", `iequals(name, "city")`).Items, ShouldBeEmpty) // equality, not prefix
			})
		})

		Convey("When predicates are joined with and/or and negated", func() {
			Convey("Then Kleene logic selects the expected entities", func() {
				So(entityIDs(h.query("product", `price > 1000 and contains(name, "Bike")`)),
					ShouldResemble, []string{"sku-1"})
				So(h.query("product", `price > 1000 and price < 500`).Items, ShouldBeEmpty)
				So(entityIDs(h.query("product", `price = 799 or price = 1499`)), ShouldHaveLength, 2)
				So(entityIDs(h.query("product", `not (price > 1000)`)), ShouldResemble, []string{"sku-2"})
			})
		})
	})
}

func TestFQLTraversalDirections(t *testing.T) {
	Convey("Given products linked to suppliers through a directed relationship", t, func() {
		h := newHarness()
		productID := h.createType("product", "")
		supplierID := h.createType("supplier", "")
		nameAttr := h.createAttr(productID, "name", "string")
		regionAttr := h.createAttr(supplierID, "region", "string")

		rels := h.interactors().Relationships()
		def, err := rels.CreateDefinition(h.ctx, apprelationship.CreateDefinitionInput{
			InternalName: "supplied_by", DisplayName: "Supplied by",
			ParentTypeID: productID, ChildTypeID: supplierID,
		})
		So(err, ShouldBeNil)

		h.setValue(nameAttr, "sku-1", productID, "Trail Bike")
		h.setValue(nameAttr, "sku-2", productID, "City Bike")
		h.setValue(nameAttr, "sku-3", productID, "Unlinked Bike") // no link at all
		h.setValue(regionAttr, "acme", supplierID, "EU")
		h.setValue(regionAttr, "globex", supplierID, "US")

		link := func(parent, child string) {
			_, e := rels.Link(h.ctx, apprelationship.LinkInput{
				DefinitionID: def.ID.String(), ParentEntity: parent, ChildEntity: child,
			})
			So(e, ShouldBeNil)
		}
		link("sku-1", "acme")
		link("sku-2", "globex")

		Convey("When traversing from the parent side with child()", func() {
			Convey("Then only parents with a matching child-side counterpart are selected", func() {
				So(entityIDs(h.query("product", `child(supplied_by) { region = "EU" }`)),
					ShouldResemble, []string{"sku-1"})
				So(entityIDs(h.query("product", `child(supplied_by) { region = "US" }`)),
					ShouldResemble, []string{"sku-2"})
				// The unlinked product has no counterpart, so the EXISTS is false.
				So(entityIDs(h.query("product", `child(supplied_by) { has(region) }`)),
					ShouldNotContain, "sku-3")
			})
		})

		Convey("When traversing from the child side with parent()", func() {
			Convey("Then the inner expression evaluates against the parent-side counterpart", func() {
				So(entityIDs(h.query("supplier", `parent(supplied_by) { name = "Trail Bike" }`)),
					ShouldResemble, []string{"acme"})
				So(h.query("supplier", `parent(supplied_by) { name = "Unlinked Bike" }`).Items,
					ShouldBeEmpty)
			})
		})

		Convey("When traversing with the direction-agnostic linked()", func() {
			Convey("Then it matches from either end and evaluates the opposite one", func() {
				So(entityIDs(h.query("product", `linked(supplied_by) { region = "US" }`)),
					ShouldResemble, []string{"sku-2"})
				So(entityIDs(h.query("supplier", `linked(supplied_by) { name = "City Bike" }`)),
					ShouldResemble, []string{"globex"})
			})
		})
	})
}
