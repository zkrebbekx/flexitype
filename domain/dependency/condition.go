package dependency

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// ConditionKind discriminates dependency condition types.
type ConditionKind string

const (
	// CondEquals matches when the source value equals Value.
	CondEquals ConditionKind = "equals"
	// CondIn matches when the source value is any member of Values.
	CondIn ConditionKind = "in"
	// CondRange matches when Min <= source <= Max (either bound optional).
	CondRange ConditionKind = "range"
	// CondPattern matches textual source values against an RE2 expression.
	CondPattern ConditionKind = "pattern"
	// CondDynamic compares temporal source values against a runtime
	// instant (now/today/relative) using Op.
	CondDynamic ConditionKind = "dynamic"
)

// DynamicOp is the comparison operator for CondDynamic conditions.
type DynamicOp string

// The supported dynamic comparison operators.
const (
	OpBefore   DynamicOp = "before"
	OpAfter    DynamicOp = "after"
	OpOnBefore DynamicOp = "on_or_before"
	OpOnAfter  DynamicOp = "on_or_after"
)

// Condition is one predicate over a source attribute's value. All of a
// dependency's conditions must match for its effect to apply.
type Condition struct {
	Kind    ConditionKind              `json:"kind"`
	Value   *valueobjects.Value        `json:"-"`
	Values  []valueobjects.Value       `json:"-"`
	Min     *valueobjects.Value        `json:"-"`
	Max     *valueobjects.Value        `json:"-"`
	Pattern string                     `json:"pattern,omitempty"`
	Dynamic *valueobjects.DynamicValue `json:"dynamic,omitempty"`
	Op      DynamicOp                  `json:"op,omitempty"`
}

type conditionJSON struct {
	Kind    ConditionKind              `json:"kind"`
	Value   json.RawMessage            `json:"value,omitempty"`
	Values  []json.RawMessage          `json:"values,omitempty"`
	Min     json.RawMessage            `json:"min,omitempty"`
	Max     json.RawMessage            `json:"max,omitempty"`
	Pattern string                     `json:"pattern,omitempty"`
	Dynamic *valueobjects.DynamicValue `json:"dynamic,omitempty"`
	Op      DynamicOp                  `json:"op,omitempty"`
}

// MarshalJSON encodes value operands in their self-describing typed form.
func (c Condition) MarshalJSON() ([]byte, error) {
	out := conditionJSON{Kind: c.Kind, Pattern: c.Pattern, Dynamic: c.Dynamic, Op: c.Op}

	marshal := func(v *valueobjects.Value) (json.RawMessage, error) {
		if v == nil {
			return nil, nil
		}
		return v.MarshalTyped()
	}

	var err error
	if out.Value, err = marshal(c.Value); err != nil {
		return nil, err
	}
	if out.Min, err = marshal(c.Min); err != nil {
		return nil, err
	}
	if out.Max, err = marshal(c.Max); err != nil {
		return nil, err
	}
	for _, v := range c.Values {
		typed, err := v.MarshalTyped()
		if err != nil {
			return nil, err
		}
		out.Values = append(out.Values, typed)
	}
	return json.Marshal(out)
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (c *Condition) UnmarshalJSON(b []byte) error {
	var in conditionJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	c.Kind = in.Kind
	c.Pattern = in.Pattern
	c.Dynamic = in.Dynamic
	c.Op = in.Op
	c.Value, c.Min, c.Max, c.Values = nil, nil, nil, nil

	unmarshal := func(raw json.RawMessage) (*valueobjects.Value, error) {
		if len(raw) == 0 || string(raw) == "null" {
			return nil, nil
		}
		v, err := valueobjects.UnmarshalTypedValue(raw)
		if err != nil {
			return nil, err
		}
		return &v, nil
	}

	var err error
	if c.Value, err = unmarshal(in.Value); err != nil {
		return fmt.Errorf("condition value: %w", err)
	}
	if c.Min, err = unmarshal(in.Min); err != nil {
		return fmt.Errorf("condition min: %w", err)
	}
	if c.Max, err = unmarshal(in.Max); err != nil {
		return fmt.Errorf("condition max: %w", err)
	}
	for _, raw := range in.Values {
		v, err := valueobjects.UnmarshalTypedValue(raw)
		if err != nil {
			return fmt.Errorf("condition values member: %w", err)
		}
		c.Values = append(c.Values, v)
	}
	return nil
}

// Validate checks the condition's shape against the source attribute's data
// type.
func (c Condition) Validate(sourceType valueobjects.DataType) error {
	switch c.Kind {
	case CondEquals:
		if c.Value == nil {
			return domainerrors.NewValidation("equals condition requires a value")
		}
		if c.Value.DataType() != sourceType {
			return domainerrors.NewValidation("equals operand type must match the source attribute type",
				"operand_type", c.Value.DataType().String(), "source_type", sourceType.String())
		}
	case CondIn:
		if len(c.Values) == 0 {
			return domainerrors.NewValidation("in condition requires at least one value")
		}
		for _, v := range c.Values {
			if v.DataType() != sourceType {
				return domainerrors.NewValidation("in member type must match the source attribute type",
					"member_type", v.DataType().String(), "source_type", sourceType.String())
			}
		}
	case CondRange:
		if c.Min == nil && c.Max == nil {
			return domainerrors.NewValidation("range condition requires min and/or max")
		}
		if !sourceType.IsOrdered() {
			return domainerrors.NewValidation("range condition requires an ordered source attribute",
				"source_type", sourceType.String())
		}
		for _, bound := range []*valueobjects.Value{c.Min, c.Max} {
			if bound != nil && bound.DataType() != sourceType {
				return domainerrors.NewValidation("range bound type must match the source attribute type",
					"bound_type", bound.DataType().String(), "source_type", sourceType.String())
			}
		}
	case CondPattern:
		if !sourceType.IsTextual() {
			return domainerrors.NewValidation("pattern condition requires a textual source attribute",
				"source_type", sourceType.String())
		}
		if _, err := regexp.Compile(c.Pattern); err != nil {
			return domainerrors.NewValidation("invalid pattern", "pattern", c.Pattern, "error", err.Error())
		}
	case CondDynamic:
		if !sourceType.IsTemporal() {
			return domainerrors.NewValidation("dynamic condition requires a temporal source attribute",
				"source_type", sourceType.String())
		}
		if c.Dynamic == nil {
			return domainerrors.NewValidation("dynamic condition requires a dynamic value")
		}
		if err := c.Dynamic.Validate(sourceType); err != nil {
			return domainerrors.NewValidation(err.Error())
		}
		switch c.Op {
		case OpBefore, OpAfter, OpOnBefore, OpOnAfter:
		default:
			return domainerrors.NewValidation("unknown dynamic operator", "op", string(c.Op))
		}
	default:
		return domainerrors.NewValidation("unknown condition kind", "kind", string(c.Kind))
	}
	return nil
}

// Matches evaluates the condition against a source value at instant now.
// An absent source value never matches.
func (c Condition) Matches(source valueobjects.Value, now time.Time) (bool, error) {
	if source.IsZero() {
		return false, nil
	}

	switch c.Kind {
	case CondEquals:
		return c.Value != nil && source.Equal(*c.Value), nil

	case CondIn:
		for _, v := range c.Values {
			if source.Equal(v) {
				return true, nil
			}
		}
		return false, nil

	case CondRange:
		if c.Min != nil {
			cmp, err := source.Compare(*c.Min)
			if err != nil {
				return false, err
			}
			if cmp < 0 {
				return false, nil
			}
		}
		if c.Max != nil {
			cmp, err := source.Compare(*c.Max)
			if err != nil {
				return false, err
			}
			if cmp > 0 {
				return false, nil
			}
		}
		return true, nil

	case CondPattern:
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return false, err
		}
		return re.MatchString(source.Text()), nil

	case CondDynamic:
		if c.Dynamic == nil {
			return false, nil
		}
		ref, err := c.Dynamic.Resolve(source.DataType(), now)
		if err != nil {
			return false, err
		}
		cmp, err := source.Compare(ref)
		if err != nil {
			return false, err
		}
		switch c.Op {
		case OpBefore:
			return cmp < 0, nil
		case OpAfter:
			return cmp > 0, nil
		case OpOnBefore:
			return cmp <= 0, nil
		case OpOnAfter:
			return cmp >= 0, nil
		default:
			return false, fmt.Errorf("unknown dynamic operator %q", c.Op)
		}

	default:
		return false, fmt.Errorf("unknown condition kind %q", c.Kind)
	}
}
