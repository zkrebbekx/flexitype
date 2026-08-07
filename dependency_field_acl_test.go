package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// These are the regressions for the dependency surface's missing field ACL
// (#470): authoring rules over invisible attributes, the effective-schema
// value oracle, and the completeness name/denominator leak.

// depACLFixture builds one type with a restricted salary, a read-only level,
// and unrestricted notes and po_number, and returns the two contexts.
type depACLFixture struct {
	svc               *flexitype.Service
	admin, restricted context.Context
	typeID            string
	attrID            map[string]string
}

func newDepACLFixture() *depACLFixture {
	f := &depACLFixture{attrID: map[string]string{}}
	f.admin = uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	f.restricted = uow.WithAccess(f.admin, uow.Access{Attr: map[string]uow.Perm{
		"salary": uow.PermNone,
		"level":  uow.PermRead,
	}})
	f.svc = flexitype.NewInMemory()
	ia := f.svc.Interactors(f.admin)

	person, err := ia.TypeDefinitions().Create(f.admin, apptypedef.CreateInput{
		InternalName: "person", DisplayName: "Person",
	})
	So(err, ShouldBeNil)
	f.typeID = person.ID.String()
	mk := func(name, dataType string, required bool) {
		a, aerr := ia.Attributes().Create(f.admin, appattribute.CreateInput{
			TypeDefinitionID: f.typeID, InternalName: name,
			DisplayName: name, DataType: dataType, Required: required,
		})
		So(aerr, ShouldBeNil)
		f.attrID[name] = a.ID.String()
	}
	mk("salary", "float", true)
	mk("notes", "string", true)
	mk("level", "string", false)
	mk("po_number", "string", false)
	return f
}

func (f *depACLFixture) createDep(ctx context.Context, source, target, conditions, effect string) error {
	_, err := f.svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
		SourceAttributeID: f.attrID[source],
		TargetAttributeID: f.attrID[target],
		Conditions:        json.RawMessage(conditions),
		Effect:            json.RawMessage(effect),
	})
	return err
}

func (f *depACLFixture) set(ctx context.Context, name, raw string) {
	_, err := f.svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
		AttributeDefinitionID: f.attrID[name], EntityID: "e1",
		TypeDefinitionID: f.typeID, Value: json.RawMessage(raw),
	})
	So(err, ShouldBeNil)
}

const salaryOver100k = `[{"kind":"range","min":{"type":"float","value":100000}}]`

func TestDependencyAuthoringRequiresFieldAccess(t *testing.T) {
	Convey("Given a restricted salary, a read-only level and writable notes", t, func() {
		f := newDepACLFixture()

		Convey("When the restricted principal authors a rule keyed on the restricted source", func() {
			err := f.createDep(f.restricted, "salary", "notes", salaryOver100k, `{"required":true}`)

			Convey("Then the source is reported as not found, exactly like an unknown ID", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				_, uerr := f.svc.Interactors(f.restricted).Dependencies().Create(f.restricted, appdependency.CreateInput{
					SourceAttributeID: ulid.New().String(),
					TargetAttributeID: f.attrID["notes"],
					Conditions:        json.RawMessage(salaryOver100k),
					Effect:            json.RawMessage(`{"required":true}`),
				})
				So(domainerrors.IsNotFound(uerr), ShouldBeTrue)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeOf(uerr))
			})
		})

		Convey("When the restricted principal targets an attribute it cannot read", func() {
			err := f.createDep(f.restricted, "notes", "salary",
				`[{"kind":"equals","value":{"type":"string","value":"x"}}]`, `{"required":true}`)

			Convey("Then the target is reported as not found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the restricted principal targets an attribute it can read but not write", func() {
			err := f.createDep(f.restricted, "notes", "level",
				`[{"kind":"equals","value":{"type":"string","value":"x"}}]`, `{"required":true}`)

			Convey("Then the refusal is a plain permission error", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)
			})
		})

		Convey("When an admin authors a rule over the restricted source", func() {
			err := f.createDep(f.admin, "salary", "notes", salaryOver100k, `{"required":true}`)

			Convey("Then it is accepted", func() {
				So(err, ShouldBeNil)
			})

			Convey("And the restricted principal cannot update it", func() {
				So(err, ShouldBeNil)
				deps, lerr := f.svc.Interactors(f.admin).Dependencies().List(f.admin, appdependency.ListInput{})
				So(lerr, ShouldBeNil)
				So(deps.Items, ShouldHaveLength, 1)
				_, uerr := f.svc.Interactors(f.restricted).Dependencies().Update(f.restricted, appdependency.UpdateInput{
					ID:         deps.Items[0].ID.String(),
					Conditions: json.RawMessage(salaryOver100k),
					Effect:     json.RawMessage(`{"required":true}`),
				})
				So(domainerrors.IsNotFound(uerr), ShouldBeTrue)
			})
		})

		Convey("When an admin authors a rule targeting the read-only level", func() {
			So(f.createDep(f.admin, "notes", "level",
				`[{"kind":"equals","value":{"type":"string","value":"x"}}]`, `{"required":true}`), ShouldBeNil)
			deps, lerr := f.svc.Interactors(f.admin).Dependencies().List(f.admin, appdependency.ListInput{})
			So(lerr, ShouldBeNil)
			So(deps.Items, ShouldHaveLength, 1)

			Convey("Then the restricted principal cannot archive it", func() {
				_, aerr := f.svc.Interactors(f.restricted).Dependencies().Archive(f.restricted, deps.Items[0].ID.String())
				So(domainerrors.CodeOf(aerr), ShouldEqual, domainerrors.CodeForbidden)
			})

			Convey("And an admin still can", func() {
				_, aerr := f.svc.Interactors(f.admin).Dependencies().Archive(f.admin, deps.Items[0].ID.String())
				So(aerr, ShouldBeNil)
			})
		})
	})
}

func TestEffectiveSchemaIsNotAValueOracle(t *testing.T) {
	Convey("Given an admin-authored rule: notes restricted when salary > 100000", t, func() {
		f := newDepACLFixture()
		So(f.createDep(f.admin, "salary", "notes", salaryOver100k,
			`{"allowed_values":[{"type":"string","value":"cleared"}]}`), ShouldBeNil)
		f.set(f.admin, "salary", `150000`)
		f.set(f.admin, "notes", `"cleared"`)

		Convey("When an admin resolves the effective schema of notes", func() {
			eff, err := f.svc.Interactors(f.admin).Dependencies().EffectiveSchema(f.admin, f.attrID["notes"], "e1")

			Convey("Then the rule fires", func() {
				So(err, ShouldBeNil)
				So(eff.Restricted, ShouldBeTrue)
			})
		})

		Convey("When the restricted principal resolves the effective schema of notes", func() {
			eff, err := f.svc.Interactors(f.restricted).Dependencies().EffectiveSchema(f.restricted, f.attrID["notes"], "e1")

			Convey("Then the rule keyed on the invisible salary does not fire: no bit leaks", func() {
				So(err, ShouldBeNil)
				So(eff.Restricted, ShouldBeFalse)
			})
		})

		Convey("When the restricted principal asks for the effective schema OF the restricted attribute", func() {
			_, err := f.svc.Interactors(f.restricted).Dependencies().EffectiveSchema(f.restricted, f.attrID["salary"], "e1")

			Convey("Then it is not found, exactly like an unknown attribute", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				_, uerr := f.svc.Interactors(f.restricted).Dependencies().EffectiveSchema(f.restricted, ulid.New().String(), "e1")
				So(domainerrors.IsNotFound(uerr), ShouldBeTrue)
			})
		})
	})
}

func TestCompletenessRespectsFieldACL(t *testing.T) {
	Convey("Given required salary and notes, and a rule requiring po_number on high salary", t, func() {
		f := newDepACLFixture()
		So(f.createDep(f.admin, "salary", "po_number", salaryOver100k, `{"required":true}`), ShouldBeNil)
		f.set(f.admin, "salary", `150000`)
		f.set(f.admin, "notes", `"filled"`)

		Convey("When an admin scores the entity", func() {
			out, err := f.svc.Interactors(f.admin).Dependencies().Completeness(f.admin, f.typeID, "e1")

			Convey("Then salary counts and the fired rule lists po_number as missing", func() {
				So(err, ShouldBeNil)
				So(out.Required, ShouldEqual, 3) // salary, notes, po_number (rule-required)
				So(out.Filled, ShouldEqual, 2)
				names := make([]string, 0, len(out.Missing))
				for _, m := range out.Missing {
					names = append(names, m.InternalName)
				}
				So(names, ShouldResemble, []string{"po_number"})
			})
		})

		Convey("When the restricted principal scores the entity", func() {
			out, err := f.svc.Interactors(f.restricted).Dependencies().Completeness(f.restricted, f.typeID, "e1")

			Convey("Then salary is out of the denominator and no restricted name appears", func() {
				So(err, ShouldBeNil)
				So(out.Required, ShouldEqual, 1) // notes only; the salary-keyed rule does not fire
				So(out.Filled, ShouldEqual, 1)
				So(out.Missing, ShouldBeEmpty)
				raw, _ := json.Marshal(out)
				So(string(raw), ShouldNotContainSubstring, "salary")
			})
		})
	})
}
