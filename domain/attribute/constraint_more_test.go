package attribute

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func mediaValue(t *testing.T, mime string, size int64) valueobjects.Value {
	t.Helper()
	raw, err := json.Marshal(valueobjects.MediaMeta{ObjectKey: "k", MIME: mime, Size: size})
	So(err, ShouldBeNil)
	v, err := valueobjects.NewMediaValue(raw)
	So(err, ShouldBeNil)
	return v
}

func TestConstraintValidateAgainstDataType(t *testing.T) {
	Convey("Given constraints declared against an attribute's data type", t, func() {
		Convey("When a length constraint carries a nonsensical bound", func() {
			Convey("Then a negative min_length and a non-positive max_length are rejected", func() {
				So(domainerrors.IsValidation(MinLength{N: -1}.Validate(valueobjects.DataTypeString)), ShouldBeTrue)
				So(domainerrors.IsValidation(MaxLength{N: 0}.Validate(valueobjects.DataTypeString)), ShouldBeTrue)
				So(domainerrors.IsValidation(MaxLength{N: -3}.Validate(valueobjects.DataTypeString)), ShouldBeTrue)
			})

			Convey("Then a zero min_length is allowed", func() {
				So(MinLength{N: 0}.Validate(valueobjects.DataTypeString), ShouldBeNil)
			})
		})

		Convey("When a bound constraint is declared on an unordered data type", func() {
			Convey("Then min_value and max_value are rejected", func() {
				So(domainerrors.IsValidation(
					MinValue{Min: valueobjects.NewBoolValue(true)}.Validate(valueobjects.DataTypeBool)), ShouldBeTrue)
				So(domainerrors.IsValidation(
					MaxValue{Max: valueobjects.NewBoolValue(true)}.Validate(valueobjects.DataTypeBool)), ShouldBeTrue)
			})
		})

		Convey("When a bound constraint's operand type differs from the attribute type", func() {
			Convey("Then the mismatch is rejected for both bounds", func() {
				So(domainerrors.IsValidation(
					MinValue{Min: valueobjects.NewStringValue("x")}.Validate(valueobjects.DataTypeInteger)), ShouldBeTrue)
				So(domainerrors.IsValidation(
					MaxValue{Max: valueobjects.NewStringValue("x")}.Validate(valueobjects.DataTypeInteger)), ShouldBeTrue)
			})
		})

		Convey("When a max_value operand matches an ordered attribute type", func() {
			Convey("Then it is accepted", func() {
				So(MaxValue{Max: valueobjects.NewIntegerValue(9)}.Validate(valueobjects.DataTypeInteger), ShouldBeNil)
			})
		})

		Convey("When a pattern is declared", func() {
			Convey("Then a non-textual data type is rejected", func() {
				So(domainerrors.IsValidation(
					Pattern{Expr: "^a"}.Validate(valueobjects.DataTypeInteger)), ShouldBeTrue)
			})

			Convey("Then an uncompilable expression is rejected", func() {
				So(domainerrors.IsValidation(
					Pattern{Expr: "a[("}.Validate(valueobjects.DataTypeString)), ShouldBeTrue)
			})
		})

		Convey("When a one_of constraint is declared", func() {
			Convey("Then an empty allowed set is rejected", func() {
				So(domainerrors.IsValidation(
					OneOf{}.Validate(valueobjects.DataTypeEnum)), ShouldBeTrue)
			})

			Convey("Then a member whose type differs from the attribute type is rejected", func() {
				c := OneOf{Values: []valueobjects.Value{valueobjects.NewIntegerValue(1)}}
				So(domainerrors.IsValidation(c.Validate(valueobjects.DataTypeString)), ShouldBeTrue)
			})
		})

		Convey("When the same kind is declared twice", func() {
			err := Constraints{MinLength{N: 1}, MinLength{N: 2}}.Validate(valueobjects.DataTypeString)

			Convey("Then the duplicate is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "duplicate")
			})
		})
	})
}

func TestMediaConstraint(t *testing.T) {
	Convey("Given a media constraint", t, func() {
		Convey("When it is validated against a data type", func() {
			Convey("Then it requires a media attribute", func() {
				So(MediaConstraint{}.Validate(valueobjects.DataTypeMedia), ShouldBeNil)
				So(domainerrors.IsValidation(
					MediaConstraint{}.Validate(valueobjects.DataTypeString)), ShouldBeTrue)
			})

			Convey("Then a negative max_size is rejected", func() {
				So(domainerrors.IsValidation(
					MediaConstraint{MaxSize: -1}.Validate(valueobjects.DataTypeMedia)), ShouldBeTrue)
			})
		})

		Convey("When it restricts MIME types", func() {
			c := MediaConstraint{AllowedMIME: []string{"image/png", "image/jpeg"}}

			Convey("Then an allowed type passes and a disallowed type is rejected", func() {
				So(c.Check(mediaValue(t, "image/png", 10)), ShouldBeNil)
				So(c.Check(mediaValue(t, "image/jpeg", 10)), ShouldBeNil)

				err := c.Check(mediaValue(t, "application/pdf", 10))
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "not allowed")
			})
		})

		Convey("When it caps the upload size", func() {
			c := MediaConstraint{MaxSize: 100}

			Convey("Then the cap is inclusive and oversize uploads are rejected", func() {
				So(c.Check(mediaValue(t, "image/png", 100)), ShouldBeNil)

				err := c.Check(mediaValue(t, "image/png", 101))
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "maximum size")
			})
		})

		Convey("When it is unrestricted", func() {
			Convey("Then any MIME type and size pass", func() {
				So(MediaConstraint{}.Check(mediaValue(t, "application/octet-stream", 1<<30)), ShouldBeNil)
			})
		})
	})
}

func TestConstraintCheckTypeMismatch(t *testing.T) {
	Convey("Given a bound constraint whose operand cannot be compared to the value", t, func() {
		Convey("When a value of an incomparable type is checked", func() {
			minErr := MinValue{Min: valueobjects.NewIntegerValue(5)}.Check(valueobjects.NewStringValue("abc"))
			maxErr := MaxValue{Max: valueobjects.NewIntegerValue(5)}.Check(valueobjects.NewStringValue("abc"))

			Convey("Then the comparison failure surfaces as a validation error", func() {
				So(domainerrors.IsValidation(minErr), ShouldBeTrue)
				So(domainerrors.IsValidation(maxErr), ShouldBeTrue)
			})
		})
	})
}

func TestPatternCheckWithUncompiledExpression(t *testing.T) {
	Convey("Given a pattern constraint that was never compiled", t, func() {
		Convey("When its expression is invalid and a value is checked", func() {
			err := Pattern{Expr: "a[("}.Check(valueobjects.NewStringValue("anything"))

			Convey("Then the check fails closed rather than admitting the value", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "invalid pattern")
			})
		})

		Convey("When its expression is valid, it compiles lazily and still matches", func() {
			c := Pattern{Expr: `^\d+$`}

			Convey("Then matching values pass and others are rejected", func() {
				So(c.Check(valueobjects.NewStringValue("123")), ShouldBeNil)
				So(domainerrors.IsValidation(c.Check(valueobjects.NewStringValue("12a"))), ShouldBeTrue)
			})
		})
	})
}

// unknownConstraint stands in for a constraint kind the JSON encoder does not
// know about, so the encoder's fail-loud behaviour can be asserted: silently
// dropping an unrecognised rule would relax validation on the way to storage.
type unknownConstraint struct{}

func (unknownConstraint) Kind() ConstraintKind                 { return ConstraintKind("wormhole") }
func (unknownConstraint) Validate(valueobjects.DataType) error { return nil }
func (unknownConstraint) Check(valueobjects.Value) error       { return nil }

func TestConstraintsJSON(t *testing.T) {
	Convey("Given constraints crossing the JSONB storage boundary", t, func() {
		Convey("When a media constraint round-trips", func() {
			original := Constraints{MediaConstraint{AllowedMIME: []string{"image/png"}, MaxSize: 2048}}
			encoded, err := json.Marshal(original)
			So(err, ShouldBeNil)

			var restored Constraints
			So(json.Unmarshal(encoded, &restored), ShouldBeNil)

			Convey("Then its MIME allow-list and size cap survive", func() {
				So(restored, ShouldHaveLength, 1)
				mc, ok := restored[0].(MediaConstraint)
				So(ok, ShouldBeTrue)
				So(mc.AllowedMIME, ShouldResemble, []string{"image/png"})
				So(mc.MaxSize, ShouldEqual, 2048)
			})
		})

		Convey("When a media constraint carries no size cap", func() {
			encoded, err := json.Marshal(Constraints{MediaConstraint{AllowedMIME: []string{"image/png"}}})
			So(err, ShouldBeNil)

			var restored Constraints
			So(json.Unmarshal(encoded, &restored), ShouldBeNil)

			Convey("Then it restores as unbounded", func() {
				So(restored[0].(MediaConstraint).MaxSize, ShouldEqual, 0)
			})
		})

		Convey("When min_length and min_value round-trip", func() {
			encoded, err := json.Marshal(Constraints{
				MinLength{N: 2},
				MinValue{Min: valueobjects.NewIntegerValue(4)},
			})
			So(err, ShouldBeNil)

			var restored Constraints
			So(json.Unmarshal(encoded, &restored), ShouldBeNil)

			Convey("Then both restore with their operands intact", func() {
				So(restored[0].(MinLength).N, ShouldEqual, 2)
				So(restored[1].(MinValue).Min.Int(), ShouldEqual, 4)
			})
		})

		Convey("When an unknown constraint kind is encoded", func() {
			_, err := json.Marshal(Constraints{unknownConstraint{}})

			Convey("Then encoding fails rather than silently dropping the rule", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "wormhole")
			})
		})

		Convey("When stored JSON is malformed", func() {
			cases := []struct {
				name string
				raw  string
			}{
				{"an unknown kind", `[{"kind":"wormhole"}]`},
				{"min_length without n", `[{"kind":"min_length"}]`},
				{"max_length without n", `[{"kind":"max_length"}]`},
				{"min_value with a bad operand", `[{"kind":"min_value","value":{"type":"nope","value":1}}]`},
				{"max_value with a bad operand", `[{"kind":"max_value","value":{"type":"nope","value":1}}]`},
				{"one_of with a bad member", `[{"kind":"one_of","values":[{"type":"nope","value":1}]}]`},
				{"pattern with an uncompilable expression", `[{"kind":"pattern","expr":"a[("}]`},
				{"a non-array payload", `{"kind":"min_length"}`},
			}

			for _, c := range cases {
				Convey("Then decoding "+c.name+" is rejected", func() {
					var restored Constraints
					So(json.Unmarshal([]byte(c.raw), &restored), ShouldNotBeNil)
				})
			}
		})
	})
}
