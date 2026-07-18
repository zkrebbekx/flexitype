package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

type attrFixture struct {
	ctx    context.Context
	it     *application.Interactors
	typeID string
}

func newAttrFixture() *attrFixture {
	ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
	it := flexitype.NewInMemory().Interactors(ctx)

	product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
		InternalName: "product", DisplayName: "Product",
	})
	So(err, ShouldBeNil)
	return &attrFixture{ctx: ctx, it: it, typeID: product.ID.String()}
}

func (f *attrFixture) create(in appattribute.CreateInput) (string, error) {
	if in.TypeDefinitionID == "" {
		in.TypeDefinitionID = f.typeID
	}
	if in.DisplayName == "" {
		in.DisplayName = in.InternalName
	}
	snap, err := f.it.Attributes().Create(f.ctx, in)
	if err != nil {
		return "", err
	}
	return snap.ID.String(), nil
}

func TestAttributeCreateValidation(t *testing.T) {
	Convey("Given a product type", t, func() {
		f := newAttrFixture()

		Convey("When the type ID or data type is malformed", func() {
			_, typeErr := f.create(appattribute.CreateInput{
				TypeDefinitionID: "nope", InternalName: "sku", DataType: "string",
			})
			_, dtErr := f.create(appattribute.CreateInput{InternalName: "sku", DataType: "colour"})

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(dtErr), ShouldBeTrue)
				So(dtErr.Error(), ShouldContainSubstring, "unknown data type")
			})
		})

		Convey("When the type does not exist", func() {
			_, err := f.create(appattribute.CreateInput{
				TypeDefinitionID: valueobjects.NewTypeDefinitionID().String(),
				InternalName:     "sku", DataType: "string",
			})

			Convey("Then it is a not-found", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the constraints or default JSON is malformed", func() {
			_, cErr := f.create(appattribute.CreateInput{
				InternalName: "sku", DataType: "string", Constraints: json.RawMessage(`[{`),
			})
			_, dErr := f.create(appattribute.CreateInput{
				InternalName: "sku2", DataType: "string", DefaultValue: json.RawMessage(`{"static":`),
			})

			Convey("Then each names the offending section", func() {
				So(domainerrors.IsValidation(cErr), ShouldBeTrue)
				So(cErr.Error(), ShouldContainSubstring, "invalid constraints")
				So(domainerrors.IsValidation(dErr), ShouldBeTrue)
			})
		})

		Convey("When a constraint does not suit the data type", func() {
			_, err := f.create(appattribute.CreateInput{
				InternalName: "count", DataType: "integer",
				// max_length is a textual rule.
				Constraints: json.RawMessage(`[{"kind":"max_length","n":5}]`),
			})

			Convey("Then it is rejected as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the internal name is already used on the same type", func() {
			_, err := f.create(appattribute.CreateInput{InternalName: "sku", DataType: "string"})
			So(err, ShouldBeNil)
			_, err = f.create(appattribute.CreateInput{InternalName: "sku", DataType: "integer"})

			Convey("Then it conflicts", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(domainerrors.DetailsOf(err)["internal_name"], ShouldEqual, "sku")
			})
		})

		Convey("When a child type declares a name its ancestor already declares", func() {
			_, err := f.create(appattribute.CreateInput{InternalName: "sku", DataType: "string"})
			So(err, ShouldBeNil)
			child, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "bike", DisplayName: "Bike", ExtendsID: f.typeID,
			})
			So(err, ShouldBeNil)

			_, err = f.create(appattribute.CreateInput{
				TypeDefinitionID: child.ID.String(), InternalName: "sku", DataType: "string",
			})

			Convey("Then the shadowing attempt conflicts and names where it is declared", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "shadow")
				So(domainerrors.DetailsOf(err)["declared_in"], ShouldEqual, "product")
			})
		})

		Convey("When an ancestor declares a name its descendant already declares", func() {
			child, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
				InternalName: "bike", DisplayName: "Bike", ExtendsID: f.typeID,
			})
			So(err, ShouldBeNil)
			_, err = f.create(appattribute.CreateInput{
				TypeDefinitionID: child.ID.String(), InternalName: "frame", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = f.create(appattribute.CreateInput{InternalName: "frame", DataType: "string"})

			Convey("Then the shadowing attempt conflicts from the other direction too", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "shadow")
			})
		})

		Convey("When the parent type is archived", func() {
			_, err := f.it.TypeDefinitions().Archive(f.ctx, f.typeID)
			So(err, ShouldBeNil)

			_, err = f.create(appattribute.CreateInput{InternalName: "sku", DataType: "string"})

			Convey("Then attribute creation is refused", func() {
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})

		Convey("When another tenant creates against this type", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := f.it.Attributes().Create(other, appattribute.CreateInput{
				TypeDefinitionID: f.typeID, InternalName: "sku",
				DisplayName: "SKU", DataType: "string",
			})

			Convey("Then it is refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a quantity attribute carries value constraints without a unit family", func() {
			_, err := f.create(appattribute.CreateInput{
				InternalName: "weight", DataType: "quantity",
				Constraints: json.RawMessage(
					`[{"kind":"min_value","min":{"type":"quantity","value":{"magnitude":"1","unit":"kg","base":1}}}]`),
			})

			Convey("Then it is refused — the bound cannot be folded to a base unit", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "unit family")
			})
		})

		Convey("When a quantity attribute carries no value constraints", func() {
			_, err := f.create(appattribute.CreateInput{InternalName: "weight", DataType: "quantity"})

			Convey("Then no unit family is required", func() {
				So(err, ShouldBeNil)
			})
		})
	})
}

func TestAttributeUpdateAndLifecycle(t *testing.T) {
	Convey("Given a string attribute on a product type", t, func() {
		f := newAttrFixture()
		id, err := f.create(appattribute.CreateInput{
			InternalName: "sku", DataType: "string", Description: "stock keeping unit",
		})
		So(err, ShouldBeNil)

		Convey("When it is updated", func() {
			snap, err := f.it.Attributes().Update(f.ctx, appattribute.UpdateInput{
				ID: id, DisplayName: "Stock Code", Description: "renamed",
				Required: true, Group: "identity", SortOrder: 3, HelpText: "unique per product",
			})

			Convey("Then the mutable fields change and the version bumps", func() {
				So(err, ShouldBeNil)
				So(snap.DisplayName, ShouldEqual, "Stock Code")
				So(snap.Required, ShouldBeTrue)
				So(snap.Group, ShouldEqual, "identity")
				So(snap.SortOrder, ShouldEqual, 3)
				So(snap.Version, ShouldEqual, 2)

				got, err := f.it.Attributes().Get(f.ctx, id)
				So(err, ShouldBeNil)
				So(got.HelpText, ShouldEqual, "unique per product")
				// The machine name and data type are immutable.
				So(got.InternalName, ShouldEqual, "sku")
				So(string(got.DataType), ShouldEqual, "string")
			})
		})

		Convey("When an update names a malformed or unknown attribute", func() {
			_, badErr := f.it.Attributes().Update(f.ctx,
				appattribute.UpdateInput{ID: "nope", DisplayName: "X"})
			_, missingErr := f.it.Attributes().Update(f.ctx, appattribute.UpdateInput{
				ID: valueobjects.NewAttributeDefinitionID().String(), DisplayName: "X",
			})

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When an update carries constraints the data type rejects", func() {
			_, err := f.it.Attributes().Update(f.ctx, appattribute.UpdateInput{
				ID: id, DisplayName: "SKU",
				Constraints: json.RawMessage(`[{"kind":"min_value","min":{"type":"integer","value":1}}]`),
			})

			Convey("Then the update is refused and the stored definition is unchanged", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				got, err := f.it.Attributes().Get(f.ctx, id)
				So(err, ShouldBeNil)
				So(got.Version, ShouldEqual, 1)
			})
		})

		Convey("When it is archived", func() {
			snap, err := f.it.Attributes().Archive(f.ctx, id)

			Convey("Then it is marked archived", func() {
				So(err, ShouldBeNil)
				So(snap.ArchivedAt, ShouldNotBeNil)
			})

			Convey("And archiving twice is refused", func() {
				_, err := f.it.Attributes().Archive(f.ctx, id)
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And updating an archived attribute is refused", func() {
				_, err := f.it.Attributes().Update(f.ctx,
					appattribute.UpdateInput{ID: id, DisplayName: "X"})
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})

			Convey("And restoring brings it back live", func() {
				restored, err := f.it.Attributes().Restore(f.ctx, id)
				So(err, ShouldBeNil)
				So(restored.ArchivedAt, ShouldBeNil)

				_, err = f.it.Attributes().Update(f.ctx,
					appattribute.UpdateInput{ID: id, DisplayName: "Back"})
				So(err, ShouldBeNil)
			})
		})

		Convey("When a live attribute is restored", func() {
			_, err := f.it.Attributes().Restore(f.ctx, id)

			Convey("Then it is refused — there is nothing to restore", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When archive or restore names a malformed or unknown attribute", func() {
			_, badErr := f.it.Attributes().Archive(f.ctx, "nope")
			_, missingErr := f.it.Attributes().Restore(f.ctx,
				valueobjects.NewAttributeDefinitionID().String())

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant mutates it", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, getErr := f.it.Attributes().Get(other, id)
			_, archiveErr := f.it.Attributes().Archive(other, id)

			Convey("Then both are refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(getErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(archiveErr), ShouldBeTrue)
			})
		})
	})
}

func TestAttributeValidateValue(t *testing.T) {
	Convey("Given a constrained attribute", t, func() {
		f := newAttrFixture()
		id, err := f.create(appattribute.CreateInput{
			InternalName: "sku", DataType: "string",
			Constraints: json.RawMessage(`[{"kind":"max_length","n":5}]`),
		})
		So(err, ShouldBeNil)

		Convey("When a conforming value is dry-run", func() {
			err := f.it.Attributes().ValidateValue(f.ctx, id, json.RawMessage(`"ABC"`))

			Convey("Then it passes without persisting anything", func() {
				So(err, ShouldBeNil)
				out, err := f.it.Values().ListByEntity(f.ctx, f.typeID, "any")
				So(err, ShouldBeNil)
				So(out, ShouldBeEmpty)
			})
		})

		Convey("When a value of the wrong JSON type is dry-run", func() {
			err := f.it.Attributes().ValidateValue(f.ctx, id, json.RawMessage(`42`))

			Convey("Then the parse failure is reported as validation", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a value violating a constraint is dry-run", func() {
			err := f.it.Attributes().ValidateValue(f.ctx, id, json.RawMessage(`"TOO-LONG"`))

			Convey("Then the constraint failure is reported", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the attribute ID is malformed or unknown", func() {
			badErr := f.it.Attributes().ValidateValue(f.ctx, "nope", json.RawMessage(`"x"`))
			missingErr := f.it.Attributes().ValidateValue(f.ctx,
				valueobjects.NewAttributeDefinitionID().String(), json.RawMessage(`"x"`))

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant dry-runs a value", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			err := f.it.Attributes().ValidateValue(other, id, json.RawMessage(`"ABC"`))

			Convey("Then it is refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})
	})
}

func TestAttributeListing(t *testing.T) {
	Convey("Given several attributes across a type hierarchy", t, func() {
		f := newAttrFixture()

		sku, err := f.create(appattribute.CreateInput{InternalName: "sku", DataType: "string"})
		So(err, ShouldBeNil)
		_, err = f.create(appattribute.CreateInput{InternalName: "price", DataType: "decimal"})
		So(err, ShouldBeNil)
		_, err = f.create(appattribute.CreateInput{InternalName: "stock", DataType: "integer"})
		So(err, ShouldBeNil)

		child, err := f.it.TypeDefinitions().Create(f.ctx, apptypedef.CreateInput{
			InternalName: "bike", DisplayName: "Bike", ExtendsID: f.typeID,
		})
		So(err, ShouldBeNil)
		_, err = f.create(appattribute.CreateInput{
			TypeDefinitionID: child.ID.String(), InternalName: "frame", DataType: "string",
		})
		So(err, ShouldBeNil)

		Convey("When attributes are listed for a subtype", func() {
			items, err := f.it.Attributes().ListByTypeDefinition(f.ctx, child.ID.String(), db.PageArgs{})

			Convey("Then only the attributes the subtype itself declares come back", func() {
				// This usecase lists one type's own declarations; resolving the
				// inherited set is the type-definition side's job.
				So(err, ShouldBeNil)
				So(items.Items, ShouldHaveLength, 1)
				So(items.Items[0].InternalName, ShouldEqual, "frame")
			})
		})

		Convey("When attributes are listed for a malformed or unknown type", func() {
			_, badErr := f.it.Attributes().ListByTypeDefinition(f.ctx, "nope", db.PageArgs{})
			_, missingErr := f.it.Attributes().ListByTypeDefinition(f.ctx,
				valueobjects.NewTypeDefinitionID().String(), db.PageArgs{})

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When the flat list is filtered by type, name and data type", func() {
			byType, err := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{TypeDefinitionID: f.typeID})
			So(err, ShouldBeNil)
			byName, err := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{InternalNames: []string{"sku", "price"}})
			So(err, ShouldBeNil)
			byDataType, err := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{DataTypes: []string{"integer"}})
			So(err, ShouldBeNil)

			Convey("Then each filter selects exactly what it declares", func() {
				So(byType.Items, ShouldHaveLength, 3)
				So(byName.Items, ShouldHaveLength, 2)
				So(byDataType.Items, ShouldHaveLength, 1)
				So(byDataType.Items[0].InternalName, ShouldEqual, "stock")
			})
		})

		Convey("When a list filter is malformed", func() {
			_, typeErr := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{TypeDefinitionID: "nope"})
			_, dtErr := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{DataTypes: []string{"colour"}})
			zero := 0
			_, pageErr := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{Page: db.PageArgs{Limit: &zero}})

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(dtErr), ShouldBeTrue)
				So(domainerrors.IsValidation(pageErr), ShouldBeTrue)
			})
		})

		Convey("When an archived attribute is listed", func() {
			_, err := f.it.Attributes().Archive(f.ctx, sku)
			So(err, ShouldBeNil)

			live, err := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{TypeDefinitionID: f.typeID})
			So(err, ShouldBeNil)
			all, err := f.it.Attributes().List(f.ctx,
				appattribute.ListInput{TypeDefinitionID: f.typeID, IncludeArchived: true})
			So(err, ShouldBeNil)

			Convey("Then it is hidden by default and visible when asked for", func() {
				So(live.Items, ShouldHaveLength, 2)
				So(all.Items, ShouldHaveLength, 3)
			})
		})

		Convey("When a page smaller than the result set is requested", func() {
			limit := 2
			first, err := f.it.Attributes().List(f.ctx, appattribute.ListInput{
				TypeDefinitionID: f.typeID,
				Page:             db.PageArgs{Limit: &limit, WantTotal: true},
			})
			So(err, ShouldBeNil)

			Convey("Then it carries a cursor and a total, and the cursor resumes", func() {
				So(first.Items, ShouldHaveLength, 2)
				So(first.PageInfo.HasNextPage, ShouldBeTrue)
				So(*first.PageInfo.TotalCount, ShouldEqual, 3)

				second, err := f.it.Attributes().List(f.ctx, appattribute.ListInput{
					TypeDefinitionID: f.typeID,
					Page:             db.PageArgs{Limit: &limit, Cursor: first.PageInfo.NextCursor},
				})
				So(err, ShouldBeNil)
				So(second.Items, ShouldHaveLength, 1)
				So(second.PageInfo.HasPreviousPage, ShouldBeTrue)
			})
		})

		Convey("When another tenant lists attributes", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			out, err := f.it.Attributes().List(other, appattribute.ListInput{})

			Convey("Then nothing crosses the tenant boundary", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)
			})
		})
	})
}
