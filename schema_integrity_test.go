package flexitype_test

import (
	"context"
	"encoding/json"
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

// TestStructuralFlagFlips covers the schema changes that used to orphan data.
//
// Definition.Update replaced the structural flags with no check against what
// was already stored. Each flip left rows the new schema cannot express or
// reach, and none of them reported anything: the rows kept hydrating and kept
// matching FQL, so schema and data disagreed in silence. Version pinning does
// not cover this — it records the constraint version a value was validated
// against, and these flags are structural rather than per-value.
func TestStructuralFlagFlips(t *testing.T) {
	Convey("Given a product type", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)
		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)

		attr := func(in appattribute.CreateInput) string {
			in.TypeDefinitionID = product.ID.String()
			in.DisplayName = in.InternalName
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, in)
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		set := func(attrID, entity, v string, scope appvalue.SetInput) error {
			raw, _ := json.Marshal(v)
			scope.AttributeDefinitionID = attrID
			scope.EntityID = entity
			scope.TypeDefinitionID = product.ID.String()
			scope.Value = raw
			_, serr := svc.Interactors(ctx).Values().Set(ctx, scope)
			return serr
		}
		update := func(id string, in appattribute.UpdateInput) error {
			in.ID = id
			in.DisplayName = "x"
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, in)
			return uerr
		}

		Convey("When a multi-valued attribute already holds several values per entity", func() {
			tags := attr(appattribute.CreateInput{InternalName: "tags", DataType: "string", MultiValued: true})
			So(set(tags, "p1", "a", appvalue.SetInput{}), ShouldBeNil)
			So(set(tags, "p1", "b", appvalue.SetInput{}), ShouldBeNil)

			Convey("Then making it single-valued is refused, not silently stranding rows", func() {
				err := update(tags, appattribute.UpdateInput{MultiValued: false})
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "more than one value")
			})

			Convey("Then leaving it multi-valued is still allowed", func() {
				So(update(tags, appattribute.UpdateInput{MultiValued: true}), ShouldBeNil)
			})
		})

		Convey("When two entities already share a value", func() {
			sku := attr(appattribute.CreateInput{InternalName: "sku", DataType: "string"})
			So(set(sku, "p1", "ABC", appvalue.SetInput{}), ShouldBeNil)
			So(set(sku, "p2", "ABC", appvalue.SetInput{}), ShouldBeNil)

			Convey("Then making it unique is refused, rather than half-enforced", func() {
				err := update(sku, appattribute.UpdateInput{Unique: true})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "duplicates")
			})
		})

		Convey("When a localizable attribute holds scoped values", func() {
			name := attr(appattribute.CreateInput{InternalName: "name", DataType: "string", Localizable: true})
			So(set(name, "p1", "Widget", appvalue.SetInput{Locale: "en"}), ShouldBeNil)

			Convey("Then turning localizable off is refused", func() {
				err := update(name, appattribute.UpdateInput{Localizable: false})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "locale or channel")
			})
		})

		Convey("When an attribute already holds written values", func() {
			total := attr(appattribute.CreateInput{InternalName: "total", DataType: "integer"})
			raw, _ := json.Marshal(5)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: total, EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)

			Convey("Then making it computed is refused: a recompute would erase them", func() {
				err := update(total, appattribute.UpdateInput{
					Computed: json.RawMessage(`{"kind":"formula","formula":"1 + 1"}`),
				})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "already holds written values")
			})
		})
	})
}

// TestSiblingAttributeNameCollision covers the namespace the no-shadowing
// guard did not cover.
//
// The guard checked a type's ancestor chain and its own descendants, so two
// SIBLING subtypes could declare the same internal name. The FQL binder
// flattens the queried root's whole tree into one name→attribute map, so on a
// collision it bound to whichever was listed last — silently, and the winner
// could change with an unrelated edit.
func TestSiblingAttributeNameCollision(t *testing.T) {
	Convey("Given two sibling subtypes of one parent", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		parent, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		book, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "book", DisplayName: "Book", ExtendsID: parent.ID.String(),
		})
		So(err, ShouldBeNil)
		electronics, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "electronics", DisplayName: "Electronics", ExtendsID: parent.ID.String(),
		})
		So(err, ShouldBeNil)

		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: book.ID.String(), InternalName: "code",
			DisplayName: "Code", DataType: "string",
		})
		So(err, ShouldBeNil)

		Convey("When the sibling declares the same internal name", func() {
			_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: electronics.ID.String(), InternalName: "code",
				DisplayName: "Code", DataType: "string",
			})

			Convey("Then it is refused, rather than left for the binder to pick", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "already declared elsewhere")
			})
		})

		Convey("When an unrelated root type declares that name", func() {
			other, oerr := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
				InternalName: "supplier", DisplayName: "Supplier",
			})
			So(oerr, ShouldBeNil)

			Convey("Then it is allowed: a different tree is a different namespace", func() {
				_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: other.ID.String(), InternalName: "code",
					DisplayName: "Code", DataType: "string",
				})
				So(err, ShouldBeNil)
			})
		})
	})
}

// TestArchivedTypeRefusesWrites covers data written where it cannot be read.
//
// Archiving a type was a pure soft delete with no write-path guard, while the
// FQL binder excludes archived types from query scope. So a write under an
// archived type succeeded and the data was then unqueryable — invisible to
// the very surface an operator would use to find it.
func TestArchivedTypeRefusesWrites(t *testing.T) {
	Convey("Given an archived type", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		sku, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)

		write := func(entity string) error {
			raw, _ := json.Marshal("ABC")
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: sku.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			return serr
		}
		So(write("p1"), ShouldBeNil)

		_, err = svc.Interactors(ctx).TypeDefinitions().Archive(ctx, product.ID.String())
		So(err, ShouldBeNil)

		Convey("When a value is written under it", func() {
			err := write("p2")

			Convey("Then it is refused rather than stored where no query reaches", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsArchived(err), ShouldBeTrue)
			})
		})

		Convey("When the type is restored", func() {
			_, rerr := svc.Interactors(ctx).TypeDefinitions().Restore(ctx, product.ID.String())
			So(rerr, ShouldBeNil)

			Convey("Then writes are accepted again", func() {
				So(write("p2"), ShouldBeNil)
			})
		})
	})
}
