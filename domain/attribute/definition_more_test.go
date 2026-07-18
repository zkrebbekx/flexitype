package attribute

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestDefinitionConstructionRules(t *testing.T) {
	Convey("Given an attribute definition being created", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		valid := NewInput{
			TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
			InternalName:     "weight_kg",
			DisplayName:      "Weight",
			DataType:         valueobjects.DataTypeFloat,
		}

		Convey("When the owning type definition is missing", func() {
			in := valid
			in.TypeDefinitionID = valueobjects.TypeDefinitionID{}
			_, _, err := New(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "type definition ID")
			})
		})

		Convey("When the display name is empty", func() {
			in := valid
			in.DisplayName = ""
			_, _, err := New(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "display name")
			})
		})

		Convey("When the data type is not a known soft type", func() {
			in := valid
			in.DataType = valueobjects.DataType("unobtainium")
			_, _, err := New(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When no tenant is supplied", func() {
			def, _, err := New(valid, now)

			Convey("Then the definition falls back to the default tenant", func() {
				So(err, ShouldBeNil)
				So(def.TenantID(), ShouldEqual, valueobjects.DefaultTenant)
			})
		})

		Convey("When the attribute is both multi-valued and unique", func() {
			in := valid
			in.MultiValued = true
			in.Unique = true
			_, _, err := New(in, now)

			Convey("Then the contradictory pair is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "multi-valued and unique")
			})
		})

		Convey("When the computed spec is invalid", func() {
			in := valid
			in.Computed = &Computed{Kind: ComputedFormula} // no formula
			_, _, err := New(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a default value's type does not match the attribute", func() {
			in := valid
			in.DataType = valueobjects.DataTypeInteger
			str := valueobjects.NewStringValue("nope")
			in.DefaultValue = &valueobjects.Default{Static: &str}
			_, _, err := New(in, now)

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a default value violates the attribute's own constraints", func() {
			in := valid
			in.DataType = valueobjects.DataTypeString
			short := valueobjects.NewStringValue("ab")
			in.DefaultValue = &valueobjects.Default{Static: &short}
			in.Constraints = Constraints{MinLength{N: 5}}
			_, _, err := New(in, now)

			Convey("Then creation is rejected, naming the constraint violation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "default value violates constraints")
			})
		})
	})
}

func TestDefinitionUpdateRules(t *testing.T) {
	Convey("Given an existing attribute definition", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		def, _, err := New(NewInput{
			TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
			InternalName:     "weight_kg",
			DisplayName:      "Weight",
			DataType:         valueobjects.DataTypeFloat,
		}, now)
		So(err, ShouldBeNil)

		Convey("When it is updated with an empty display name", func() {
			_, err := def.Update(UpdateInput{}, now.Add(time.Hour))

			Convey("Then the update is rejected and the version does not move", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(def.Version(), ShouldEqual, 1)
			})
		})

		Convey("When it is updated with an invalid computed spec", func() {
			_, err := def.Update(UpdateInput{
				DisplayName: "Weight",
				Computed:    &Computed{Kind: ComputedRollup}, // no rollup spec
			}, now.Add(time.Hour))

			Convey("Then the update is rejected and the version does not move", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(def.Version(), ShouldEqual, 1)
			})
		})

		Convey("When it is updated with contradictory cardinality flags", func() {
			_, err := def.Update(UpdateInput{
				DisplayName: "Weight", MultiValued: true, Unique: true,
			}, now.Add(time.Hour))

			Convey("Then the update is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When it is updated with a valid computed spec and presentation metadata", func() {
			evts, err := def.Update(UpdateInput{
				DisplayName: "Derived Weight",
				Description: "kilograms",
				Computed:    &Computed{Kind: ComputedFormula, Formula: "net + tare"},
				DisplayUnit: "kg",
				Group:       "physical",
				SortOrder:   3,
				HelpText:    "auto-derived",
			}, now.Add(time.Hour))

			Convey("Then the state, version and event all reflect the change", func() {
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(def.Version(), ShouldEqual, 2)
				So(def.IsComputed(), ShouldBeTrue)
				So(def.Computed().Formula, ShouldEqual, "net + tare")
				So(def.Description(), ShouldEqual, "kilograms")
				So(def.DisplayUnit(), ShouldEqual, "kg")
				So(def.UpdatedAt(), ShouldEqual, now.Add(time.Hour))
				So(def.CreatedAt(), ShouldEqual, now)
			})
		})
	})
}

func TestDefinitionArchiveRestore(t *testing.T) {
	Convey("Given an attribute definition", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		def := newTestDefinition(valueobjects.DataTypeString, nil, false)

		Convey("When it is restored while still live", func() {
			_, err := def.Restore(now)

			Convey("Then the restore is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(def.IsArchived(), ShouldBeFalse)
				So(def.ArchivedAt(), ShouldBeNil)
			})
		})

		Convey("When it is archived", func() {
			_, err := def.Archive(now)
			So(err, ShouldBeNil)

			Convey("Then it reports the archive instant", func() {
				So(def.IsArchived(), ShouldBeTrue)
				So(*def.ArchivedAt(), ShouldEqual, now)
			})

			Convey("Then archiving again is rejected", func() {
				_, err := def.Archive(now.Add(time.Hour))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("Then value validation is refused while archived", func() {
				err := def.ValidateValue(valueobjects.NewStringValue("x"))
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("Then restoring clears the archive and re-admits values", func() {
				evts, err := def.Restore(now.Add(time.Hour))
				So(err, ShouldBeNil)
				So(evts, ShouldHaveLength, 1)
				So(def.IsArchived(), ShouldBeFalse)
				So(def.ArchivedAt(), ShouldBeNil)
				So(def.ValidateValue(valueobjects.NewStringValue("x")), ShouldBeNil)
			})
		})
	})
}

func TestDefinitionValidateValueAndDefaults(t *testing.T) {
	Convey("Given an attribute definition validating candidate values", t, func() {
		now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)

		Convey("When an optional attribute receives no value", func() {
			def := newTestDefinition(valueobjects.DataTypeString, nil, false)

			Convey("Then the absent value is accepted", func() {
				So(def.ValidateValue(valueobjects.Value{}), ShouldBeNil)
			})
		})

		Convey("When a value of the wrong type is offered", func() {
			def := newTestDefinition(valueobjects.DataTypeInteger, nil, false)
			err := def.ValidateValue(valueobjects.NewStringValue("not a number"))

			Convey("Then it is rejected as a type mismatch", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "does not match")
			})
		})

		Convey("When the attribute declares no default", func() {
			def := newTestDefinition(valueobjects.DataTypeString, nil, false)
			v, err := def.DefaultFor(now)

			Convey("Then the resolved default is the zero value", func() {
				So(err, ShouldBeNil)
				So(v.IsZero(), ShouldBeTrue)
				So(def.DefaultValue(), ShouldBeNil)
			})
		})

		Convey("When the attribute declares a static default", func() {
			static := valueobjects.NewStringValue("pending")
			def, _, err := New(NewInput{
				TypeDefinitionID: valueobjects.NewTypeDefinitionID(),
				InternalName:     "status_note",
				DisplayName:      "Status Note",
				DataType:         valueobjects.DataTypeString,
				DefaultValue:     &valueobjects.Default{Static: &static},
			}, now)
			So(err, ShouldBeNil)

			Convey("Then it resolves to that value", func() {
				v, err := def.DefaultFor(now)
				So(err, ShouldBeNil)
				So(v.Text(), ShouldEqual, "pending")
				So(def.DefaultValue(), ShouldNotBeNil)
			})
		})
	})
}
