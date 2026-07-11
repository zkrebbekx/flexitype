package dependency

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

func newAttr(typeDef valueobjects.TypeDefinitionID, name string, dt valueobjects.DataType, cs attribute.Constraints) *attribute.Definition {
	def, _, err := attribute.New(attribute.NewInput{
		TypeDefinitionID: typeDef,
		InternalName:     name,
		DisplayName:      name,
		DataType:         dt,
		Constraints:      cs,
	}, time.Now())
	So(err, ShouldBeNil)
	return def
}

func enumOf(members ...string) attribute.Constraints {
	values := make([]valueobjects.Value, 0, len(members))
	for _, m := range members {
		values = append(values, valueobjects.NewEnumValue(m))
	}
	return attribute.Constraints{attribute.OneOf{Values: values}}
}

func TestConditions(t *testing.T) {
	Convey("Given dependency conditions over source values", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

		Convey("When an equals condition is evaluated", func() {
			v := valueobjects.NewEnumValue("bike")
			cond := Condition{Kind: CondEquals, Value: &v}

			matched, err := cond.Matches(valueobjects.NewEnumValue("bike"), now)
			So(err, ShouldBeNil)
			notMatched, err := cond.Matches(valueobjects.NewEnumValue("car"), now)
			So(err, ShouldBeNil)
			absent, err := cond.Matches(valueobjects.Value{}, now)
			So(err, ShouldBeNil)

			Convey("Then only the exact value matches and absence never matches", func() {
				So(matched, ShouldBeTrue)
				So(notMatched, ShouldBeFalse)
				So(absent, ShouldBeFalse)
			})
		})

		Convey("When a range condition is evaluated", func() {
			lower := valueobjects.NewIntegerValue(10)
			upper := valueobjects.NewIntegerValue(20)
			cond := Condition{Kind: CondRange, Min: &lower, Max: &upper}

			inside, err := cond.Matches(valueobjects.NewIntegerValue(15), now)
			So(err, ShouldBeNil)
			below, err := cond.Matches(valueobjects.NewIntegerValue(9), now)
			So(err, ShouldBeNil)

			Convey("Then bounds are inclusive", func() {
				So(inside, ShouldBeTrue)
				So(below, ShouldBeFalse)
			})
		})

		Convey("When a dynamic condition compares against 'today'", func() {
			cond := Condition{
				Kind:    CondDynamic,
				Dynamic: &valueobjects.DynamicValue{Kind: valueobjects.DynamicToday},
				Op:      OpBefore,
			}

			past, err := cond.Matches(valueobjects.NewDateValue(now.AddDate(0, 0, -1)), now)
			So(err, ShouldBeNil)
			future, err := cond.Matches(valueobjects.NewDateValue(now.AddDate(0, 0, 1)), now)
			So(err, ShouldBeNil)

			Convey("Then dates before today match and later dates do not", func() {
				So(past, ShouldBeTrue)
				So(future, ShouldBeFalse)
			})
		})

		Convey("When conditions round-trip through JSON storage", func() {
			v := valueobjects.NewEnumValue("bike")
			original := []Condition{{Kind: CondEquals, Value: &v}}
			encoded, err := json.Marshal(original)
			So(err, ShouldBeNil)

			var restored []Condition
			Convey("Then typed operands survive", func() {
				So(json.Unmarshal(encoded, &restored), ShouldBeNil)
				So(restored, ShouldHaveLength, 1)
				So(restored[0].Value, ShouldNotBeNil)
				So(restored[0].Value.Equal(v), ShouldBeTrue)
			})
		})
	})
}

func TestDependencyAggregate(t *testing.T) {
	Convey("Given a category → subcategory cascading picklist", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
		typeDef := valueobjects.NewTypeDefinitionID()
		category := newAttr(typeDef, "category", valueobjects.DataTypeEnum, enumOf("bike", "car"))
		subcategory := newAttr(typeDef, "subcategory", valueobjects.DataTypeEnum, enumOf("mountain", "road", "sedan", "suv"))

		bike := valueobjects.NewEnumValue("bike")
		dep, evts, err := New(NewInput{
			Source:     category,
			Target:     subcategory,
			Conditions: []Condition{{Kind: CondEquals, Value: &bike}},
			Effect: Effect{AllowedValues: []valueobjects.Value{
				valueobjects.NewEnumValue("mountain"),
				valueobjects.NewEnumValue("road"),
			}},
		}, now)

		Convey("When the dependency is created", func() {
			Convey("Then it emits a created event", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, events.Type("flexitype.attribute_value_dependency.created"))
			})
		})

		Convey("When the entity's category is bike", func() {
			sourceValues := map[valueobjects.AttributeDefinitionID]valueobjects.Value{
				category.ID(): valueobjects.NewEnumValue("bike"),
			}
			schema, err := ResolveEffective(subcategory, []*Dependency{dep}, sourceValues, now)
			So(err, ShouldBeNil)

			Convey("Then the subcategory narrows to bike subcategories", func() {
				So(schema.Restricted, ShouldBeTrue)
				So(schema.AllowedValues, ShouldHaveLength, 2)
				So(schema.Check(valueobjects.NewEnumValue("mountain")), ShouldBeNil)
				err := schema.Check(valueobjects.NewEnumValue("sedan"))
				So(domainerrors.IsDependencyViolation(err), ShouldBeTrue)
			})
		})

		Convey("When the entity's category is car", func() {
			sourceValues := map[valueobjects.AttributeDefinitionID]valueobjects.Value{
				category.ID(): valueobjects.NewEnumValue("car"),
			}
			schema, err := ResolveEffective(subcategory, []*Dependency{dep}, sourceValues, now)
			So(err, ShouldBeNil)

			Convey("Then the dependency does not restrict the subcategory", func() {
				So(schema.Restricted, ShouldBeFalse)
				So(schema.Check(valueobjects.NewEnumValue("sedan")), ShouldBeNil)
			})
		})

		Convey("When two matched dependencies intersect to an empty set", func() {
			dep2, _, err := New(NewInput{
				Source:     category,
				Target:     subcategory,
				Conditions: []Condition{{Kind: CondEquals, Value: &bike}},
				Effect: Effect{AllowedValues: []valueobjects.Value{
					valueobjects.NewEnumValue("sedan"),
				}},
			}, now)
			So(err, ShouldBeNil)

			sourceValues := map[valueobjects.AttributeDefinitionID]valueobjects.Value{
				category.ID(): valueobjects.NewEnumValue("bike"),
			}
			schema, err := ResolveEffective(subcategory, []*Dependency{dep, dep2}, sourceValues, now)
			So(err, ShouldBeNil)

			Convey("Then nothing is allowed rather than everything", func() {
				So(schema.Restricted, ShouldBeTrue)
				So(schema.AllowedValues, ShouldBeEmpty)
				So(domainerrors.IsDependencyViolation(schema.Check(valueobjects.NewEnumValue("mountain"))), ShouldBeTrue)
			})
		})

		Convey("When a dependency targets its own source", func() {
			_, _, err := New(NewInput{
				Source:     category,
				Target:     category,
				Conditions: []Condition{{Kind: CondEquals, Value: &bike}},
				Effect:     Effect{Required: boolPtr(true)},
			}, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a required override matches", func() {
			dep3, _, err := New(NewInput{
				Source:     category,
				Target:     subcategory,
				Conditions: []Condition{{Kind: CondEquals, Value: &bike}},
				Effect:     Effect{Required: boolPtr(true)},
			}, now)
			So(err, ShouldBeNil)

			sourceValues := map[valueobjects.AttributeDefinitionID]valueobjects.Value{
				category.ID(): valueobjects.NewEnumValue("bike"),
			}
			schema, err := ResolveEffective(subcategory, []*Dependency{dep3}, sourceValues, now)
			So(err, ShouldBeNil)

			Convey("Then an absent value violates the dependency", func() {
				So(schema.Required, ShouldBeTrue)
				So(domainerrors.IsDependencyViolation(schema.Check(valueobjects.Value{})), ShouldBeTrue)
			})
		})
	})
}

func boolPtr(b bool) *bool { return &b }
