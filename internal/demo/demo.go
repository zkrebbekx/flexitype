// Package demo seeds a small, feature-covering dataset: a type hierarchy
// (product → e-bike), attributes with constraints, entity values, a
// relationship with link attributes and a dependency — enough to explore
// every console screen and FQL construct. Used by the browser playground.
package demo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
)

// Seed loads the demo dataset through the regular usecases, so activity
// log, events and the search index all populate exactly as they would in
// production.
func Seed(ctx context.Context, i *application.Interactors) error {
	product, err := i.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product",
		DisplayName:  "Product",
		Description:  "Anything the shop sells.",
	})
	if err != nil {
		return fmt.Errorf("seed product type: %w", err)
	}

	name, err := i.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: product.ID.String(),
		InternalName:     "name",
		DisplayName:      "Name",
		DataType:         "string",
		Required:         true,
	})
	if err != nil {
		return fmt.Errorf("seed name attribute: %w", err)
	}
	price, err := i.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: product.ID.String(),
		InternalName:     "price",
		DisplayName:      "Price",
		DataType:         "decimal",
	})
	if err != nil {
		return fmt.Errorf("seed price attribute: %w", err)
	}
	category, err := i.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: product.ID.String(),
		InternalName:     "category",
		DisplayName:      "Category",
		DataType:         "enum",
		Constraints: json.RawMessage(`[{"kind":"one_of","values":[
			{"type":"enum","value":"city"},{"type":"enum","value":"road"},
			{"type":"enum","value":"mountain"},{"type":"enum","value":"cargo"}]}]`),
	})
	if err != nil {
		return fmt.Errorf("seed category attribute: %w", err)
	}

	ebike, err := i.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "e_bike",
		DisplayName:  "E-Bike",
		Description:  "Electrified product line; inherits every product attribute.",
		ExtendsID:    product.ID.String(),
	})
	if err != nil {
		return fmt.Errorf("seed e_bike type: %w", err)
	}
	motorPower, err := i.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: ebike.ID.String(),
		InternalName:     "motor_power_w",
		DisplayName:      "Motor power (W)",
		DataType:         "integer",
	})
	if err != nil {
		return fmt.Errorf("seed motor_power_w attribute: %w", err)
	}

	supplier, err := i.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "supplier",
		DisplayName:  "Supplier",
	})
	if err != nil {
		return fmt.Errorf("seed supplier type: %w", err)
	}
	region, err := i.Attributes().Create(ctx, appattribute.CreateInput{
		TypeDefinitionID: supplier.ID.String(),
		InternalName:     "region",
		DisplayName:      "Region",
		DataType:         "string",
	})
	if err != nil {
		return fmt.Errorf("seed region attribute: %w", err)
	}

	suppliedBy, err := i.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
		InternalName: "supplied_by",
		DisplayName:  "Supplied by",
		Description:  "Which supplier delivers a product.",
		ParentTypeID: product.ID.String(),
		ChildTypeID:  supplier.ID.String(),
	})
	if err != nil {
		return fmt.Errorf("seed supplied_by definition: %w", err)
	}

	set := func(attrID, entityID, typeID string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = i.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: attrID,
			EntityID:              entityID,
			TypeDefinitionID:      typeID,
			Value:                 raw,
		})
		return err
	}

	type row struct {
		entity, typeID string
		attrs          map[string]any
	}
	rows := []row{
		{"sku-city-01", product.ID.String(), map[string]any{
			name.ID.String(): "City Cruiser", price.ID.String(): "799.00", category.ID.String(): "city"}},
		{"sku-road-01", product.ID.String(), map[string]any{
			name.ID.String(): "Road Racer", price.ID.String(): "1499.00", category.ID.String(): "road"}},
		{"sku-ebike-01", ebike.ID.String(), map[string]any{
			name.ID.String(): "Trail Blazer E", price.ID.String(): "3299.00",
			category.ID.String(): "mountain", motorPower.ID.String(): 750}},
		{"sku-ebike-02", ebike.ID.String(), map[string]any{
			name.ID.String(): "Cargo Hauler E", price.ID.String(): "4599.00",
			category.ID.String(): "cargo", motorPower.ID.String(): 500}},
		{"acme-cycles", supplier.ID.String(), map[string]any{region.ID.String(): "EU"}},
		{"pacific-parts", supplier.ID.String(), map[string]any{region.ID.String(): "APAC"}},
	}
	for _, r := range rows {
		for attrID, v := range r.attrs {
			if err := set(attrID, r.entity, r.typeID, v); err != nil {
				return fmt.Errorf("seed value for %s: %w", r.entity, err)
			}
		}
	}

	links := [][2]string{
		{"sku-city-01", "acme-cycles"},
		{"sku-road-01", "acme-cycles"},
		{"sku-ebike-01", "pacific-parts"},
		{"sku-ebike-02", "pacific-parts"},
	}
	for _, l := range links {
		if _, err := i.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: suppliedBy.ID.String(),
			ParentEntity: l[0],
			ChildEntity:  l[1],
		}); err != nil {
			return fmt.Errorf("seed link %s→%s: %w", l[0], l[1], err)
		}
	}

	return nil
}
