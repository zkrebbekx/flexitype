package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zkrebbekx/flexitype/client"
)

// Every helper here is IDEMPOTENT. The service runs them on every start, so a
// restart repairs a schema that was half-created when a previous start failed,
// and an operator can re-run them without thinking about it.

func ensureUnitFamily(ctx context.Context, c *client.Client) (string, error) {
	// A kitchen buys in kilograms, pounds and grams and cooks in grams. The
	// family is what lets one cost per kilogram serve all of them: a value
	// keeps the unit it was entered in, and compares in the base unit.
	created, err := c.UnitFamilies().Create(ctx, client.CreateUnitFamilyInput{
		Name:     unitFamilyMass,
		BaseUnit: "kg",
		Units: map[string]float64{
			"kg": 1,
			"g":  0.001,
			"lb": 0.45359237,
			"oz": 0.028349523125,
		},
	})
	switch {
	case err == nil:
		return created.ID, nil
	case errors.Is(err, client.ErrConflict):
		// Already there, from a previous start. An attribute addresses a
		// family by ID, so the existing one has to be found rather than
		// assumed.
		families, lerr := c.UnitFamilies().List(ctx)
		if lerr != nil {
			return "", fmt.Errorf("list unit families: %w", lerr)
		}
		for _, family := range families {
			if family.Name == unitFamilyMass {
				return family.ID, nil
			}
		}
		return "", fmt.Errorf("the %s unit family exists but could not be found", unitFamilyMass)
	default:
		return "", fmt.Errorf("create the mass unit family: %w", err)
	}
}

func ensureType(ctx context.Context, c *client.Client, internalName string) (string, error) {
	existing, err := typeByName(ctx, c, internalName)
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	if existing != nil {
		return existing.ID, nil
	}
	created, err := c.Types().Create(ctx, client.CreateTypeInput{
		InternalName: internalName, DisplayName: displayNameOf(internalName),
	})
	if err != nil {
		if errors.Is(err, client.ErrConflict) {
			// Another replica created it between the read and the write.
			again, gerr := typeByName(ctx, c, internalName)
			if gerr != nil {
				return "", gerr
			}
			return again.ID, nil
		}
		return "", fmt.Errorf("create type %q: %w", internalName, err)
	}
	return created.ID, nil
}

func ensureRelationship(ctx context.Context, c *client.Client, name, parentTypeID, childTypeID string) error {
	page, err := c.RelationshipDefinitions().List(ctx, client.ListRelationshipDefinitionsOptions{ListOptions: client.ListOptions{Limit: 200}})
	if err != nil {
		return fmt.Errorf("list relationship definitions: %w", err)
	}
	for _, def := range page.Items {
		if def.InternalName == name {
			return nil
		}
	}
	_, err = c.RelationshipDefinitions().Create(ctx, client.CreateRelationshipDefinitionInput{
		InternalName: name, DisplayName: displayNameOf(name), Kind: "directed",
		ParentTypeID: parentTypeID, ChildTypeID: childTypeID,
	})
	if err != nil && !errors.Is(err, client.ErrConflict) {
		return fmt.Errorf("create relationship %q: %w", name, err)
	}
	return nil
}

func ensureAttribute(ctx context.Context, c *client.Client, in client.CreateAttributeInput) error {
	attrs, err := c.Types().EffectiveAttributes(ctx, in.TypeDefinitionID)
	if err != nil {
		return fmt.Errorf("read attributes of %s: %w", in.TypeDefinitionID, err)
	}
	for _, a := range attrs {
		if a.Attribute.InternalName == in.InternalName {
			return nil
		}
	}
	if _, err := c.Attributes().Create(ctx, in); err != nil && !errors.Is(err, client.ErrConflict) {
		return fmt.Errorf("create attribute %q: %w", in.InternalName, err)
	}
	return nil
}

// attributeIDs maps a type's effective attributes by internal name.
func attributeIDs(ctx context.Context, c *client.Client, typeID string) (map[string]string, error) {
	attrs, err := c.Types().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("read attributes: %w", err)
	}
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[a.Attribute.InternalName] = a.Attribute.ID
	}
	return out, nil
}

// typeByName resolves a type definition by its internal name.
func typeByName(ctx context.Context, c *client.Client, internalName string) (*client.TypeDefinition, error) {
	page, err := c.Types().List(ctx, client.ListTypesOptions{
		InternalNames: []string{internalName},
		ListOptions:   client.ListOptions{Limit: 10},
	})
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		if page.Items[i].InternalName == internalName {
			return &page.Items[i], nil
		}
	}
	return nil, client.ErrNotFound
}

// relationshipByName resolves a relationship definition by its internal name.
func relationshipByName(ctx context.Context, c *client.Client, internalName string) (string, error) {
	page, err := c.RelationshipDefinitions().List(ctx, client.ListRelationshipDefinitionsOptions{ListOptions: client.ListOptions{Limit: 200}})
	if err != nil {
		return "", fmt.Errorf("list relationship definitions: %w", err)
	}
	for _, def := range page.Items {
		if def.InternalName == internalName {
			return def.ID, nil
		}
	}
	return "", fmt.Errorf("no relationship named %q", internalName)
}

// ensureDependency creates one dependency rule, tolerating one that is there.
func ensureDependency(ctx context.Context, c *client.Client, in client.CreateDependencyInput) error {
	if _, err := c.Dependencies().Create(ctx, in); err != nil && !errors.Is(err, client.ErrConflict) {
		return fmt.Errorf("create dependency: %w", err)
	}
	return nil
}

// displayNameOf turns an internal name into something a person reads.
func displayNameOf(internalName string) string {
	out := []rune{}
	upper := true
	for _, r := range internalName {
		if r == '_' {
			out = append(out, ' ')
			upper = true
			continue
		}
		if upper && r >= 'a' && r <= 'z' {
			r = r - 'a' + 'A'
		}
		upper = false
		out = append(out, r)
	}
	return string(out)
}

// quantity builds the wire form of a quantity value: a magnitude and the unit
// it was entered in. The service converts to the family's base unit for
// comparison and arithmetic, and keeps the unit for display.
func quantity(magnitude, unit string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"magnitude": magnitude, "unit": unit})
	if err != nil {
		// Both fields are strings, so this cannot fail.
		return json.RawMessage(`null`)
	}
	return raw
}
