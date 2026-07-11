package attribute

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func newTestDefinition(dt valueobjects.DataType, cs Constraints, required bool) *Definition {
	def, _, err := New(NewInput{
		TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
		InternalName:     "test_attr",
		DisplayName:      "Test Attribute",
		DataType:         dt,
		Required:         required,
		Constraints:      cs,
	}, time.Now())
	So(err, ShouldBeNil)
	return def
}

func TestAttributeDefinition(t *testing.T) {
	Convey("Given a valid attribute definition input", t, func() {
		now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
		input := NewInput{
			TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
			InternalName:     "weight_kg",
			DisplayName:      "Weight (kg)",
			DataType:         valueobjects.DataTypeFloat,
			Required:         true,
		}

		Convey("When the definition is created", func() {
			def, evts, err := New(input, now)

			Convey("Then the aggregate is version 1 and a created event is returned", func() {
				So(err, ShouldBeNil)
				So(def.Version(), ShouldEqual, 1)
				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, "flexitype.attribute_definition.created")
				So(evts[0].AggregateID(), ShouldEqual, def.ID().String())
			})
		})

		Convey("When the internal name is not snake_case", func() {
			input.InternalName = "WeightKG"
			_, _, err := New(input, now)

			Convey("Then creation fails validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When an enum attribute lacks a one_of constraint", func() {
			input.DataType = valueobjects.DataTypeEnum
			_, _, err := New(input, now)

			Convey("Then creation fails validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When it is updated", func() {
			def, _, err := New(input, now)
			So(err, ShouldBeNil)

			evts, err := def.Update(UpdateInput{DisplayName: "Mass (kg)", Required: false}, now.Add(time.Hour))

			Convey("Then the version bumps and an updated event is returned", func() {
				So(err, ShouldBeNil)
				So(def.Version(), ShouldEqual, 2)
				So(def.DisplayName(), ShouldEqual, "Mass (kg)")
				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, "flexitype.attribute_definition.updated")
			})
		})

		Convey("When it is archived and mutated again", func() {
			def, _, err := New(input, now)
			So(err, ShouldBeNil)

			evts, err := def.Archive(now.Add(time.Hour))
			So(err, ShouldBeNil)
			So(evts[0].EventType(), ShouldEqual, "flexitype.attribute_definition.archived")

			_, err = def.Update(UpdateInput{DisplayName: "X"}, now.Add(2*time.Hour))

			Convey("Then the mutation is rejected as archived", func() {
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})
	})
}

func TestConstraints(t *testing.T) {
	Convey("Given constraints applied to typed values", t, func() {
		Convey("When length constraints guard a string attribute", func() {
			def := newTestDefinition(valueobjects.DataTypeString, Constraints{
				MinLength{N: 3},
				MaxLength{N: 5},
			}, false)

			Convey("Then in-range values pass and out-of-range values fail", func() {
				So(def.ValidateValue(valueobjects.NewStringValue("abcd")), ShouldBeNil)
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.NewStringValue("ab"))), ShouldBeTrue)
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.NewStringValue("abcdef"))), ShouldBeTrue)
			})
		})

		Convey("When value bounds guard an integer attribute", func() {
			def := newTestDefinition(valueobjects.DataTypeInteger, Constraints{
				MinValue{Min: valueobjects.NewIntegerValue(0)},
				MaxValue{Max: valueobjects.NewIntegerValue(300)},
			}, false)

			Convey("Then bounds are inclusive", func() {
				So(def.ValidateValue(valueobjects.NewIntegerValue(0)), ShouldBeNil)
				So(def.ValidateValue(valueobjects.NewIntegerValue(300)), ShouldBeNil)
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.NewIntegerValue(-1))), ShouldBeTrue)
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.NewIntegerValue(301))), ShouldBeTrue)
			})
		})

		Convey("When a one_of constraint guards an enum attribute", func() {
			def := newTestDefinition(valueobjects.DataTypeEnum, Constraints{
				OneOf{Values: []valueobjects.Value{
					valueobjects.NewEnumValue("red"),
					valueobjects.NewEnumValue("green"),
				}},
			}, false)

			Convey("Then only members pass", func() {
				So(def.ValidateValue(valueobjects.NewEnumValue("red")), ShouldBeNil)
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.NewEnumValue("blue"))), ShouldBeTrue)
			})
		})

		Convey("When a pattern constraint guards a string attribute", func() {
			def := newTestDefinition(valueobjects.DataTypeString, Constraints{
				Pattern{Expr: `^[A-Z]{2}-\d{4}$`},
			}, false)

			Convey("Then only matching values pass", func() {
				So(def.ValidateValue(valueobjects.NewStringValue("AB-1234")), ShouldBeNil)
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.NewStringValue("nope"))), ShouldBeTrue)
			})
		})

		Convey("When a required attribute receives no value", func() {
			def := newTestDefinition(valueobjects.DataTypeString, nil, true)

			Convey("Then validation demands a value", func() {
				So(domainerrors.IsValidation(def.ValidateValue(valueobjects.Value{})), ShouldBeTrue)
			})
		})

		Convey("When length constraints are declared on a numeric attribute", func() {
			err := Constraints{MinLength{N: 2}}.Validate(valueobjects.DataTypeInteger)

			Convey("Then the constraint set is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When constraints round-trip through JSON storage", func() {
			original := Constraints{
				MinLength{N: 1},
				MaxValue{Max: valueobjects.NewIntegerValue(10)},
				Pattern{Expr: `^x`},
				OneOf{Values: []valueobjects.Value{valueobjects.NewIntegerValue(1)}},
			}
			encoded, err := json.Marshal(original)
			So(err, ShouldBeNil)

			var restored Constraints
			Convey("Then every kind survives with typed operands", func() {
				So(json.Unmarshal(encoded, &restored), ShouldBeNil)
				So(restored, ShouldHaveLength, 4)
				So(restored[0].Kind(), ShouldEqual, KindMinLength)
				So(restored[1].Kind(), ShouldEqual, KindMaxValue)
				maxValue := restored[1].(MaxValue)
				So(maxValue.Max.DataType(), ShouldEqual, valueobjects.DataTypeInteger)
				So(maxValue.Max.Int(), ShouldEqual, 10)
			})
		})
	})
}
