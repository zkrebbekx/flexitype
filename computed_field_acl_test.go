package flexitype_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestComputedFormulaCannotReadRestrictedSource is the regression for the
// field-ACL read bypass (#469).
//
// A computed attribute is materialized under system access and is born
// unrestricted, so a formula over a restricted source republishes that
// source's exact values to the formula's author. The guard refuses the write.
// The refusal must be indistinguishable from an unresolved reference, or the
// guard itself becomes an existence oracle over restricted attribute names.
func TestComputedFormulaCannotReadRestrictedSource(t *testing.T) {
	Convey("Given a type with a restricted salary and a readable public attribute", t, func() {
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		restricted := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
			"salary": uow.PermNone,
			"tags":   uow.PermNone,
		}})
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(admin)

		person, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "person", DisplayName: "Person",
		})
		So(err, ShouldBeNil)
		mkAttr := func(name, dataType string, multi bool) {
			_, aerr := ia.Attributes().Create(admin, appattribute.CreateInput{
				TypeDefinitionID: person.ID.String(), InternalName: name,
				DisplayName: name, DataType: dataType, MultiValued: multi,
			})
			So(aerr, ShouldBeNil)
		}
		mkAttr("salary", "float", false)
		mkAttr("public", "float", false)
		mkAttr("tags", "string", true)

		formula := func(ctx context.Context, name, expr string) error {
			_, ferr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: person.ID.String(), InternalName: name,
				DisplayName: name, DataType: "float",
				Computed: json.RawMessage(`{"kind":"formula","formula":` + jsonQuote(expr) + `}`),
			})
			return ferr
		}

		Convey("When a field-restricted principal computes over the restricted source", func() {
			err := formula(restricted, "leak", "salary * 1")

			Convey("Then the write is refused as validation, worded like an unknown reference", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unknown attribute")
				So(err.Error(), ShouldNotContainSubstring, "read")
				So(err.Error(), ShouldNotContainSubstring, "restricted")
			})

			Convey("And the refusal matches an unresolved reference exactly, name aside", func() {
				unknownErr := formula(restricted, "leak2", "no_such_attribute * 1")
				So(domainerrors.CodeOf(unknownErr), ShouldEqual, domainerrors.CodeValidation)
				normalize := func(e error, name string) string {
					return strings.ReplaceAll(e.Error(), name, "X")
				}
				So(normalize(err, "salary"), ShouldEqual, normalize(unknownErr, "no_such_attribute"))
			})
		})

		Convey("When the restricted source is folded in an aggregate", func() {
			err := formula(restricted, "leak_count", "count(salary)")

			Convey("Then the aggregate is refused the same way", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unknown attribute")
			})
		})

		Convey("When the restricted source is multi-valued and referenced bare", func() {
			err := formula(restricted, "leak_multi", "tags * 1")

			Convey("Then the refusal does not disclose the shape of the restricted attribute", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unknown attribute")
				So(err.Error(), ShouldNotContainSubstring, "multi-valued")
			})
		})

		Convey("When a field-restricted principal updates a formula toward the restricted source", func() {
			derived, err := svc.Interactors(restricted).Attributes().Create(restricted, appattribute.CreateInput{
				TypeDefinitionID: person.ID.String(), InternalName: "derived",
				DisplayName: "derived", DataType: "float",
				Computed: json.RawMessage(`{"kind":"formula","formula":"public * 2"}`),
			})
			So(err, ShouldBeNil)
			_, uerr := svc.Interactors(restricted).Attributes().Update(restricted, appattribute.UpdateInput{
				ID: derived.ID.String(), DisplayName: "derived",
				Computed: json.RawMessage(`{"kind":"formula","formula":"salary * 1"}`),
			})

			Convey("Then the update is refused as an unknown reference", func() {
				So(domainerrors.CodeOf(uerr), ShouldEqual, domainerrors.CodeValidation)
				So(uerr.Error(), ShouldContainSubstring, "unknown attribute")
			})
		})

		Convey("When the same principal computes over a readable source", func() {
			err := formula(restricted, "doubled", "public * 2")

			Convey("Then the formula is accepted", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When an admin computes over the restricted source", func() {
			err := formula(admin, "band", "salary * 0")

			Convey("Then the formula is accepted: publishing derived data is the schema owner's call", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

// jsonQuote JSON-quotes a formula for embedding in a computed spec literal.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestComputedMaterializationIgnoresWriterACL is the regression for derived
// values computed under the writing principal's field ACL (#471).
//
// The materializer runs in the writing request's post-commit. Reading the
// entity's inputs through the writer's ACL redacted the values the writer may
// not see, and the formula then durably wrote a wrong result — or deleted a
// right one — for every reader. Materialization must read under system
// access whoever triggered it.
func TestComputedMaterializationIgnoresWriterACL(t *testing.T) {
	Convey("Given margin = price - cost with cost restricted for the writer", t, func() {
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		writer := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{
			"cost": uow.PermNone,
			"tags": uow.PermNone,
		}})
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(admin)

		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		attrID := map[string]string{}
		mk := func(name, dataType, formula string, multi bool) {
			in := appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: name,
				DisplayName: name, DataType: dataType, MultiValued: multi,
			}
			if formula != "" {
				in.Computed = json.RawMessage(`{"kind":"formula","formula":` + jsonQuote(formula) + `}`)
			}
			a, aerr := ia.Attributes().Create(admin, in)
			So(aerr, ShouldBeNil)
			attrID[name] = a.ID.String()
		}
		mk("price", "float", "", false)
		mk("cost", "float", "", false)
		mk("tags", "string", "", true)
		mk("margin", "float", "price - cost", false)
		mk("score", "integer", "count(tags) * 10", false)

		set := func(ctx context.Context, name string, raw string) error {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID[name], EntityID: "e1",
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(raw),
			})
			return serr
		}
		readAs := func(ctx context.Context, name string) (string, bool) {
			vals, verr := svc.Interactors(ctx).Values().ListByEntity(ctx, product.ID.String(), "e1")
			So(verr, ShouldBeNil)
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == attrID[name] {
					return v.Value.String(), true
				}
			}
			return "", false
		}

		So(set(admin, "price", `100`), ShouldBeNil)
		So(set(admin, "cost", `40`), ShouldBeNil)
		So(set(admin, "tags", `"a"`), ShouldBeNil)
		So(set(admin, "tags", `"b"`), ShouldBeNil)
		margin, _ := readAs(admin, "margin")
		So(margin, ShouldEqual, "60")
		score, _ := readAs(admin, "score")
		So(score, ShouldEqual, "20")

		Convey("When the restricted writer updates an input it may write", func() {
			So(set(writer, "price", `130`), ShouldBeNil)

			Convey("Then the derived values are recomputed from the FULL input set", func() {
				margin, ok := readAs(admin, "margin")
				So(ok, ShouldBeTrue)
				So(margin, ShouldEqual, "90")
				score, ok := readAs(admin, "score")
				So(ok, ShouldBeTrue)
				So(score, ShouldEqual, "20")
			})

			Convey("And the writer still cannot read the restricted input itself", func() {
				_, visible := readAs(writer, "cost")
				So(visible, ShouldBeFalse)
				_, tagsVisible := readAs(writer, "tags")
				So(tagsVisible, ShouldBeFalse)
			})
		})
	})
}
