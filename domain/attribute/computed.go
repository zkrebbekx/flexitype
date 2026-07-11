package attribute

import (
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/formula"
)

// ComputedKind discriminates how a computed attribute derives its value.
type ComputedKind string

const (
	// ComputedFormula derives the value from an arithmetic expression over
	// the entity's other attribute values.
	ComputedFormula ComputedKind = "formula"
	// ComputedRollup derives the value by aggregating a target attribute
	// across the entities reached by a relationship.
	ComputedRollup ComputedKind = "rollup"
)

// RollupAggregate is the aggregate applied over the traversed entities.
type RollupAggregate string

// The supported rollup aggregates.
const (
	RollupCount RollupAggregate = "count"
	RollupSum   RollupAggregate = "sum"
	RollupMin   RollupAggregate = "min"
	RollupMax   RollupAggregate = "max"
)

// Rollup describes a relationship aggregate: e.g. count(child(uses)) or
// sum(child(uses).weight).
type Rollup struct {
	// Relationship is the relationship definition's internal name.
	Relationship string `json:"relationship"`
	// Direction is which endpoint to traverse to: child, parent or linked.
	Direction string `json:"direction"`
	// Aggregate is count/sum/min/max.
	Aggregate RollupAggregate `json:"aggregate"`
	// Target is the counterpart attribute's internal name aggregated
	// (empty for count).
	Target string `json:"target,omitempty"`
}

// Computed is a read-only attribute's derivation. Exactly one of Formula or
// Rollup is populated per Kind. Computed values are materialized (never
// written through the values API) and are queryable in FQL like any value.
type Computed struct {
	Kind    ComputedKind `json:"kind"`
	Formula string       `json:"formula,omitempty"`
	Rollup  *Rollup      `json:"rollup,omitempty"`
}

// Validate checks the computed spec is well-formed and returns the formula's
// attribute references (empty for rollups) for cycle detection.
func (c *Computed) Validate() ([]string, error) {
	switch c.Kind {
	case ComputedFormula:
		if c.Formula == "" {
			return nil, domainerrors.NewValidation("computed formula is required")
		}
		expr, err := formula.Parse(c.Formula)
		if err != nil {
			return nil, domainerrors.NewValidation("invalid formula: " + err.Error())
		}
		return expr.Refs(), nil
	case ComputedRollup:
		if c.Rollup == nil {
			return nil, domainerrors.NewValidation("computed rollup spec is required")
		}
		if c.Rollup.Relationship == "" {
			return nil, domainerrors.NewValidation("rollup relationship is required")
		}
		switch c.Rollup.Direction {
		case "child", "parent", "linked":
		default:
			return nil, domainerrors.NewValidation("rollup direction must be child, parent or linked", "direction", c.Rollup.Direction)
		}
		switch c.Rollup.Aggregate {
		case RollupCount:
		case RollupSum, RollupMin, RollupMax:
			if c.Rollup.Target == "" {
				return nil, domainerrors.NewValidation("rollup aggregate requires a target attribute", "aggregate", string(c.Rollup.Aggregate))
			}
		default:
			return nil, domainerrors.NewValidation("unknown rollup aggregate", "aggregate", string(c.Rollup.Aggregate))
		}
		return nil, nil
	default:
		return nil, domainerrors.NewValidation("unknown computed kind", "kind", string(c.Kind))
	}
}
