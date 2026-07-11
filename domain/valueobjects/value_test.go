package valueobjects

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseValue(t *testing.T) {
	Convey("Given raw JSON scalars for each data type", t, func() {
		Convey("When parsing well-typed values", func() {
			cases := []struct {
				dt   DataType
				raw  string
				want string
			}{
				{DataTypeBool, `true`, "true"},
				{DataTypeString, `"hello"`, "hello"},
				{DataTypeInteger, `42`, "42"},
				{DataTypeFloat, `3.14`, "3.14"},
				{DataTypeDecimal, `"1234.560000"`, "1234.560000"},
				{DataTypeDate, `"2026-07-11"`, "2026-07-11"},
				{DataTypeTime, `"14:30:00"`, "14:30:00"},
				{DataTypeDateTime, `"2026-07-11T14:30:00Z"`, "2026-07-11T14:30:00Z"},
				{DataTypeEnum, `"red"`, "red"},
				{DataTypeURL, `"https://example.com/x"`, "https://example.com/x"},
				{DataTypeEmail, `"dev@example.com"`, "dev@example.com"},
				{DataTypeJSON, `{"a": 1}`, `{"a":1}`},
			}
			Convey("Then each parses to a typed value rendering canonically", func() {
				for _, c := range cases {
					v, err := ParseValue(c.dt, json.RawMessage(c.raw))
					So(err, ShouldBeNil)
					So(v.DataType(), ShouldEqual, c.dt)
					So(v.String(), ShouldEqual, c.want)
				}
			})
		})

		Convey("When parsing mistyped values", func() {
			bad := []struct {
				dt  DataType
				raw string
			}{
				{DataTypeBool, `"yes"`},
				{DataTypeInteger, `3.5`},
				{DataTypeDate, `"11/07/2026"`},
				{DataTypeURL, `"not-a-url"`},
				{DataTypeEmail, `"not-an-email"`},
				{DataTypeDecimal, `"12.3.4"`},
			}
			Convey("Then parsing fails", func() {
				for _, c := range bad {
					_, err := ParseValue(c.dt, json.RawMessage(c.raw))
					So(err, ShouldNotBeNil)
				}
			})
		})
	})
}

func TestValueSemantics(t *testing.T) {
	Convey("Given typed values", t, func() {
		Convey("When comparing ordered values", func() {
			small := NewIntegerValue(1)
			large := NewIntegerValue(9)
			cmp, err := small.Compare(large)

			Convey("Then ordering works and mismatched types are rejected", func() {
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)

				_, err := small.Compare(NewStringValue("x"))
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When comparing decimals of different textual forms", func() {
			a, _ := NewDecimalValue("1.50")
			b, _ := NewDecimalValue("1.5")

			Convey("Then they compare numerically equal", func() {
				So(a.Equal(b), ShouldBeTrue)
				cmp, err := a.Compare(b)
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, 0)
			})
		})

		Convey("When round-tripping through the typed persisted form", func() {
			original, _ := NewDecimalValue("99.990000")
			typed, err := original.MarshalTyped()
			So(err, ShouldBeNil)

			restored, err := UnmarshalTypedValue(typed)

			Convey("Then the value survives with its type", func() {
				So(err, ShouldBeNil)
				So(restored.DataType(), ShouldEqual, DataTypeDecimal)
				So(restored.Equal(original), ShouldBeTrue)
			})
		})
	})
}

func TestDynamicValues(t *testing.T) {
	Convey("Given a fixed reference instant", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

		Convey("When resolving 'today' for a date attribute", func() {
			v, err := DynamicValue{Kind: DynamicToday}.Resolve(DataTypeDate, now)

			Convey("Then it yields the current date", func() {
				So(err, ShouldBeNil)
				So(v.String(), ShouldEqual, "2026-07-11")
			})
		})

		Convey("When resolving a relative offset", func() {
			v, err := DynamicValue{Kind: DynamicRelative, Period: PeriodDays, Amount: -7}.
				Resolve(DataTypeDate, now)

			Convey("Then it shifts by the period", func() {
				So(err, ShouldBeNil)
				So(v.String(), ShouldEqual, "2026-07-04")
			})
		})

		Convey("When validating dynamics against non-temporal types", func() {
			err := DynamicValue{Kind: DynamicNow}.Validate(DataTypeString)

			Convey("Then validation rejects them", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a default declares both static and dynamic sides", func() {
			s := NewIntegerValue(5)
			err := Default{Static: &s, Dynamic: &DynamicValue{Kind: DynamicNow}}.Validate(DataTypeInteger)

			Convey("Then validation rejects it", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a default round-trips through JSON", func() {
			s := NewIntegerValue(5)
			d := Default{Static: &s}
			encoded, err := json.Marshal(d)
			So(err, ShouldBeNil)

			var restored Default
			Convey("Then the typed static value survives", func() {
				So(json.Unmarshal(encoded, &restored), ShouldBeNil)
				So(restored.Static, ShouldNotBeNil)
				So(restored.Static.Equal(s), ShouldBeTrue)
			})
		})
	})
}
