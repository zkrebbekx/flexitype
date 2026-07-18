package valueobjects

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestValueConstructorGuards(t *testing.T) {
	Convey("Given the validating value constructors", t, func() {
		Convey("When a URL is built", func() {
			Convey("Then it must be absolute and within the length ceiling", func() {
				v, err := NewURLValue("https://example.com/a?b=c#d")
				So(err, ShouldBeNil)
				So(v.DataType(), ShouldEqual, DataTypeURL)

				_, err = NewURLValue("/relative/path")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "must be absolute")

				_, err = NewURLValue("https://")
				So(err, ShouldNotBeNil)

				_, err = NewURLValue("https://example.com/" + strings.Repeat("x", 2048))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "exceeds 2048 characters")
			})
		})

		Convey("When an email is built", func() {
			Convey("Then display-name forms are rejected alongside garbage and overlong input", func() {
				v, err := NewEmailValue("dev@example.com")
				So(err, ShouldBeNil)
				So(v.Text(), ShouldEqual, "dev@example.com")

				// mail.ParseAddress accepts this, but it is not a bare address.
				_, err = NewEmailValue("Dev <dev@example.com>")
				So(err, ShouldNotBeNil)

				_, err = NewEmailValue("not-an-email")
				So(err, ShouldNotBeNil)

				_, err = NewEmailValue(strings.Repeat("a", 315) + "@example.com")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "exceeds 320 characters")
			})
		})

		Convey("When a decimal is built", func() {
			Convey("Then only canonical decimal text within the length ceiling is accepted", func() {
				for _, s := range []string{"0", "-1", "+2.5", "1234.560000"} {
					_, err := NewDecimalValue(s)
					So(err, ShouldBeNil)
				}
				for _, s := range []string{"", "1.", ".5", "1e3", "abc", strings.Repeat("9", 41)} {
					_, err := NewDecimalValue(s)
					So(err, ShouldNotBeNil)
				}
			})
		})

		Convey("When a JSON document is built", func() {
			Convey("Then invalid documents are refused and valid ones are compacted", func() {
				v, err := NewJSONValue(json.RawMessage(` { "a" :  1 } `))
				So(err, ShouldBeNil)
				So(string(v.JSON()), ShouldEqual, `{"a":1}`)

				_, err = NewJSONValue(json.RawMessage(`{"a":`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid json document")
			})
		})

		Convey("When media metadata is built", func() {
			Convey("Then object key, mime and a non-negative size are all required", func() {
				v, err := NewMediaValue(json.RawMessage(
					`{"object_key":"t/abc","mime":"image/png","size":12,"filename":"a.png"}`))
				So(err, ShouldBeNil)
				So(v.Media().ObjectKey, ShouldEqual, "t/abc")
				So(v.Media().Filename, ShouldEqual, "a.png")
				So(v.String(), ShouldEqual, "t/abc")

				_, err = NewMediaValue(json.RawMessage(`{"mime":"image/png","size":1}`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "object_key")

				_, err = NewMediaValue(json.RawMessage(`{"object_key":"k","size":1}`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "mime type")

				_, err = NewMediaValue(json.RawMessage(`{"object_key":"k","mime":"image/png","size":-1}`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "non-negative")

				_, err = NewMediaValue(json.RawMessage(`"not-an-object"`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode media metadata")
			})
		})

		Convey("When a quantity is built", func() {
			Convey("Then both magnitude and unit are required", func() {
				_, err := NewQuantityValue("", "kg", 0)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a magnitude")

				_, err = NewQuantityValue("5", "", 5)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a unit")

				v, err := NewQuantityValue("5", "kg", 5000)
				So(err, ShouldBeNil)
				So(v.String(), ShouldEqual, "5 kg")
				So(v.Quantity().Base, ShouldEqual, 5000)
			})
		})
	})
}

func TestParseValueEdges(t *testing.T) {
	Convey("Given ParseValue as the single API entry point for raw values", t, func() {
		Convey("When an unknown data type is requested", func() {
			_, err := ParseValue(DataType("colour"), json.RawMessage(`"red"`))

			Convey("Then it is rejected rather than silently stored as text", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `unknown data type "colour"`)
			})
		})

		Convey("When an enum arrives empty", func() {
			_, err := ParseValue(DataTypeEnum, json.RawMessage(`""`))

			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "must not be empty")
			})
		})

		Convey("When a decimal arrives as a JSON number", func() {
			v, err := ParseValue(DataTypeDecimal, json.RawMessage(`12.50`))

			Convey("Then the literal text is kept, avoiding float rounding", func() {
				So(err, ShouldBeNil)
				So(v.Text(), ShouldEqual, "12.50")
			})
		})

		Convey("When a decimal arrives as something else entirely", func() {
			_, err := ParseValue(DataTypeDecimal, json.RawMessage(`true`))

			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "expected decimal string or number")
			})
		})

		Convey("When scalars arrive with the wrong JSON shape", func() {
			Convey("Then each type reports what it expected", func() {
				bad := []struct {
					dt   DataType
					raw  string
					want string
				}{
					{DataTypeBool, `1`, "expected boolean"},
					{DataTypeString, `42`, "expected string"},
					{DataTypeEnum, `42`, "expected string"},
					{DataTypeInteger, `9.5`, "expected integer"},
					{DataTypeFloat, `"x"`, "expected number"},
					{DataTypeDate, `42`, "expected string"},
					{DataTypeTime, `"25:00:00"`, "expected time"},
					{DataTypeTime, `42`, "expected string"},
					{DataTypeDateTime, `"2026-07-11"`, "expected RFC 3339"},
					{DataTypeDateTime, `42`, "expected string"},
					{DataTypeURL, `42`, "expected string"},
					{DataTypeEmail, `42`, "expected string"},
					{DataTypeDecimal, `[`, "expected decimal"},
					{DataTypeQuantity, `"5 kg"`, "expected a quantity"},
				}
				for _, c := range bad {
					_, err := ParseValue(c.dt, json.RawMessage(c.raw))
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, c.want)
				}
			})
		})

		Convey("When a numeric type arrives as a quoted number", func() {
			i, intErr := ParseValue(DataTypeInteger, json.RawMessage(`"5"`))
			f, floatErr := ParseValue(DataTypeFloat, json.RawMessage(`"1.5"`))

			Convey("Then it is coerced, since json.Number accepts quoted numeric literals", func() {
				So(intErr, ShouldBeNil)
				So(i.Int(), ShouldEqual, 5)
				So(floatErr, ShouldBeNil)
				So(f.Float(), ShouldEqual, 1.5)
			})

			Convey("And a quoted non-number is still rejected", func() {
				_, err := ParseValue(DataTypeInteger, json.RawMessage(`"five"`))
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a quantity arrives with its base already folded", func() {
			v, err := ParseValue(DataTypeQuantity, json.RawMessage(`{"magnitude":"6000","unit":"g","base":6000}`))

			Convey("Then magnitude, unit and base all survive", func() {
				So(err, ShouldBeNil)
				So(v.Quantity(), ShouldResemble, Quantity{Magnitude: "6000", Unit: "g", Base: 6000})
			})
		})

		Convey("When media arrives through ParseValue", func() {
			v, err := ParseValue(DataTypeMedia, json.RawMessage(`{"object_key":"k","mime":"text/plain","size":3}`))

			Convey("Then it is validated by the media constructor", func() {
				So(err, ShouldBeNil)
				So(v.DataType(), ShouldEqual, DataTypeMedia)

				_, err = ParseValue(DataTypeMedia, json.RawMessage(`{"mime":"text/plain"}`))
				So(err, ShouldNotBeNil)
			})
		})
	})
}

func TestTypedValueRoundTrip(t *testing.T) {
	Convey("Given the self-describing persisted value form", t, func() {
		Convey("When the absent value is encoded", func() {
			raw, err := Value{}.MarshalTyped()

			Convey("Then it becomes null and decodes back to absent", func() {
				So(err, ShouldBeNil)
				So(string(raw), ShouldEqual, "null")

				back, err := UnmarshalTypedValue(raw)
				So(err, ShouldBeNil)
				So(back.IsZero(), ShouldBeTrue)
			})
		})

		Convey("When an empty payload is decoded", func() {
			back, err := UnmarshalTypedValue(nil)

			Convey("Then it decodes to absent rather than erroring", func() {
				So(err, ShouldBeNil)
				So(back.IsZero(), ShouldBeTrue)
			})
		})

		Convey("When a corrupt payload is decoded", func() {
			_, err := UnmarshalTypedValue(json.RawMessage(`{"type":`))

			Convey("Then decoding fails loudly", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "decode typed value")
			})
		})

		Convey("When a payload names an unknown type", func() {
			_, err := UnmarshalTypedValue(json.RawMessage(`{"type":"colour","value":"red"}`))

			Convey("Then it is refused", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When every data type round-trips", func() {
			media, err := NewMediaValue(json.RawMessage(`{"object_key":"k","mime":"image/png","size":9}`))
			So(err, ShouldBeNil)
			quantity, err := NewQuantityValue("2.5", "m", 2.5)
			So(err, ShouldBeNil)
			decimal, err := NewDecimalValue("1.25")
			So(err, ShouldBeNil)
			jsonVal, err := NewJSONValue(json.RawMessage(`{"k":[1,2]}`))
			So(err, ShouldBeNil)
			url, err := NewURLValue("https://example.com/x")
			So(err, ShouldBeNil)
			email, err := NewEmailValue("dev@example.com")
			So(err, ShouldBeNil)
			ts := time.Date(2026, 7, 11, 14, 30, 0, 0, time.UTC)

			originals := []Value{
				NewBoolValue(true), NewStringValue("hi"), NewEnumValue("red"),
				NewIntegerValue(-7), NewFloatValue(1.5), decimal,
				NewDateValue(ts), NewTimeValue(ts), NewDateTimeValue(ts),
				url, email, jsonVal, media, quantity,
			}

			Convey("Then each survives with its type and content intact", func() {
				for _, want := range originals {
					typed, err := want.MarshalTyped()
					So(err, ShouldBeNil)
					got, err := UnmarshalTypedValue(typed)
					So(err, ShouldBeNil)
					So(got.DataType(), ShouldEqual, want.DataType())
					So(got.Equal(want), ShouldBeTrue)
				}
			})
		})

		Convey("When the absent value is marshalled naturally", func() {
			raw, err := Value{}.MarshalJSON()

			Convey("Then it renders as JSON null", func() {
				So(err, ShouldBeNil)
				So(string(raw), ShouldEqual, "null")
			})
		})
	})
}

func TestValueEqualityAndOrdering(t *testing.T) {
	Convey("Given values of assorted types", t, func() {
		Convey("When JSON documents that differ only in key order are compared", func() {
			a, err := NewJSONValue(json.RawMessage(`{"a":1,"b":2}`))
			So(err, ShouldBeNil)
			b, err := NewJSONValue(json.RawMessage(`{"b":2,"a":1}`))
			So(err, ShouldBeNil)
			c, err := NewJSONValue(json.RawMessage(`{"a":1,"b":3}`))
			So(err, ShouldBeNil)

			Convey("Then they compare structurally equal, matching jsonb semantics", func() {
				So(a.Equal(b), ShouldBeTrue)
				So(a.Equal(c), ShouldBeFalse)
			})
		})

		Convey("When values of different types are compared", func() {
			Convey("Then they are never equal and never ordered", func() {
				So(NewIntegerValue(1).Equal(NewFloatValue(1)), ShouldBeFalse)
				So(NewStringValue("x").Equal(NewEnumValue("x")), ShouldBeFalse)

				_, err := NewIntegerValue(1).Compare(NewFloatValue(1))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "cannot compare integer with float")
			})
		})

		Convey("When unordered types are compared", func() {
			Convey("Then ordering is refused even between same-typed operands", func() {
				pairs := [][2]Value{
					{NewBoolValue(true), NewBoolValue(false)},
					{NewStringValue("a"), NewStringValue("b")},
					{NewEnumValue("a"), NewEnumValue("b")},
				}
				for _, p := range pairs {
					_, err := p[0].Compare(p[1])
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, "is not ordered")
				}
			})
		})

		Convey("When ordered types are compared", func() {
			ts := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
			later, _ := NewDecimalValue("2")
			earlier, _ := NewDecimalValue("1")
			// Same magnitude, different textual form: decimals order numerically.
			earlierAlias, _ := NewDecimalValue("1.0")

			Convey("Then each ordered family orders both ways and reports equality", func() {
				cmp, err := earlier.Compare(later)
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)
				cmp, err = later.Compare(earlier)
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, 1)
				cmp, err = earlier.Compare(earlierAlias)
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, 0)

				cmp, err = NewFloatValue(2).Compare(NewFloatValue(1))
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, 1)

				cmp, err = NewDateValue(ts).Compare(NewDateValue(ts.AddDate(0, 0, 1)))
				So(err, ShouldBeNil)
				So(cmp, ShouldEqual, -1)
			})
		})

		Convey("When media values are compared", func() {
			a, err := NewMediaValue(json.RawMessage(`{"object_key":"k","mime":"image/png","size":1}`))
			So(err, ShouldBeNil)
			b, err := NewMediaValue(json.RawMessage(`{"size":1,"mime":"image/png","object_key":"k"}`))
			So(err, ShouldBeNil)
			c, err := NewMediaValue(json.RawMessage(`{"object_key":"other","mime":"image/png","size":1}`))
			So(err, ShouldBeNil)

			Convey("Then canonicalised metadata compares equal and a different key does not", func() {
				So(a.Equal(b), ShouldBeTrue)
				So(a.Equal(c), ShouldBeFalse)
			})
		})

		Convey("When textual values report their length", func() {
			Convey("Then it counts runes rather than bytes", func() {
				So(NewStringValue("héllo").Length(), ShouldEqual, 5)
				So(NewStringValue("").Length(), ShouldEqual, 0)
			})
		})

		Convey("When the absent value is inspected", func() {
			Convey("Then it reports itself absent and renders empty", func() {
				So(Value{}.IsZero(), ShouldBeTrue)
				So(Value{}.String(), ShouldEqual, "")
				So(NewBoolValue(false).IsZero(), ShouldBeFalse)
			})
		})
	})
}

func TestDynamicValueGuards(t *testing.T) {
	Convey("Given dynamic (runtime-computed) values", t, func() {
		now := time.Date(2026, 7, 11, 14, 30, 0, 0, time.UTC)

		Convey("When 'now' or 'today' carries an offset", func() {
			Convey("Then the extra period or amount is rejected", func() {
				err := DynamicValue{Kind: DynamicNow, Period: PeriodDays}.Validate(DataTypeDateTime)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "does not accept period or amount")

				err = DynamicValue{Kind: DynamicToday, Amount: 3}.Validate(DataTypeDate)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a relative offset names an unknown period", func() {
			Convey("Then both validation and resolution refuse it", func() {
				d := DynamicValue{Kind: DynamicRelative, Period: RelativePeriod("fortnights"), Amount: 1}
				err := d.Validate(DataTypeDate)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown relative period")

				_, err = d.Resolve(DataTypeDate, now)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When the kind is unknown", func() {
			Convey("Then both validation and resolution refuse it", func() {
				d := DynamicValue{Kind: DynamicKind("yesterday")}
				err := d.Validate(DataTypeDate)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "unknown dynamic value kind")

				_, err = d.Resolve(DataTypeDate, now)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When a valid dynamic resolves against a non-temporal type", func() {
			_, err := DynamicValue{Kind: DynamicNow}.Resolve(DataTypeString, now)

			Convey("Then resolution refuses it", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "require a temporal data type")
			})
		})

		Convey("When each period is applied", func() {
			Convey("Then the offset uses the period's duration", func() {
				cases := []struct {
					period RelativePeriod
					amount int
					want   string
				}{
					{PeriodSeconds, 30, "2026-07-11T14:30:30Z"},
					{PeriodMinutes, 15, "2026-07-11T14:45:00Z"},
					{PeriodHours, -2, "2026-07-11T12:30:00Z"},
					{PeriodDays, 1, "2026-07-12T14:30:00Z"},
					{PeriodWeeks, 1, "2026-07-18T14:30:00Z"},
				}
				for _, c := range cases {
					d := DynamicValue{Kind: DynamicRelative, Period: c.period, Amount: c.amount}
					So(d.Validate(DataTypeDateTime), ShouldBeNil)
					v, err := d.Resolve(DataTypeDateTime, now)
					So(err, ShouldBeNil)
					So(v.String(), ShouldEqual, c.want)
				}
			})
		})

		Convey("When 'now' resolves for a time-of-day attribute", func() {
			v, err := DynamicValue{Kind: DynamicNow}.Resolve(DataTypeTime, now)

			Convey("Then the date portion is discarded", func() {
				So(err, ShouldBeNil)
				So(v.DataType(), ShouldEqual, DataTypeTime)
				So(v.String(), ShouldEqual, "14:30:00")
			})
		})
	})
}

func TestDefaults(t *testing.T) {
	Convey("Given attribute defaults", t, func() {
		now := time.Date(2026, 7, 11, 14, 30, 0, 0, time.UTC)

		Convey("When a default sets neither side", func() {
			Convey("Then validation and resolution both refuse it", func() {
				err := Default{}.Validate(DataTypeString)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "must set static or dynamic")

				_, err = Default{}.Resolve(DataTypeString, now)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "empty default")
			})
		})

		Convey("When a static default's type disagrees with the attribute", func() {
			s := NewStringValue("x")
			err := Default{Static: &s}.Validate(DataTypeInteger)

			Convey("Then it is rejected naming both types", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "string")
				So(err.Error(), ShouldContainSubstring, "integer")
			})
		})

		Convey("When a dynamic default is validated", func() {
			Convey("Then it defers to the dynamic value's own rules", func() {
				So(Default{Dynamic: &DynamicValue{Kind: DynamicToday}}.Validate(DataTypeDate), ShouldBeNil)
				So(Default{Dynamic: &DynamicValue{Kind: DynamicToday}}.Validate(DataTypeString), ShouldNotBeNil)
			})
		})

		Convey("When a dynamic default is resolved", func() {
			v, err := Default{Dynamic: &DynamicValue{Kind: DynamicToday}}.Resolve(DataTypeDate, now)

			Convey("Then it produces the runtime value", func() {
				So(err, ShouldBeNil)
				So(v.String(), ShouldEqual, "2026-07-11")
			})
		})

		Convey("When a dynamic default round-trips through JSON", func() {
			original := Default{Dynamic: &DynamicValue{Kind: DynamicRelative, Period: PeriodDays, Amount: -7}}
			encoded, err := json.Marshal(original)
			So(err, ShouldBeNil)

			var restored Default
			So(json.Unmarshal(encoded, &restored), ShouldBeNil)

			Convey("Then the dynamic side survives and the static side stays empty", func() {
				So(restored.Static, ShouldBeNil)
				So(restored.Dynamic, ShouldNotBeNil)
				So(restored.Dynamic.Kind, ShouldEqual, DynamicRelative)
				So(restored.Dynamic.Period, ShouldEqual, PeriodDays)
				So(restored.Dynamic.Amount, ShouldEqual, -7)
			})
		})

		Convey("When an explicitly null static side is decoded", func() {
			var restored Default
			err := json.Unmarshal([]byte(`{"static":null,"dynamic":{"kind":"now"}}`), &restored)

			Convey("Then the static side is left unset rather than becoming an absent value", func() {
				So(err, ShouldBeNil)
				So(restored.Static, ShouldBeNil)
				So(restored.Dynamic.Kind, ShouldEqual, DynamicNow)
			})
		})

		Convey("When a corrupt default is decoded", func() {
			var restored Default
			Convey("Then decoding fails for both malformed JSON and a bad static payload", func() {
				So(json.Unmarshal([]byte(`{`), &restored), ShouldNotBeNil)
				So(json.Unmarshal([]byte(`{"static":{"type":"integer","value":"nope"}}`), &restored), ShouldNotBeNil)
			})
		})
	})
}
