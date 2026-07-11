package attribute

import (
	"encoding/json"
	"fmt"
	"regexp"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// ConstraintKind discriminates constraint types on the wire and in storage.
type ConstraintKind string

// The supported constraint kinds.
const (
	KindMinLength ConstraintKind = "min_length"
	KindMaxLength ConstraintKind = "max_length"
	KindMinValue  ConstraintKind = "min_value"
	KindMaxValue  ConstraintKind = "max_value"
	KindPattern   ConstraintKind = "pattern"
	KindOneOf     ConstraintKind = "one_of"
)

// Constraint is a validation rule applied to attribute values. Each kind
// validates its own shape against the attribute's data type once at
// definition time, then checks values at write time.
type Constraint interface {
	// Kind identifies the constraint for storage and APIs.
	Kind() ConstraintKind

	// Validate checks the constraint is well-formed and applicable to the
	// attribute's data type.
	Validate(dt valueobjects.DataType) error

	// Check validates a value against the constraint.
	Check(v valueobjects.Value) error
}

// MinLength requires textual values of at least N runes.
type MinLength struct {
	N int `json:"n"`
}

// Kind implements Constraint.
func (c MinLength) Kind() ConstraintKind { return KindMinLength }

// Validate implements Constraint.
func (c MinLength) Validate(dt valueobjects.DataType) error {
	if !dt.IsTextual() {
		return domainerrors.NewValidation("min_length requires a textual data type", "data_type", dt.String())
	}
	if c.N < 0 {
		return domainerrors.NewValidation("min_length must not be negative", "n", c.N)
	}
	return nil
}

// Check implements Constraint.
func (c MinLength) Check(v valueobjects.Value) error {
	if v.Length() < c.N {
		return domainerrors.NewValidation(
			fmt.Sprintf("value must be at least %d characters", c.N),
			"constraint", string(KindMinLength), "length", v.Length(),
		)
	}
	return nil
}

// MaxLength limits textual values to at most N runes.
type MaxLength struct {
	N int `json:"n"`
}

// Kind implements Constraint.
func (c MaxLength) Kind() ConstraintKind { return KindMaxLength }

// Validate implements Constraint.
func (c MaxLength) Validate(dt valueobjects.DataType) error {
	if !dt.IsTextual() {
		return domainerrors.NewValidation("max_length requires a textual data type", "data_type", dt.String())
	}
	if c.N <= 0 {
		return domainerrors.NewValidation("max_length must be positive", "n", c.N)
	}
	return nil
}

// Check implements Constraint.
func (c MaxLength) Check(v valueobjects.Value) error {
	if v.Length() > c.N {
		return domainerrors.NewValidation(
			fmt.Sprintf("value must be at most %d characters", c.N),
			"constraint", string(KindMaxLength), "length", v.Length(),
		)
	}
	return nil
}

// MinValue bounds ordered values from below (inclusive).
type MinValue struct {
	Min valueobjects.Value `json:"min"`
}

// Kind implements Constraint.
func (c MinValue) Kind() ConstraintKind { return KindMinValue }

// Validate implements Constraint.
func (c MinValue) Validate(dt valueobjects.DataType) error {
	if !dt.IsOrdered() {
		return domainerrors.NewValidation("min_value requires an ordered data type", "data_type", dt.String())
	}
	if c.Min.DataType() != dt {
		return domainerrors.NewValidation("min_value operand type must match the attribute type",
			"operand_type", c.Min.DataType().String(), "data_type", dt.String())
	}
	return nil
}

// Check implements Constraint.
func (c MinValue) Check(v valueobjects.Value) error {
	cmp, err := v.Compare(c.Min)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	if cmp < 0 {
		return domainerrors.NewValidation(
			fmt.Sprintf("value must be at least %s", c.Min.String()),
			"constraint", string(KindMinValue),
		)
	}
	return nil
}

// MaxValue bounds ordered values from above (inclusive).
type MaxValue struct {
	Max valueobjects.Value `json:"max"`
}

// Kind implements Constraint.
func (c MaxValue) Kind() ConstraintKind { return KindMaxValue }

// Validate implements Constraint.
func (c MaxValue) Validate(dt valueobjects.DataType) error {
	if !dt.IsOrdered() {
		return domainerrors.NewValidation("max_value requires an ordered data type", "data_type", dt.String())
	}
	if c.Max.DataType() != dt {
		return domainerrors.NewValidation("max_value operand type must match the attribute type",
			"operand_type", c.Max.DataType().String(), "data_type", dt.String())
	}
	return nil
}

// Check implements Constraint.
func (c MaxValue) Check(v valueobjects.Value) error {
	cmp, err := v.Compare(c.Max)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	if cmp > 0 {
		return domainerrors.NewValidation(
			fmt.Sprintf("value must be at most %s", c.Max.String()),
			"constraint", string(KindMaxValue),
		)
	}
	return nil
}

// Pattern requires textual values to match an anchored RE2 expression.
type Pattern struct {
	Expr string `json:"expr"`

	compiled *regexp.Regexp
}

// Kind implements Constraint.
func (c Pattern) Kind() ConstraintKind { return KindPattern }

// Validate implements Constraint.
func (c Pattern) Validate(dt valueobjects.DataType) error {
	if !dt.IsTextual() {
		return domainerrors.NewValidation("pattern requires a textual data type", "data_type", dt.String())
	}
	if _, err := regexp.Compile(c.Expr); err != nil {
		return domainerrors.NewValidation("invalid pattern", "expr", c.Expr, "error", err.Error())
	}
	return nil
}

// Check implements Constraint.
func (c Pattern) Check(v valueobjects.Value) error {
	re := c.compiled
	if re == nil {
		var err error
		re, err = regexp.Compile(c.Expr)
		if err != nil {
			return domainerrors.NewValidation("invalid pattern", "expr", c.Expr)
		}
	}
	if !re.MatchString(v.Text()) {
		return domainerrors.NewValidation(
			fmt.Sprintf("value does not match pattern %s", c.Expr),
			"constraint", string(KindPattern),
		)
	}
	return nil
}

// OneOf restricts values to an allowed set. Mandatory for enum attributes.
type OneOf struct {
	Values []valueobjects.Value `json:"values"`
}

// Kind implements Constraint.
func (c OneOf) Kind() ConstraintKind { return KindOneOf }

// Validate implements Constraint.
func (c OneOf) Validate(dt valueobjects.DataType) error {
	if len(c.Values) == 0 {
		return domainerrors.NewValidation("one_of requires at least one allowed value")
	}
	for _, v := range c.Values {
		if v.DataType() != dt {
			return domainerrors.NewValidation("one_of member type must match the attribute type",
				"member_type", v.DataType().String(), "data_type", dt.String())
		}
	}
	return nil
}

// Check implements Constraint.
func (c OneOf) Check(v valueobjects.Value) error {
	for _, allowed := range c.Values {
		if v.Equal(allowed) {
			return nil
		}
	}
	return domainerrors.NewValidation("value is not in the allowed set",
		"constraint", string(KindOneOf), "value", v.String())
}

// Constraints is an ordered set of constraints with self-describing JSON
// encoding for JSONB storage and the API.
type Constraints []Constraint

type constraintJSON struct {
	Kind   ConstraintKind    `json:"kind"`
	N      *int              `json:"n,omitempty"`
	Value  json.RawMessage   `json:"value,omitempty"`
	Expr   string            `json:"expr,omitempty"`
	Values []json.RawMessage `json:"values,omitempty"`
}

// MarshalJSON encodes each constraint with a kind discriminator and typed
// value operands.
func (cs Constraints) MarshalJSON() ([]byte, error) {
	out := make([]constraintJSON, 0, len(cs))
	for _, c := range cs {
		var cj constraintJSON
		cj.Kind = c.Kind()
		switch t := c.(type) {
		case MinLength:
			n := t.N
			cj.N = &n
		case MaxLength:
			n := t.N
			cj.N = &n
		case MinValue:
			typed, err := t.Min.MarshalTyped()
			if err != nil {
				return nil, err
			}
			cj.Value = typed
		case MaxValue:
			typed, err := t.Max.MarshalTyped()
			if err != nil {
				return nil, err
			}
			cj.Value = typed
		case Pattern:
			cj.Expr = t.Expr
		case OneOf:
			for _, v := range t.Values {
				typed, err := v.MarshalTyped()
				if err != nil {
					return nil, err
				}
				cj.Values = append(cj.Values, typed)
			}
		default:
			return nil, fmt.Errorf("unknown constraint kind %q", c.Kind())
		}
		out = append(out, cj)
	}
	return json.Marshal(out)
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (cs *Constraints) UnmarshalJSON(b []byte) error {
	var raw []constraintJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(Constraints, 0, len(raw))
	for _, cj := range raw {
		switch cj.Kind {
		case KindMinLength:
			if cj.N == nil {
				return fmt.Errorf("min_length missing n")
			}
			out = append(out, MinLength{N: *cj.N})
		case KindMaxLength:
			if cj.N == nil {
				return fmt.Errorf("max_length missing n")
			}
			out = append(out, MaxLength{N: *cj.N})
		case KindMinValue:
			v, err := valueobjects.UnmarshalTypedValue(cj.Value)
			if err != nil {
				return fmt.Errorf("min_value operand: %w", err)
			}
			out = append(out, MinValue{Min: v})
		case KindMaxValue:
			v, err := valueobjects.UnmarshalTypedValue(cj.Value)
			if err != nil {
				return fmt.Errorf("max_value operand: %w", err)
			}
			out = append(out, MaxValue{Max: v})
		case KindPattern:
			compiled, err := regexp.Compile(cj.Expr)
			if err != nil {
				return fmt.Errorf("pattern %q: %w", cj.Expr, err)
			}
			out = append(out, Pattern{Expr: cj.Expr, compiled: compiled})
		case KindOneOf:
			values := make([]valueobjects.Value, 0, len(cj.Values))
			for _, rv := range cj.Values {
				v, err := valueobjects.UnmarshalTypedValue(rv)
				if err != nil {
					return fmt.Errorf("one_of member: %w", err)
				}
				values = append(values, v)
			}
			out = append(out, OneOf{Values: values})
		default:
			return fmt.Errorf("unknown constraint kind %q", cj.Kind)
		}
	}
	*cs = out
	return nil
}

// Validate checks every constraint against the data type and rejects
// duplicate kinds.
func (cs Constraints) Validate(dt valueobjects.DataType) error {
	seen := make(map[ConstraintKind]struct{}, len(cs))
	for _, c := range cs {
		if _, dup := seen[c.Kind()]; dup {
			return domainerrors.NewValidation("duplicate constraint", "kind", string(c.Kind()))
		}
		seen[c.Kind()] = struct{}{}
		if err := c.Validate(dt); err != nil {
			return err
		}
	}
	return nil
}

// Check validates a value against every constraint.
func (cs Constraints) Check(v valueobjects.Value) error {
	for _, c := range cs {
		if err := c.Check(v); err != nil {
			return err
		}
	}
	return nil
}
