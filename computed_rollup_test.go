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
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// rollupFixture is a recipe-costing model: the smallest thing that needs every
// part of a rollup at once.
//
//	ingredient.cost_per_kg      plain
//	line.ingredient_cost        rollup  sum(parent(of_ingredient).cost_per_kg)
//	line.line_cost              formula quantity_kg * ingredient_cost
//	dish.food_cost              rollup  sum(child(has_line).line_cost)
//	dish.line_count             rollup  count(child(has_line))
type rollupFixture struct {
	svc                                 *flexitype.Service
	ctx                                 context.Context
	ingredient, line, dish              string
	costPerKg, quantityKg               string
	ofIngredient, hasLine               string
	foodCost, lineCount, lineCostAttrID string
	ingredientCostAttrID                string
}

func newRollupFixture(t *testing.T, svc *flexitype.Service) *rollupFixture {
	t.Helper()
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	f := &rollupFixture{svc: svc, ctx: ctx}

	newType := func(name string) string {
		out, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: name, DisplayName: name,
		})
		So(err, ShouldBeNil)
		return out.ID.String()
	}
	newAttr := func(in appattribute.CreateInput) string {
		out, err := svc.Interactors(ctx).Attributes().Create(ctx, in)
		So(err, ShouldBeNil)
		return out.ID.String()
	}
	newRel := func(name, parentType, childType string) string {
		out, err := svc.Interactors(ctx).Relationships().CreateDefinition(ctx,
			apprelationship.CreateDefinitionInput{
				InternalName: name, DisplayName: name, Kind: "directed",
				ParentTypeID: parentType, ChildTypeID: childType,
			})
		So(err, ShouldBeNil)
		return out.ID.String()
	}

	f.ingredient = newType("ingredient")
	f.line = newType("recipe_line")
	f.dish = newType("dish")

	f.costPerKg = newAttr(appattribute.CreateInput{
		TypeDefinitionID: f.ingredient, InternalName: "cost_per_kg",
		DisplayName: "Cost per kg", DataType: "decimal",
	})
	f.ofIngredient = newRel("of_ingredient", f.ingredient, f.line)
	f.hasLine = newRel("has_line", f.dish, f.line)

	f.quantityKg = newAttr(appattribute.CreateInput{
		TypeDefinitionID: f.line, InternalName: "quantity_kg",
		DisplayName: "Quantity (kg)", DataType: "decimal",
	})
	f.ingredientCostAttrID = newAttr(appattribute.CreateInput{
		TypeDefinitionID: f.line, InternalName: "ingredient_cost",
		DisplayName: "Ingredient cost", DataType: "decimal",
		Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"of_ingredient","direction":"parent","aggregate":"sum","target":"cost_per_kg"}}`),
	})
	f.lineCostAttrID = newAttr(appattribute.CreateInput{
		TypeDefinitionID: f.line, InternalName: "line_cost",
		DisplayName: "Line cost", DataType: "decimal",
		Computed: json.RawMessage(`{"kind":"formula","formula":"quantity_kg * ingredient_cost"}`),
	})
	f.foodCost = newAttr(appattribute.CreateInput{
		TypeDefinitionID: f.dish, InternalName: "food_cost",
		DisplayName: "Food cost", DataType: "decimal",
		Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line","direction":"child","aggregate":"sum","target":"line_cost"}}`),
	})
	f.lineCount = newAttr(appattribute.CreateInput{
		TypeDefinitionID: f.dish, InternalName: "line_count",
		DisplayName: "Lines", DataType: "integer",
		Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"has_line","direction":"child","aggregate":"count"}}`),
	})
	return f
}

func (f *rollupFixture) set(attrID, typeID, entity, raw string) {
	_, err := f.svc.Interactors(f.ctx).Values().Set(f.ctx, appvalue.SetInput{
		AttributeDefinitionID: attrID, EntityID: entity,
		TypeDefinitionID: typeID, Value: json.RawMessage(raw),
	})
	So(err, ShouldBeNil)
}

func (f *rollupFixture) link(defID, parent, child string) {
	_, err := f.svc.Interactors(f.ctx).Relationships().Link(f.ctx, apprelationship.LinkInput{
		DefinitionID: defID, ParentEntity: parent, ChildEntity: child,
	})
	So(err, ShouldBeNil)
}

// value reads one attribute of one entity in its wire form, or "" when absent.
func (f *rollupFixture) value(typeID, entity, attrID string) string {
	values, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, typeID, entity)
	So(err, ShouldBeNil)
	for _, v := range values {
		if v.AttributeDefinitionID.String() == attrID {
			raw, merr := json.Marshal(v.Value)
			So(merr, ShouldBeNil)
			return string(raw)
		}
	}
	return ""
}

// runComputedRollups covers the rollup evaluator.
//
// A rollup aggregates one attribute across the entities a relationship
// reaches. It was refused for a release — declared in the API, described in
// the OpenAPI document, exported by both SDKs, and rejected by the service —
// so a schema author could model a total and never receive one.
func runComputedRollups(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given a dish whose cost rolls up from its ingredients ("+label+")", t, func() {
		f := newRollupFixture(t, setup())

		f.set(f.costPerKg, f.ingredient, "flour", `"1.20"`)
		f.set(f.costPerKg, f.ingredient, "butter", `"7.50"`)
		f.set(f.quantityKg, f.line, "line-flour", `"0.500"`)
		f.set(f.quantityKg, f.line, "line-butter", `"0.250"`)
		f.link(f.ofIngredient, "flour", "line-flour")
		f.link(f.ofIngredient, "butter", "line-butter")
		f.link(f.hasLine, "shortcrust", "line-flour")
		f.link(f.hasLine, "shortcrust", "line-butter")

		Convey("When the links are in place", func() {
			Convey("Then each line carries its own cost", func() {
				// 0.500 * 1.20, and 0.250 * 7.50.
				So(f.value(f.line, "line-flour", f.lineCostAttrID), ShouldEqual, `"0.6"`)
				So(f.value(f.line, "line-butter", f.lineCostAttrID), ShouldEqual, `"1.875"`)
			})

			Convey("Then the dish totals them", func() {
				So(f.value(f.dish, "shortcrust", f.foodCost), ShouldEqual, `"2.475"`)
			})

			Convey("Then count answers without a target attribute", func() {
				So(f.value(f.dish, "shortcrust", f.lineCount), ShouldEqual, `2`)
			})
		})

		Convey("When a supplier price rises", func() {
			f.set(f.costPerKg, f.ingredient, "butter", `"9.00"`)

			Convey("Then the change reaches the dish, two relationships away", func() {
				// The butter line becomes 0.250 * 9.00 = 2.25, so the dish is
				// 0.6 + 2.25. Nothing wrote to the dish, or to the line.
				//
				// Both halves are reported together: when this fails, which
				// one is stale says whether the rollup or the formula reading
				// it did not catch up.
				So(
					f.value(f.line, "line-butter", f.ingredientCostAttrID)+" "+
						f.value(f.line, "line-butter", f.lineCostAttrID)+" "+
						f.value(f.dish, "shortcrust", f.foodCost),
					ShouldEqual, `"9" "2.25" "2.85"`)
			})
		})

		Convey("When a line is unlinked from the dish", func() {
			links, err := f.svc.Interactors(f.ctx).Relationships().ListByEntity(f.ctx, "line-butter")
			So(err, ShouldBeNil)
			removed := false
			for _, link := range links {
				if link.Definition.InternalName == "has_line" {
					_, uerr := f.svc.Interactors(f.ctx).Relationships().Unlink(f.ctx,
						link.Relationship.ID.String())
					So(uerr, ShouldBeNil)
					removed = true
				}
			}
			So(removed, ShouldBeTrue)

			Convey("Then the total follows the link, with no value written anywhere", func() {
				// A rollup's inputs are on other entities, so unlinking emits
				// no value event for the dish at all.
				So(f.value(f.dish, "shortcrust", f.foodCost), ShouldEqual, `"0.6"`)
				So(f.value(f.dish, "shortcrust", f.lineCount), ShouldEqual, `1`)
			})
		})

		Convey("When a new line is added to the dish", func() {
			f.set(f.costPerKg, f.ingredient, "salt", `"0.40"`)
			f.set(f.quantityKg, f.line, "line-salt", `"0.010"`)
			f.link(f.ofIngredient, "salt", "line-salt")
			f.link(f.hasLine, "shortcrust", "line-salt")

			Convey("Then the total includes it", func() {
				// 0.6 + 1.875 + 0.004
				So(f.value(f.dish, "shortcrust", f.foodCost), ShouldEqual, `"2.479"`)
				So(f.value(f.dish, "shortcrust", f.lineCount), ShouldEqual, `3`)
			})
		})

		Convey("When a dish has no lines at all", func() {
			f.set(f.foodCostSourceForEmptyDish(), f.dish, "empty-dish", `"x"`)

			Convey("Then count is 0 and the sum is absent, not zero", func() {
				// "no lines" is a fact; "the total of nothing" is not a
				// number, and reporting 0 would read as a free dish.
				So(f.value(f.dish, "empty-dish", f.lineCount), ShouldEqual, `0`)
				So(f.value(f.dish, "empty-dish", f.foodCost), ShouldEqual, ``)
			})
		})

		Convey("When an ingredient's price is removed", func() {
			values, err := f.svc.Interactors(f.ctx).Values().ListByEntity(f.ctx, f.ingredient, "butter")
			So(err, ShouldBeNil)
			for _, v := range values {
				if v.AttributeDefinitionID.String() == f.costPerKg {
					_, rerr := f.svc.Interactors(f.ctx).Values().Remove(f.ctx, v.ID.String())
					So(rerr, ShouldBeNil)
				}
			}

			Convey("Then the dependent totals clear rather than freeze", func() {
				So(f.value(f.line, "line-butter", f.lineCostAttrID), ShouldEqual, ``)
				So(f.value(f.dish, "shortcrust", f.foodCost), ShouldEqual, `"0.6"`)
			})
		})
	})
}

// foodCostSourceForEmptyDish gives an empty dish one plain value, so it exists
// as an entity at all.
func (f *rollupFixture) foodCostSourceForEmptyDish() string {
	out, err := f.svc.Interactors(f.ctx).Attributes().Create(f.ctx, appattribute.CreateInput{
		TypeDefinitionID: f.dish, InternalName: "note", DisplayName: "Note", DataType: "string",
	})
	if err != nil {
		// Created once per fixture; a second call reuses it.
		attrs, lerr := f.svc.Interactors(f.ctx).TypeDefinitions().EffectiveAttributes(f.ctx, f.dish)
		So(lerr, ShouldBeNil)
		for _, a := range attrs {
			if a.Attribute.InternalName == "note" {
				return a.Attribute.ID.String()
			}
		}
		So(err, ShouldBeNil)
	}
	return out.ID.String()
}

// TestComputedRollups runs the scenarios against the in-memory backend.
func TestComputedRollups(t *testing.T) {
	runComputedRollups(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory()
	})
}

// TestComputedRollupsPostgres re-runs them against Postgres, where the
// traversal and the value reads are SQL.
func TestComputedRollupsPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runComputedRollups(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool, svc)
		return svc
	})
}

// TestRollupDefinitionIsResolved covers the guard at definition time.
//
// A rollup naming a relationship that does not exist finds no counterparts,
// aggregates nothing and clears its value — silently, for ever. That is
// indistinguishable from "no data yet", and it is the exact failure the
// feature was withheld for a release to avoid. Each mistake below is refused
// where the author can still see it.
func TestRollupDefinitionIsResolved(t *testing.T) {
	Convey("Given a dish type that owns lines through a relationship", t, func() {
		svc := flexitype.NewInMemory()
		f := newRollupFixture(t, svc)

		create := func(typeID, name, spec string) error {
			_, err := svc.Interactors(f.ctx).Attributes().Create(f.ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
				DataType: "decimal", Computed: json.RawMessage(spec),
			})
			return err
		}

		Convey("When the relationship does not exist", func() {
			err := create(f.dish, "bad_rel",
				`{"kind":"rollup","rollup":{"relationship":"has_lines","direction":"child","aggregate":"sum","target":"line_cost"}}`)

			Convey("Then it is refused, naming the relationship", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "has_lines")
			})
		})

		Convey("When the direction points the wrong way", func() {
			// The dish is the PARENT of has_line, so it has no parents to
			// traverse: this rollup could only ever aggregate nothing.
			err := create(f.dish, "bad_direction",
				`{"kind":"rollup","rollup":{"relationship":"has_line","direction":"parent","aggregate":"sum","target":"line_cost"}}`)

			Convey("Then it is refused, saying which side the traversal starts from", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "child type")
			})
		})

		Convey("When the target does not exist on the type the relationship reaches", func() {
			err := create(f.dish, "bad_target",
				`{"kind":"rollup","rollup":{"relationship":"has_line","direction":"child","aggregate":"sum","target":"nonexistent"}}`)

			Convey("Then it is refused, naming the target", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "nonexistent")
			})
		})

		Convey("When the target is not numeric", func() {
			_, err := svc.Interactors(f.ctx).Attributes().Create(f.ctx, appattribute.CreateInput{
				TypeDefinitionID: f.line, InternalName: "note", DisplayName: "Note", DataType: "string",
			})
			So(err, ShouldBeNil)

			cerr := create(f.dish, "bad_type",
				`{"kind":"rollup","rollup":{"relationship":"has_line","direction":"child","aggregate":"sum","target":"note"}}`)

			Convey("Then it is refused: a total of text is not a number", func() {
				So(cerr, ShouldNotBeNil)
				So(cerr.Error(), ShouldContainSubstring, "not numeric")
			})
		})

		Convey("When a count rollup names no target", func() {
			err := create(f.dish, "line_tally",
				`{"kind":"rollup","rollup":{"relationship":"has_line","direction":"child","aggregate":"count"}}`)

			Convey("Then it is accepted: count needs nothing on the far side", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

// TestOnePassIsSelfConsistent pins the ordering inside a single recompute.
//
// `line_cost = quantity * ingredient_cost` reads a ROLLUP on the same entity.
// If the formula is evaluated against the values loaded at the start of the
// pass, it uses the rollup's PREVIOUS result, and the right answer arrives
// only because the rollup's write emits an event that recomputes the entity
// again. That makes correctness depend on a follow-up dispatch — and under
// load the stale value is what a read finds.
//
// One pass must therefore be self-consistent. The follow-up event is then a
// no-op rather than the thing that saves it.
func TestOnePassIsSelfConsistent(t *testing.T) {
	Convey("Given a line whose cost reads a rollup on the same entity", t, func() {
		svc := flexitype.NewInMemory()
		f := newRollupFixture(t, svc)

		f.set(f.costPerKg, f.ingredient, "butter", `"7.50"`)
		f.set(f.quantityKg, f.line, "line-butter", `"0.250"`)
		f.link(f.ofIngredient, "butter", "line-butter")
		So(f.value(f.line, "line-butter", f.lineCostAttrID), ShouldEqual, `"1.875"`)

		Convey("When the ingredient's price changes", func() {
			f.set(f.costPerKg, f.ingredient, "butter", `"9.00"`)

			Convey("Then the line cost is right on the FIRST read", func() {
				// 0.250 * 9.00. Reading 1.875 here means the formula ran
				// against the rollup's previous result and something later
				// was expected to fix it.
				So(f.value(f.line, "line-butter", f.lineCostAttrID), ShouldEqual, `"2.25"`)
			})
		})

		Convey("When the link is removed", func() {
			links, err := svc.Interactors(f.ctx).Relationships().ListByEntity(f.ctx, "line-butter")
			So(err, ShouldBeNil)
			for _, link := range links {
				if link.Definition.InternalName == "of_ingredient" {
					_, uerr := svc.Interactors(f.ctx).Relationships().Unlink(f.ctx, link.Relationship.ID.String())
					So(uerr, ShouldBeNil)
				}
			}

			Convey("Then the dependent formula clears in the same pass", func() {
				// The rollup is undefined with no counterparts, so the formula
				// that multiplies it is undefined too — not left at its last
				// value.
				So(f.value(f.line, "line-butter", f.lineCostAttrID), ShouldEqual, ``)
			})
		})
	})
}
