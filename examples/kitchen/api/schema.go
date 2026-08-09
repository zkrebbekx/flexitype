package main

import (
	"context"
	"encoding/json"

	"github.com/zkrebbekx/flexitype/client"
)

// The model this example runs on.
//
//	ingredient      what a supplier sells, priced per pack
//	recipe_line     one ingredient in one dish, with a quantity
//	dish            what a guest orders
//
// The costing is entirely the SERVICE's work. Nothing in this application adds
// up a price:
//
//	ingredient.cost_per_kg     formula  pack_price / pack_size
//	recipe_line.ingredient_cost  rollup   sum(parent(of_ingredient).cost_per_kg)
//	recipe_line.line_cost        formula  quantity * ingredient_cost
//	dish.food_cost               rollup   sum(child(has_line).line_cost)
//	dish.line_count              rollup   count(child(has_line))
//
// A supplier price change therefore reaches every dish that uses it, two
// relationships away, with no code here to make that happen.
const (
	typeIngredient = "ingredient"
	typeRecipeLine = "recipe_line"
	typeDish       = "dish"

	relOfIngredient = "of_ingredient"
	relHasLine      = "has_line"

	unitFamilyMass = "mass"
)

// channels a dish is priced for. A price is SCOPED by channel: the same dish
// costs one thing at a table and another through a delivery app, and both are
// the same dish.
var channels = []string{"dine_in", "delivery", "catering"}

// locales a dish is named and described in, besides the base value.
var locales = []string{"en", "fr"}

// ensureSchema creates the model if it is not there. It is idempotent: every
// step tolerates the conflict of already existing, so a restart re-runs it and
// an operator can re-run it to repair a partial failure.
func ensureSchema(ctx context.Context, c *client.Client, log Logger) error {
	massFamily, err := ensureUnitFamily(ctx, c)
	if err != nil {
		return err
	}

	types := map[string]string{}
	for _, name := range []string{typeIngredient, typeRecipeLine, typeDish} {
		id, err := ensureType(ctx, c, name)
		if err != nil {
			return err
		}
		types[name] = id
	}

	// Relationships come before the rollups that traverse them: a rollup
	// naming a relationship that does not exist yet is refused, which is the
	// service telling us the order matters.
	if err := ensureRelationship(ctx, c, relOfIngredient, types[typeIngredient], types[typeRecipeLine]); err != nil {
		return err
	}
	if err := ensureRelationship(ctx, c, relHasLine, types[typeDish], types[typeRecipeLine]); err != nil {
		return err
	}

	for _, attr := range schemaAttributes(types, massFamily) {
		if err := ensureAttribute(ctx, c, attr); err != nil {
			return err
		}
	}

	if err := ensureDependencies(ctx, c, types); err != nil {
		return err
	}
	log.Info("schema ready")
	return nil
}

// schemaAttributes lists every attribute, in an order that satisfies the
// dependencies between them: a formula may reference an attribute that does
// not exist yet, but a ROLLUP's target must already be there.
func schemaAttributes(types map[string]string, massFamily string) []client.CreateAttributeInput {
	decimal2 := func(name, display, typeID string, sort int) client.CreateAttributeInput {
		return client.CreateAttributeInput{
			TypeDefinitionID: typeID, InternalName: name, DisplayName: display,
			DataType: "decimal", SortOrder: sort,
		}
	}

	return []client.CreateAttributeInput{
		// --- ingredient ---
		{
			TypeDefinitionID: types[typeIngredient], InternalName: "name", DisplayName: "Name",
			DataType: "string", Required: true, SortOrder: 10,
		},
		{
			TypeDefinitionID: types[typeIngredient], InternalName: "supplier", DisplayName: "Supplier",
			DataType: "string", SortOrder: 20,
		},
		{
			TypeDefinitionID: types[typeIngredient], InternalName: "pack_size", DisplayName: "Pack size",
			DataType: "quantity", UnitFamilyID: massFamily, DisplayUnit: "kg", SortOrder: 30,
			HelpText: "What one pack weighs. Enter it in whatever unit the invoice uses; the service converts.",
		},
		decimal2("pack_price", "Pack price", types[typeIngredient], 40),
		{
			TypeDefinitionID: types[typeIngredient], InternalName: "cost_per_kg", DisplayName: "Cost per kg",
			DataType: "decimal", SortOrder: 50,
			// A quantity evaluates as its BASE unit, so a pack entered in
			// pounds and one entered in grams both divide into a cost per kg.
			Computed: json.RawMessage(`{"kind":"formula","formula":"pack_price / pack_size"}`),
			HelpText: "Derived. The unit family's base unit is the kilogram, so this is per kg whatever the invoice said.",
		},

		// --- recipe line ---
		{
			TypeDefinitionID: types[typeRecipeLine], InternalName: "quantity", DisplayName: "Quantity",
			DataType: "quantity", UnitFamilyID: massFamily, DisplayUnit: "g", Required: true, SortOrder: 10,
			HelpText: "How much of the ingredient this dish uses. Grams by default; the cost is per kg either way.",
		},
		{
			TypeDefinitionID: types[typeRecipeLine], InternalName: "ingredient_cost",
			DisplayName: "Ingredient cost per kg", DataType: "decimal", SortOrder: 20,
			Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"` + relOfIngredient +
				`","direction":"parent","aggregate":"sum","target":"cost_per_kg"}}`),
			HelpText: "Derived from the ingredient this line points at.",
		},
		{
			TypeDefinitionID: types[typeRecipeLine], InternalName: "line_cost", DisplayName: "Line cost",
			DataType: "decimal", SortOrder: 30,
			Computed: json.RawMessage(`{"kind":"formula","formula":"quantity * ingredient_cost"}`),
			HelpText: "Derived: the quantity in kilograms times the ingredient's cost per kg.",
		},

		// --- dish ---
		{
			TypeDefinitionID: types[typeDish], InternalName: "name", DisplayName: "Name",
			DataType: "string", Required: true, Localizable: true, SortOrder: 10,
			HelpText: "One name per locale. The base value is the one a menu falls back to.",
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "description", DisplayName: "Description",
			DataType: "text", Localizable: true, SortOrder: 20,
			Constraints: json.RawMessage(`[{"kind":"max_length","n":2000}]`),
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "course", DisplayName: "Course",
			DataType: "enum", SortOrder: 30,
			Constraints: json.RawMessage(`[{"kind":"one_of","values":[` +
				`{"type":"enum","value":"starter"},{"type":"enum","value":"main"},` +
				`{"type":"enum","value":"dessert"}]}]`),
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "status", DisplayName: "Status",
			DataType: "enum", SortOrder: 40,
			Constraints: json.RawMessage(`[{"kind":"one_of","values":[` +
				`{"type":"enum","value":"draft"},{"type":"enum","value":"on_menu"},` +
				`{"type":"enum","value":"withdrawn"}]}]`),
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "price", DisplayName: "Price",
			DataType: "decimal", Scopable: true, SortOrder: 50,
			HelpText: "One price per channel: a table, a delivery app and a catering order are the same dish at different prices.",
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "contains_allergens",
			DisplayName: "Contains allergens", DataType: "bool", SortOrder: 60,
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "allergens", DisplayName: "Allergens",
			DataType: "string", MultiValued: true, SortOrder: 70,
			HelpText: "Required once the dish is marked as containing allergens. The dish can be saved without them; it cannot reach the menu.",
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "photo", DisplayName: "Photo",
			DataType: "media", SortOrder: 80,
			Constraints: json.RawMessage(`[{"kind":"media","mime":["image/png","image/jpeg","image/webp"],"max_size":5242880}]`),
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "food_cost", DisplayName: "Food cost",
			DataType: "decimal", SortOrder: 90,
			Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"` + relHasLine +
				`","direction":"child","aggregate":"sum","target":"line_cost"}}`),
			HelpText: "Derived: the total of this dish's lines. Nothing in the application adds it up.",
		},
		{
			TypeDefinitionID: types[typeDish], InternalName: "line_count", DisplayName: "Lines",
			DataType: "integer", SortOrder: 100,
			Computed: json.RawMessage(`{"kind":"rollup","rollup":{"relationship":"` + relHasLine +
				`","direction":"child","aggregate":"count"}}`),
		},
	}
}

// ensureDependencies adds the rules a chef would otherwise have to remember.
func ensureDependencies(ctx context.Context, c *client.Client, types map[string]string) error {
	ids, err := attributeIDs(ctx, c, types[typeDish])
	if err != nil {
		return err
	}
	// "Marked as containing allergens" with no list is worse than no marking
	// at all: it looks answered. The dependency makes the list REQUIRED once
	// the flag is set.
	//
	// enforce is on_read, which is the DEFAULT and is stated here because it
	// is a choice. A chef ticks the box and then types the list, in that
	// order, so refusing the tick would make the form unusable. The rule
	// reports the gap instead, and publishing is what turns it into a refusal
	// — see POST /api/dishes/{id}/publish, which reads the service's own
	// completeness report.
	//
	// A rule that must refuse the write itself declares "on_write"; see
	// docs/dependencies.md.
	return ensureDependency(ctx, c, client.CreateDependencyInput{
		SourceAttributeID: ids["contains_allergens"],
		TargetAttributeID: ids["allergens"],
		Conditions:        json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
		Effect:            json.RawMessage(`{"required":true,"enforce":"on_read"}`),
		Description:       "A dish that declares allergens must list them.",
	})
}
