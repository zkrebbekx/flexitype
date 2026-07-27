package dependency

import (
	"time"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// EffectiveSchema is the resolved rule set for one target attribute given
// an entity's current source values: the attribute's own rules plus every
// matched dependency effect.
type EffectiveSchema struct {
	Required      bool
	AllowedValues []valueobjects.Value
	Constraints   attribute.Constraints
	// Restricted distinguishes "no allowed-value narrowing" from "narrowed
	// to an empty set" (conflicting dependencies): when true the value must
	// be in AllowedValues, even if that set is empty.
	Restricted bool
}

// ResolveEffective computes the effective schema for a target attribute.
// deps are the dependencies targeting it; sourceValues maps each source
// attribute ID to ALL of the entity's current values for it (absent = the zero
// value). A condition matches when any of them satisfies it — see
// Dependency.MatchesAny.
// Matched allowed-value sets intersect; constraints accumulate.
func ResolveEffective(
	target *attribute.Definition,
	deps []*Dependency,
	sourceValues map[valueobjects.AttributeDefinitionID][]valueobjects.Value,
	now time.Time,
) (EffectiveSchema, error) {
	schema := EffectiveSchema{
		Required:    target.Required(),
		Constraints: target.Constraints(),
	}
	// Tracked across the loop so conflicting overrides resolve by rule rather
	// than by whichever dependency the store happened to return last.
	overridden, requiredByAny := false, false

	for _, d := range deps {
		if d.IsArchived() || !d.TargetAttributeID().Equals(target.ID()) {
			continue
		}
		matched, err := d.MatchesAny(sourceValues[d.SourceAttributeID()], now)
		if err != nil {
			return EffectiveSchema{}, err
		}
		if !matched {
			continue
		}

		effect := d.Effect()
		if len(effect.AllowedValues) > 0 {
			if schema.Restricted {
				schema.AllowedValues = intersect(schema.AllowedValues, effect.AllowedValues)
			} else {
				schema.AllowedValues = effect.AllowedValues
				schema.Restricted = true
			}
		}
		schema.Constraints = append(schema.Constraints, effect.Constraints...)
		if effect.Required != nil {
			overridden = true
			requiredByAny = requiredByAny || *effect.Required
		}
	}
	// A matched override replaces the attribute's own required flag, so a
	// dependency can still relax a required attribute. Two matched overrides
	// that DISAGREE resolve to required.
	//
	// Last-writer-wins resolved by repository return order, so the outcome
	// depended on dependency creation order: the in-memory and SQL backends
	// could disagree for identical schemas, and editing one rule silently
	// changed the behaviour of a rule its author never touched. The direction
	// follows from the asymmetry — resolving to "optional" when a matched rule
	// demanded the field lets incomplete records through a gate that was
	// configured to stop them, and that is the failure that costs something.
	// It also matches how AllowedValues already resolves conflicts, by
	// intersection rather than by order.
	if overridden {
		schema.Required = requiredByAny
	}
	return schema, nil
}

// Check validates a candidate value against the effective schema. The
// caller has already run the target definition's own ValidateValue; this
// applies the dependency-derived narrowing on top.
func (s EffectiveSchema) Check(v valueobjects.Value) error {
	if v.IsZero() {
		if s.Required {
			return domainerrors.NewDependencyViolation("value is required by an attribute dependency")
		}
		return nil
	}
	if s.Restricted {
		allowed := false
		for _, a := range s.AllowedValues {
			if v.Equal(a) {
				allowed = true
				break
			}
		}
		if !allowed {
			return domainerrors.NewDependencyViolation(
				"value is not allowed by the entity's attribute dependencies",
				"value", v.String(),
			)
		}
	}
	for _, c := range s.Constraints {
		if err := c.Check(v); err != nil {
			return domainerrors.NewDependencyViolation(err.Error())
		}
	}
	return nil
}

// intersect narrows current by next.
func intersect(current, next []valueobjects.Value) []valueobjects.Value {
	var out []valueobjects.Value
	for _, v := range current {
		for _, n := range next {
			if v.Equal(n) {
				out = append(out, v)
				break
			}
		}
	}
	return out
}
