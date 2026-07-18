package dependency

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestConditionValidation(t *testing.T) {
	Convey("Given conditions validated against a source attribute's data type", t, func() {
		Convey("When an equals condition is checked", func() {
			Convey("Then it needs an operand of the source's own type", func() {
				err := Condition{Kind: CondEquals}.Validate(valueobjects.DataTypeEnum)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires a value")

				wrong := valueobjects.NewStringValue("bike")
				err = Condition{Kind: CondEquals, Value: &wrong}.Validate(valueobjects.DataTypeEnum)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "must match the source attribute type")

				right := valueobjects.NewEnumValue("bike")
				So(Condition{Kind: CondEquals, Value: &right}.Validate(valueobjects.DataTypeEnum), ShouldBeNil)
			})
		})

		Convey("When an in condition is checked", func() {
			Convey("Then it needs a non-empty set of same-typed members", func() {
				err := Condition{Kind: CondIn}.Validate(valueobjects.DataTypeEnum)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "at least one value")

				err = Condition{Kind: CondIn, Values: []valueobjects.Value{
					valueobjects.NewEnumValue("bike"),
					valueobjects.NewStringValue("car"),
				}}.Validate(valueobjects.DataTypeEnum)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "in member type must match")

				So(Condition{Kind: CondIn, Values: []valueobjects.Value{
					valueobjects.NewEnumValue("bike"),
				}}.Validate(valueobjects.DataTypeEnum), ShouldBeNil)
			})
		})

		Convey("When a range condition is checked", func() {
			lo := valueobjects.NewIntegerValue(1)
			hi := valueobjects.NewIntegerValue(9)

			Convey("Then it needs a bound, an ordered source and same-typed bounds", func() {
				err := Condition{Kind: CondRange}.Validate(valueobjects.DataTypeInteger)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires min and/or max")

				err = Condition{Kind: CondRange, Min: &lo}.Validate(valueobjects.DataTypeString)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires an ordered source attribute")

				wrong := valueobjects.NewFloatValue(1)
				err = Condition{Kind: CondRange, Min: &wrong}.Validate(valueobjects.DataTypeInteger)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "range bound type must match")

				err = Condition{Kind: CondRange, Min: &lo, Max: &wrong}.Validate(valueobjects.DataTypeInteger)
				So(domainerrors.IsValidation(err), ShouldBeTrue)

				So(Condition{Kind: CondRange, Min: &lo, Max: &hi}.Validate(valueobjects.DataTypeInteger), ShouldBeNil)
				So(Condition{Kind: CondRange, Max: &hi}.Validate(valueobjects.DataTypeInteger), ShouldBeNil)
			})
		})

		Convey("When a pattern condition is checked", func() {
			Convey("Then it needs a textual source and a compilable RE2 expression", func() {
				err := Condition{Kind: CondPattern, Pattern: "^a"}.Validate(valueobjects.DataTypeInteger)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires a textual source attribute")

				err = Condition{Kind: CondPattern, Pattern: "("}.Validate(valueobjects.DataTypeString)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "invalid pattern")

				So(Condition{Kind: CondPattern, Pattern: "^SKU-"}.Validate(valueobjects.DataTypeString), ShouldBeNil)
			})
		})

		Convey("When a dynamic condition is checked", func() {
			dyn := &valueobjects.DynamicValue{Kind: valueobjects.DynamicToday}

			Convey("Then it needs a temporal source, a dynamic value and a known operator", func() {
				err := Condition{Kind: CondDynamic, Dynamic: dyn, Op: OpBefore}.Validate(valueobjects.DataTypeString)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires a temporal source attribute")

				err = Condition{Kind: CondDynamic, Op: OpBefore}.Validate(valueobjects.DataTypeDate)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "requires a dynamic value")

				bad := &valueobjects.DynamicValue{Kind: valueobjects.DynamicKind("whenever")}
				err = Condition{Kind: CondDynamic, Dynamic: bad, Op: OpBefore}.Validate(valueobjects.DataTypeDate)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown dynamic value kind")

				err = Condition{Kind: CondDynamic, Dynamic: dyn, Op: DynamicOp("sideways")}.
					Validate(valueobjects.DataTypeDate)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown dynamic operator")

				for _, op := range []DynamicOp{OpBefore, OpAfter, OpOnBefore, OpOnAfter} {
					So(Condition{Kind: CondDynamic, Dynamic: dyn, Op: op}.
						Validate(valueobjects.DataTypeDate), ShouldBeNil)
				}
			})
		})

		Convey("When the condition kind is unknown", func() {
			err := Condition{Kind: ConditionKind("maybe")}.Validate(valueobjects.DataTypeString)

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unknown condition kind")
			})
		})
	})
}

func TestConditionMatchingEdges(t *testing.T) {
	Convey("Given conditions evaluated against source values", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

		Convey("When an in condition is evaluated", func() {
			cond := Condition{Kind: CondIn, Values: []valueobjects.Value{
				valueobjects.NewEnumValue("bike"),
				valueobjects.NewEnumValue("car"),
			}}

			Convey("Then membership decides the match and absence never matches", func() {
				got, err := cond.Matches(valueobjects.NewEnumValue("car"), now)
				So(err, ShouldBeNil)
				So(got, ShouldBeTrue)

				got, err = cond.Matches(valueobjects.NewEnumValue("boat"), now)
				So(err, ShouldBeNil)
				So(got, ShouldBeFalse)

				got, err = cond.Matches(valueobjects.Value{}, now)
				So(err, ShouldBeNil)
				So(got, ShouldBeFalse)
			})
		})

		Convey("When an equals condition has no operand", func() {
			got, err := Condition{Kind: CondEquals}.Matches(valueobjects.NewEnumValue("bike"), now)

			Convey("Then it simply does not match", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeFalse)
			})
		})

		Convey("When a range condition meets a source of a different type", func() {
			lo := valueobjects.NewIntegerValue(1)
			hi := valueobjects.NewIntegerValue(9)

			Convey("Then evaluating either bound surfaces the comparison error", func() {
				_, err := Condition{Kind: CondRange, Min: &lo}.Matches(valueobjects.NewFloatValue(5), now)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "cannot compare")

				_, err = Condition{Kind: CondRange, Max: &hi}.Matches(valueobjects.NewFloatValue(5), now)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When only an upper bound is set", func() {
			hi := valueobjects.NewIntegerValue(9)
			cond := Condition{Kind: CondRange, Max: &hi}

			Convey("Then values at and below the bound match and higher ones do not", func() {
				at, err := cond.Matches(valueobjects.NewIntegerValue(9), now)
				So(err, ShouldBeNil)
				So(at, ShouldBeTrue)

				above, err := cond.Matches(valueobjects.NewIntegerValue(10), now)
				So(err, ShouldBeNil)
				So(above, ShouldBeFalse)
			})
		})

		Convey("When a pattern condition is evaluated", func() {
			Convey("Then the source text is matched and a bad pattern errors", func() {
				got, err := Condition{Kind: CondPattern, Pattern: "^SKU-"}.
					Matches(valueobjects.NewStringValue("SKU-9"), now)
				So(err, ShouldBeNil)
				So(got, ShouldBeTrue)

				got, err = Condition{Kind: CondPattern, Pattern: "^SKU-"}.
					Matches(valueobjects.NewStringValue("PART-9"), now)
				So(err, ShouldBeNil)
				So(got, ShouldBeFalse)

				_, err = Condition{Kind: CondPattern, Pattern: "("}.
					Matches(valueobjects.NewStringValue("x"), now)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a dynamic condition is evaluated", func() {
			today := valueobjects.NewDateValue(now)
			yesterday := valueobjects.NewDateValue(now.AddDate(0, 0, -1))
			tomorrow := valueobjects.NewDateValue(now.AddDate(0, 0, 1))
			dyn := &valueobjects.DynamicValue{Kind: valueobjects.DynamicToday}

			match := func(op DynamicOp, v valueobjects.Value) bool {
				got, err := Condition{Kind: CondDynamic, Dynamic: dyn, Op: op}.Matches(v, now)
				So(err, ShouldBeNil)
				return got
			}

			Convey("Then each operator applies its own boundary rule", func() {
				So(match(OpBefore, yesterday), ShouldBeTrue)
				So(match(OpBefore, today), ShouldBeFalse)

				So(match(OpAfter, tomorrow), ShouldBeTrue)
				So(match(OpAfter, today), ShouldBeFalse)

				So(match(OpOnBefore, today), ShouldBeTrue)
				So(match(OpOnBefore, tomorrow), ShouldBeFalse)

				So(match(OpOnAfter, today), ShouldBeTrue)
				So(match(OpOnAfter, yesterday), ShouldBeFalse)
			})

			Convey("And an unknown operator errors at evaluation time", func() {
				_, err := Condition{Kind: CondDynamic, Dynamic: dyn, Op: DynamicOp("sideways")}.
					Matches(today, now)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown dynamic operator")
			})

			Convey("And a missing dynamic value simply does not match", func() {
				got, err := Condition{Kind: CondDynamic, Op: OpBefore}.Matches(today, now)
				So(err, ShouldBeNil)
				So(got, ShouldBeFalse)
			})

			Convey("And a non-temporal source surfaces the resolve error", func() {
				_, err := Condition{Kind: CondDynamic, Dynamic: dyn, Op: OpBefore}.
					Matches(valueobjects.NewStringValue("x"), now)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the condition kind is unknown", func() {
			_, err := Condition{Kind: ConditionKind("maybe")}.Matches(valueobjects.NewStringValue("x"), now)

			Convey("Then evaluation errors rather than silently matching", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown condition kind")
			})
		})
	})
}

func TestConditionJSONRoundTrip(t *testing.T) {
	Convey("Given conditions of every operand shape", t, func() {
		lo := valueobjects.NewIntegerValue(1)
		hi := valueobjects.NewIntegerValue(9)
		eq := valueobjects.NewEnumValue("bike")

		original := []Condition{
			{Kind: CondEquals, Value: &eq},
			{Kind: CondIn, Values: []valueobjects.Value{
				valueobjects.NewEnumValue("bike"), valueobjects.NewEnumValue("car"),
			}},
			{Kind: CondRange, Min: &lo, Max: &hi},
			{Kind: CondPattern, Pattern: "^SKU-"},
			{Kind: CondDynamic, Dynamic: &valueobjects.DynamicValue{Kind: valueobjects.DynamicToday}, Op: OpBefore},
		}

		Convey("When they round-trip through storage", func() {
			encoded, err := json.Marshal(original)
			So(err, ShouldBeNil)

			var restored []Condition
			So(json.Unmarshal(encoded, &restored), ShouldBeNil)

			Convey("Then every typed operand survives", func() {
				So(restored, ShouldHaveLength, 5)

				So(restored[0].Value.Equal(eq), ShouldBeTrue)

				So(restored[1].Values, ShouldHaveLength, 2)
				So(restored[1].Values[1].Equal(valueobjects.NewEnumValue("car")), ShouldBeTrue)

				So(restored[2].Min.Equal(lo), ShouldBeTrue)
				So(restored[2].Max.Equal(hi), ShouldBeTrue)

				So(restored[3].Pattern, ShouldEqual, "^SKU-")

				So(restored[4].Dynamic.Kind, ShouldEqual, valueobjects.DynamicToday)
				So(restored[4].Op, ShouldEqual, OpBefore)
			})
		})

		Convey("When a stored condition is corrupt", func() {
			Convey("Then decoding names the offending operand", func() {
				var c Condition
				So(json.Unmarshal([]byte(`{`), &c), ShouldNotBeNil)

				err := json.Unmarshal([]byte(`{"kind":"equals","value":{"type":"integer","value":"x"}}`), &c)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "condition value")

				err = json.Unmarshal([]byte(`{"kind":"range","min":{"type":"integer","value":"x"}}`), &c)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "condition min")

				err = json.Unmarshal([]byte(`{"kind":"range","max":{"type":"integer","value":"x"}}`), &c)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "condition max")

				err = json.Unmarshal([]byte(`{"kind":"in","values":[{"type":"integer","value":"x"}]}`), &c)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "condition values member")
			})
		})

		Convey("When a condition with no operands is decoded", func() {
			var c Condition
			err := json.Unmarshal([]byte(`{"kind":"pattern","pattern":"^a"}`), &c)

			Convey("Then the operand fields are left nil rather than zero values", func() {
				So(err, ShouldBeNil)
				So(c.Value, ShouldBeNil)
				So(c.Min, ShouldBeNil)
				So(c.Max, ShouldBeNil)
				So(c.Values, ShouldBeNil)
			})
		})
	})
}
