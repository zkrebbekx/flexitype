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

func TestDependencyConstruction(t *testing.T) {
	Convey("Given a source and target attribute on one type", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
		typeDef := valueobjects.NewTypeDefinitionID()
		category := newAttr(typeDef, "category", valueobjects.DataTypeEnum, enumOf("bike", "car"))
		subcategory := newAttr(typeDef, "subcategory", valueobjects.DataTypeEnum, enumOf("mountain", "road"))
		bike := valueobjects.NewEnumValue("bike")
		conds := []Condition{{Kind: CondEquals, Value: &bike}}

		Convey("When either attribute is missing", func() {
			_, _, srcErr := New(NewInput{Target: subcategory, Conditions: conds,
				Effect: Effect{Required: boolPtr(true)}}, now)
			_, _, tgtErr := New(NewInput{Source: category, Conditions: conds,
				Effect: Effect{Required: boolPtr(true)}}, now)

			Convey("Then construction is rejected", func() {
				So(domainerrors.IsValidation(srcErr), ShouldBeTrue)
				So(srcErr.Error(), ShouldContainSubstring, "source and target attributes are required")
				So(domainerrors.IsValidation(tgtErr), ShouldBeTrue)
			})
		})

		Convey("When no condition is given", func() {
			_, _, err := New(NewInput{Source: category, Target: subcategory,
				Effect: Effect{Required: boolPtr(true)}}, now)

			Convey("Then construction is rejected — a rule with no predicate always fires", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "at least one condition is required")
			})
		})

		Convey("When a condition does not fit the source's type", func() {
			wrong := valueobjects.NewIntegerValue(1)
			_, _, err := New(NewInput{Source: category, Target: subcategory,
				Conditions: []Condition{{Kind: CondEquals, Value: &wrong}},
				Effect:     Effect{Required: boolPtr(true)}}, now)

			Convey("Then construction is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "must match the source attribute type")
			})
		})

		Convey("When the effect changes nothing", func() {
			_, _, err := New(NewInput{Source: category, Target: subcategory,
				Conditions: conds, Effect: Effect{}}, now)

			Convey("Then construction is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "must narrow values, add constraints or override required")
			})
		})

		Convey("When an allowed value does not fit the target's type", func() {
			_, _, err := New(NewInput{Source: category, Target: subcategory,
				Conditions: conds,
				Effect:     Effect{AllowedValues: []valueobjects.Value{valueobjects.NewStringValue("mountain")}}}, now)

			Convey("Then construction is rejected naming both types", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "allowed value type must match the target attribute type")
			})
		})

		Convey("When the effect's constraints do not fit the target's type", func() {
			_, _, err := New(NewInput{Source: category, Target: subcategory,
				Conditions: conds,
				// MinValue is an ordered-type rule; the target is an enum.
				Effect: Effect{Constraints: attribute.Constraints{
					attribute.MinValue{Min: valueobjects.NewIntegerValue(1)},
				}}}, now)

			Convey("Then construction is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When no tenant is supplied", func() {
			dep, _, err := New(NewInput{Source: category, Target: subcategory,
				Conditions: conds, Effect: Effect{Required: boolPtr(true)},
				Description: "bike needs a subcategory"}, now)

			Convey("Then it falls back to the default tenant and records its provenance", func() {
				So(err, ShouldBeNil)
				So(dep.TenantID(), ShouldEqual, valueobjects.DefaultTenant)
				So(dep.SourceAttributeID().Equals(category.ID()), ShouldBeTrue)
				So(dep.TargetAttributeID().Equals(subcategory.ID()), ShouldBeTrue)
				So(dep.Conditions(), ShouldHaveLength, 1)
				So(dep.Description(), ShouldEqual, "bike needs a subcategory")
				So(dep.Version(), ShouldEqual, 1)
				So(dep.CreatedAt(), ShouldEqual, now)
				So(dep.UpdatedAt(), ShouldEqual, now)
				So(dep.ArchivedAt(), ShouldBeNil)
				So(dep.IsArchived(), ShouldBeFalse)
			})
		})

		Convey("When an explicit tenant is supplied", func() {
			dep, _, err := New(NewInput{TenantID: valueobjects.TenantID("acme"),
				Source: category, Target: subcategory,
				Conditions: conds, Effect: Effect{Required: boolPtr(true)}}, now)

			Convey("Then it is retained", func() {
				So(err, ShouldBeNil)
				So(dep.TenantID(), ShouldEqual, valueobjects.TenantID("acme"))
			})
		})
	})
}

func TestDependencyLifecycle(t *testing.T) {
	Convey("Given a live dependency", t, func() {
		now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
		later := now.Add(time.Hour)
		typeDef := valueobjects.NewTypeDefinitionID()
		category := newAttr(typeDef, "category", valueobjects.DataTypeEnum, enumOf("bike", "car"))
		subcategory := newAttr(typeDef, "subcategory", valueobjects.DataTypeEnum, enumOf("mountain", "road", "sedan"))
		other := newAttr(typeDef, "colour", valueobjects.DataTypeEnum, enumOf("red"))

		bike := valueobjects.NewEnumValue("bike")
		car := valueobjects.NewEnumValue("car")
		dep, _, err := New(NewInput{
			Source: category, Target: subcategory,
			Conditions: []Condition{{Kind: CondEquals, Value: &bike}},
			Effect:     Effect{AllowedValues: []valueobjects.Value{valueobjects.NewEnumValue("mountain")}},
		}, now)
		So(err, ShouldBeNil)

		Convey("When it is updated with a new rule", func() {
			evts, err := dep.Update(category, subcategory, UpdateInput{
				Conditions:  []Condition{{Kind: CondEquals, Value: &car}},
				Effect:      Effect{AllowedValues: []valueobjects.Value{valueobjects.NewEnumValue("sedan")}},
				Description: "cars get sedans",
			}, later)

			Convey("Then the rule is replaced, the version bumps and an update event is emitted", func() {
				So(err, ShouldBeNil)
				So(dep.Version(), ShouldEqual, 2)
				So(dep.UpdatedAt(), ShouldEqual, later)
				So(dep.Description(), ShouldEqual, "cars get sedans")
				So(dep.Effect().AllowedValues[0].Equal(valueobjects.NewEnumValue("sedan")), ShouldBeTrue)

				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, events.Type("flexitype.attribute_value_dependency.updated"))
				So(evts[0].AggregateID(), ShouldEqual, dep.ID().String())
				So(evts[0].OccurredWhen(), ShouldEqual, later)

				matched, err := dep.Matches(car, later)
				So(err, ShouldBeNil)
				So(matched, ShouldBeTrue)
				matched, err = dep.Matches(bike, later)
				So(err, ShouldBeNil)
				So(matched, ShouldBeFalse)
			})
		})

		Convey("When an update supplies the wrong attributes", func() {
			valid := UpdateInput{
				Conditions: []Condition{{Kind: CondEquals, Value: &car}},
				Effect:     Effect{Required: boolPtr(true)},
			}

			Convey("Then source and target are refused as immutable", func() {
				_, err := dep.Update(nil, subcategory, valid, later)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "must match the dependency's attributes")

				_, err = dep.Update(category, nil, valid, later)
				So(domainerrors.IsValidation(err), ShouldBeTrue)

				_, err = dep.Update(other, subcategory, valid, later)
				So(domainerrors.IsValidation(err), ShouldBeTrue)

				_, err = dep.Update(category, other, valid, later)
				So(domainerrors.IsValidation(err), ShouldBeTrue)

				So(dep.Version(), ShouldEqual, 1)
			})
		})

		Convey("When an update carries an invalid rule", func() {
			_, err := dep.Update(category, subcategory, UpdateInput{
				Conditions: []Condition{{Kind: CondEquals, Value: &car}},
				Effect:     Effect{},
			}, later)

			Convey("Then it is rejected and the aggregate is untouched", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(dep.Version(), ShouldEqual, 1)
				So(dep.UpdatedAt(), ShouldEqual, now)
			})
		})

		Convey("When it is archived", func() {
			evts, err := dep.Archive(later)

			Convey("Then it is marked archived and an archived event is emitted", func() {
				So(err, ShouldBeNil)
				So(dep.IsArchived(), ShouldBeTrue)
				So(dep.ArchivedAt(), ShouldNotBeNil)
				So(*dep.ArchivedAt(), ShouldEqual, later)
				So(dep.UpdatedAt(), ShouldEqual, later)

				So(evts, ShouldHaveLength, 1)
				So(evts[0].EventType(), ShouldEqual, events.Type("flexitype.attribute_value_dependency.archived"))
				So(evts[0].AggregateType(), ShouldEqual, AggregateType)
			})

			Convey("And archiving again is refused", func() {
				_, err := dep.Archive(later.Add(time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And updating an archived dependency is refused", func() {
				_, err := dep.Update(category, subcategory, UpdateInput{
					Conditions: []Condition{{Kind: CondEquals, Value: &car}},
					Effect:     Effect{Required: boolPtr(true)},
				}, later.Add(time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
				So(dep.Version(), ShouldEqual, 1)
			})

			Convey("And an archived dependency no longer contributes to the effective schema", func() {
				schema, err := ResolveEffective(subcategory, []*Dependency{dep},
					map[valueobjects.AttributeDefinitionID]valueobjects.Value{category.ID(): bike}, later)
				So(err, ShouldBeNil)
				So(schema.Restricted, ShouldBeFalse)
			})
		})

		Convey("When a dependency aimed at another attribute is resolved", func() {
			schema, err := ResolveEffective(other, []*Dependency{dep},
				map[valueobjects.AttributeDefinitionID]valueobjects.Value{category.ID(): bike}, now)

			Convey("Then it is skipped rather than misapplied", func() {
				So(err, ShouldBeNil)
				So(schema.Restricted, ShouldBeFalse)
			})
		})

		Convey("When a condition errors during resolution", func() {
			broken, _, err := New(NewInput{
				Source: category, Target: subcategory,
				Conditions: []Condition{{Kind: CondPattern, Pattern: "^b"}},
				Effect:     Effect{Required: boolPtr(true)},
			}, now)
			So(err, ShouldBeNil)
			// Corrupt the compiled pattern the way a bad stored rule would.
			broken.conditions[0].Pattern = "("

			_, err = ResolveEffective(subcategory, []*Dependency{broken},
				map[valueobjects.AttributeDefinitionID]valueobjects.Value{category.ID(): bike}, now)

			Convey("Then resolution fails rather than silently ignoring the rule", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When it round-trips through a snapshot", func() {
			snap := dep.Snapshot()
			encoded, err := json.Marshal(snap)
			So(err, ShouldBeNil)

			var decoded Snapshot
			So(json.Unmarshal(encoded, &decoded), ShouldBeNil)
			restored := Rehydrate(decoded)

			Convey("Then every field survives storage", func() {
				So(restored.ID().Equals(dep.ID()), ShouldBeTrue)
				So(restored.TenantID(), ShouldEqual, dep.TenantID())
				So(restored.SourceAttributeID().Equals(category.ID()), ShouldBeTrue)
				So(restored.TargetAttributeID().Equals(subcategory.ID()), ShouldBeTrue)
				So(restored.Version(), ShouldEqual, dep.Version())
				So(restored.IsArchived(), ShouldBeFalse)
				So(restored.Conditions(), ShouldHaveLength, 1)
				So(restored.Conditions()[0].Value.Equal(bike), ShouldBeTrue)
				So(restored.Effect().AllowedValues, ShouldHaveLength, 1)
				So(restored.Effect().AllowedValues[0].Equal(valueobjects.NewEnumValue("mountain")), ShouldBeTrue)
			})
		})
	})
}

func TestEffectJSON(t *testing.T) {
	Convey("Given an effect carrying every field", t, func() {
		effect := Effect{
			AllowedValues: []valueobjects.Value{
				valueobjects.NewEnumValue("mountain"), valueobjects.NewEnumValue("road"),
			},
			Constraints: attribute.Constraints{attribute.MinLength{N: 2}},
			Required:    boolPtr(true),
		}

		Convey("When it round-trips through storage", func() {
			encoded, err := json.Marshal(effect)
			So(err, ShouldBeNil)

			var restored Effect
			So(json.Unmarshal(encoded, &restored), ShouldBeNil)

			Convey("Then allowed values keep their type and the overrides survive", func() {
				So(restored.AllowedValues, ShouldHaveLength, 2)
				So(restored.AllowedValues[0].DataType(), ShouldEqual, valueobjects.DataTypeEnum)
				So(restored.AllowedValues[0].Equal(valueobjects.NewEnumValue("mountain")), ShouldBeTrue)
				So(restored.Required, ShouldNotBeNil)
				So(*restored.Required, ShouldBeTrue)
				So(restored.Constraints, ShouldHaveLength, 1)
			})
		})

		Convey("When a stored effect is corrupt", func() {
			var restored Effect
			Convey("Then decoding fails rather than dropping the narrowing silently", func() {
				So(json.Unmarshal([]byte(`{`), &restored), ShouldNotBeNil)

				err := json.Unmarshal([]byte(`{"allowed_values":[{"type":"integer","value":"x"}]}`), &restored)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "allowed value")
			})
		})
	})
}

func TestEffectiveSchemaChecks(t *testing.T) {
	Convey("Given an effective schema derived from dependencies", t, func() {
		Convey("When nothing narrows the attribute", func() {
			schema := EffectiveSchema{}

			Convey("Then any value passes and absence is allowed", func() {
				So(schema.Check(valueobjects.NewStringValue("anything")), ShouldBeNil)
				So(schema.Check(valueobjects.Value{}), ShouldBeNil)
			})
		})

		Convey("When a dependency-derived constraint fails", func() {
			schema := EffectiveSchema{
				Constraints: attribute.Constraints{attribute.MaxLength{N: 3}},
			}
			err := schema.Check(valueobjects.NewStringValue("toolong"))

			Convey("Then the failure is reported as a dependency violation, not a plain validation error", func() {
				So(domainerrors.IsDependencyViolation(err), ShouldBeTrue)
			})
		})

		Convey("When several matched dependencies accumulate constraints", func() {
			now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
			typeDef := valueobjects.NewTypeDefinitionID()
			flag := newAttr(typeDef, "flag", valueobjects.DataTypeEnum, enumOf("on", "off"))
			note := newAttr(typeDef, "note", valueobjects.DataTypeString, nil)
			on := valueobjects.NewEnumValue("on")

			first, _, err := New(NewInput{Source: flag, Target: note,
				Conditions: []Condition{{Kind: CondEquals, Value: &on}},
				Effect:     Effect{Constraints: attribute.Constraints{attribute.MinLength{N: 3}}}}, now)
			So(err, ShouldBeNil)
			second, _, err := New(NewInput{Source: flag, Target: note,
				Conditions: []Condition{{Kind: CondEquals, Value: &on}},
				Effect:     Effect{Constraints: attribute.Constraints{attribute.MaxLength{N: 5}}}}, now)
			So(err, ShouldBeNil)

			schema, err := ResolveEffective(note, []*Dependency{first, second},
				map[valueobjects.AttributeDefinitionID]valueobjects.Value{flag.ID(): on}, now)
			So(err, ShouldBeNil)

			Convey("Then both bounds apply at once", func() {
				So(schema.Constraints, ShouldHaveLength, 2)
				So(schema.Check(valueobjects.NewStringValue("abcd")), ShouldBeNil)
				So(domainerrors.IsDependencyViolation(schema.Check(valueobjects.NewStringValue("ab"))), ShouldBeTrue)
				So(domainerrors.IsDependencyViolation(schema.Check(valueobjects.NewStringValue("abcdef"))), ShouldBeTrue)
			})
		})
	})
}
