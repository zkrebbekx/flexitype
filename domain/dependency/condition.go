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
	Kind   ConditionKind        `json:"kind"`
	Value  *valueobjects.Value  `json:"-"`
	Values []valueobjects.Value `json:"-"`
	Min    *valueobjects.Value  `json:"-"`
	Max    *valueobjects.Value  `json:"-"`
	// MinExclusive/MaxExclusive turn a range bound strict: min becomes
	// "greater than" and max becomes "less than". The zero value keeps the
	// inclusive semantics every stored rule already has.
	//
	// They exist because an inclusive bound cannot express "over 50000" for a
	// continuous type: on an integer the author can write min 50001, but on a
	// float, decimal, date or quantity there is no next value to name.
	MinExclusive bool   `json:"min_exclusive,omitempty"`
	MaxExclusive bool   `json:"max_exclusive,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	// PatternSubstring opts a pattern condition out of anchoring, matching
	// attribute.Pattern.Substring. The zero value keeps the safe semantics.
	PatternSubstring bool                       `json:"pattern_substring,omitempty"`
	Dynamic          *valueobjects.DynamicValue `json:"dynamic,omitempty"`
	Op               DynamicOp                  `json:"op,omitempty"`
	// ContextKey names a CALLER-SUPPLIED fact to test instead of the rule's
	// source attribute.
	//
	// An embedder anchors flexitype values to its own entities by an opaque
	// entity_id, and those entities' primary fields live in host tables — a
	// customer's tier, an order's channel, a document's workflow state. No
	// condition could reference them, so a rule that depends on one had to be
	// expressed by copying that field into flexitype and keeping the copy in
	// step, which is the duplication soft typing exists to avoid.
	//
	// The value comes from the context the request carries
	// (uow.WithContextValues), so it is the host's fact at evaluation time
	// and is never stored here.
	ContextKey string `json:"context_key,omitempty"`
	// ContextType declares the data type of the caller-supplied fact, and is
	// required when ContextKey is set. A context condition's subject is not
	// the source attribute, so validating its operands against the source
	// type checked the wrong subject: a range over a numeric fact was
	// unbuildable unless the unrelated source attribute happened to be
	// ordered and of the operand's type, and a fact arriving as a different
	// type turned every write to the target into an unclassified comparison
	// error. Operands validate against this type, and a fact whose runtime
	// type differs does not match — the same fail-safe as an absent fact.
	ContextType valueobjects.DataType `json:"context_type,omitempty"`
}

type conditionJSON struct {
	Kind             ConditionKind              `json:"kind"`
	ContextKey       string                     `json:"context_key,omitempty"`
	ContextType      valueobjects.DataType      `json:"context_type,omitempty"`
	Value            json.RawMessage            `json:"value,omitempty"`
	Values           []json.RawMessage          `json:"values,omitempty"`
	Min              json.RawMessage            `json:"min,omitempty"`
	Max              json.RawMessage            `json:"max,omitempty"`
	MinExclusive     bool                       `json:"min_exclusive,omitempty"`
	MaxExclusive     bool                       `json:"max_exclusive,omitempty"`
	Pattern          string                     `json:"pattern,omitempty"`
	PatternSubstring bool                       `json:"pattern_substring,omitempty"`
	Dynamic          *valueobjects.DynamicValue `json:"dynamic,omitempty"`
	Op               DynamicOp                  `json:"op,omitempty"`
}

// MarshalJSON encodes value operands in their self-describing typed form.
func (c Condition) MarshalJSON() ([]byte, error) {
	out := conditionJSON{Kind: c.Kind, Pattern: c.Pattern, PatternSubstring: c.PatternSubstring,
		MinExclusive: c.MinExclusive, MaxExclusive: c.MaxExclusive,
		Dynamic: c.Dynamic, Op: c.Op, ContextKey: c.ContextKey, ContextType: c.ContextType}

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
	c.PatternSubstring = in.PatternSubstring
	c.MinExclusive = in.MinExclusive
	c.MaxExclusive = in.MaxExclusive
	c.Dynamic = in.Dynamic
	c.Op = in.Op
	c.ContextKey = in.ContextKey
	c.ContextType = in.ContextType
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

// Validate checks the condition's shape against the data type of its
// SUBJECT: the source attribute's type, or the declared context_type when
// the condition tests a caller-supplied fact. Validating a context condition
// against the source type checked the wrong subject — the operand types, the
// ordered-type rule for range and the textual rule for pattern all describe
// the fact, not the unrelated source attribute.
func (c Condition) Validate(sourceType valueobjects.DataType) error {
	subjectType := sourceType
	if c.ContextKey != "" {
		if c.ContextType == "" {
			return domainerrors.NewValidation(
				"a context condition requires context_type: the data type of the caller-supplied fact",
				"context_key", c.ContextKey)
		}
		if _, err := valueobjects.ParseDataType(string(c.ContextType)); err != nil {
			return domainerrors.NewValidation("unknown context_type",
				"context_type", string(c.ContextType), "context_key", c.ContextKey)
		}
		subjectType = c.ContextType
	} else if c.ContextType != "" {
		return domainerrors.NewValidation("context_type requires context_key")
	}
	// From here every check runs against subjectType. The error messages
	// keep the source-attribute wording for ordinary conditions; for a
	// context condition the reported type is the declared context_type.
	sourceType = subjectType
	// The strict-bound flags belong to a range and to nothing else. The
	// "a flag without its bound is an error" check lived inside the range
	// arm, so min_exclusive on an equals, in, pattern or dynamic condition
	// validated, stored and was then ignored — while the OpenAPI schema
	// documents both flags on the shared condition object with no
	// restriction, so an author had no way to learn it did nothing.
	if c.Kind != CondRange && (c.MinExclusive || c.MaxExclusive) {
		return domainerrors.NewValidation(
			"min_exclusive and max_exclusive apply to a range condition only",
			"kind", string(c.Kind))
	}
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
		if c.MinExclusive && c.Min == nil {
			return domainerrors.NewValidation("min_exclusive requires a min bound")
		}
		if c.MaxExclusive && c.Max == nil {
			return domainerrors.NewValidation("max_exclusive requires a max bound")
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
			if cmp < 0 || (c.MinExclusive && cmp == 0) {
				return false, nil
			}
		}
		if c.Max != nil {
			cmp, err := source.Compare(*c.Max)
			if err != nil {
				return false, err
			}
			if cmp > 0 || (c.MaxExclusive && cmp == 0) {
				return false, nil
			}
		}
		return true, nil

	case CondPattern:
		// Anchored, matching the attribute constraint of the same name. An
		// unanchored condition matched any value CONTAINING the pattern, so a
		// rule written to fire on an identifier format fired on anything that
		// merely embedded one.
		re, err := regexp.Compile(conditionPattern(c))
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

// conditionPattern returns the expression to compile for a pattern condition:
// anchored unless the author asked for substring matching, exactly as
// attribute.Pattern behaves.
func conditionPattern(c Condition) string {
	if c.PatternSubstring {
		return c.Pattern
	}
	return `\A(?:` + c.Pattern + `)\z`
}
