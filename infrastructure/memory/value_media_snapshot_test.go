package memory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// onePixelPNG is the smallest thing http.DetectContentType calls an image/png.
var onePixelPNG = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32))

func TestUploadMediaGuards(t *testing.T) {
	Convey("Given a media attribute and a working blob store", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		var cleanupErrs []error
		svc := flexitype.NewInMemory(
			flexitype.WithBlobStore(blob.NewMemoryStore()),
			flexitype.WithCleanupObserver(func(err error) { cleanupErrs = append(cleanupErrs, err) }),
		)
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "image",
			DisplayName: "Image", DataType: "media",
		})
		So(err, ShouldBeNil)
		sku, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)

		Convey("When the attribute ID is malformed or unknown", func() {
			_, badErr := it.Values().UploadMedia(ctx, typeID, "p1", "nope",
				bytes.NewReader(onePixelPNG), "a.png")
			_, missingErr := it.Values().UploadMedia(ctx, typeID, "p1",
				valueobjects.NewAttributeDefinitionID().String(),
				bytes.NewReader(onePixelPNG), "a.png")

			Convey("Then the malformed ID is validation and the unknown one is not-found", func() {
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
				So(domainerrors.IsNotFound(missingErr), ShouldBeTrue)
			})
		})

		Convey("When the target attribute is not a media attribute", func() {
			_, err := it.Values().UploadMedia(ctx, typeID, "p1", sku.ID.String(),
				bytes.NewReader(onePixelPNG), "a.png")

			Convey("Then the upload is refused naming the attribute", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "not a media attribute")
			})
		})

		Convey("When another tenant uploads to the attribute", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := it.Values().UploadMedia(other, typeID, "p1", image.ID.String(),
				bytes.NewReader(onePixelPNG), "a.png")

			Convey("Then it is refused across the tenant boundary", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When a PNG is uploaded", func() {
			snap, err := it.Values().UploadMedia(ctx, typeID, "p1", image.ID.String(),
				bytes.NewReader(onePixelPNG), "photo.PNG")

			Convey("Then the sniffed mime, size, checksum and lowercased extension are stored", func() {
				So(err, ShouldBeNil)
				meta := snap.Value.Media()
				So(meta.MIME, ShouldEqual, "image/png")
				So(meta.Size, ShouldEqual, int64(len(onePixelPNG)))
				So(meta.Checksum, ShouldStartWith, "sha256:")
				So(meta.Filename, ShouldEqual, "photo.PNG")
				So(meta.ObjectKey, ShouldEndWith, ".png")
				So(cleanupErrs, ShouldBeEmpty)
			})

			Convey("And the caller's tenant owns the object key", func() {
				owned, err := it.Values().MediaKeyOwned(ctx, snap.Value.Media().ObjectKey)
				So(err, ShouldBeNil)
				So(owned, ShouldBeTrue)

				other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
				owned, err = it.Values().MediaKeyOwned(other, snap.Value.Media().ObjectKey)
				So(err, ShouldBeNil)
				So(owned, ShouldBeFalse)
			})
		})
	})

}

func TestUploadMediaRejectionCleansUpTheBlob(t *testing.T) {
	Convey("Given a media attribute restricted to JPEGs", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		blobs := blob.NewMemoryStore()
		it := flexitype.NewInMemory(flexitype.WithBlobStore(blobs)).Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "image",
			DisplayName: "Image", DataType: "media",
			Constraints: json.RawMessage(`[{"kind":"media","mime":["image/jpeg"]}]`),
		})
		So(err, ShouldBeNil)

		Convey("When a PNG is uploaded", func() {
			_, err := it.Values().UploadMedia(ctx, product.ID.String(), "p1",
				image.ID.String(), bytes.NewReader(onePixelPNG), "a.png")

			Convey("Then the value is rejected and no blob is left orphaned", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)

				// Nothing was stored, so no value references any object either.
				vals, lerr := it.Values().ListByEntity(ctx, product.ID.String(), "p1")
				So(lerr, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})
	})
}

func TestRemoveEntityCascade(t *testing.T) {
	Convey("Given an entity holding values and links", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := flexitype.NewInMemory().Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		part, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "part", DisplayName: "Part",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		sku, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)

		raw, err := json.Marshal("ABC")
		So(err, ShouldBeNil)
		_, err = it.Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: sku.ID.String(), EntityID: "p1",
			TypeDefinitionID: typeID, Value: raw,
		})
		So(err, ShouldBeNil)

		def, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "contains", DisplayName: "Contains",
			ParentTypeID: typeID, ChildTypeID: part.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: def.ID.String(), ParentEntity: "p1", ChildEntity: "part-1",
		})
		So(err, ShouldBeNil)

		Convey("When the entity is removed", func() {
			out, err := it.Values().RemoveEntity(ctx, typeID, "p1")

			Convey("Then values and links are both cascaded and counted", func() {
				So(err, ShouldBeNil)
				So(out.EntityID, ShouldEqual, "p1")
				So(out.ValuesRemoved, ShouldEqual, 1)
				So(out.RelationshipsGone, ShouldEqual, 1)

				vals, err := it.Values().ListByEntity(ctx, typeID, "p1")
				So(err, ShouldBeNil)
				So(vals, ShouldBeEmpty)

				links, err := it.Relationships().ListByEntity(ctx, "p1")
				So(err, ShouldBeNil)
				So(links, ShouldBeEmpty)
			})
		})

		Convey("When an entity with neither values nor links is removed", func() {
			_, err := it.Values().RemoveEntity(ctx, typeID, "ghost")

			Convey("Then it is reported not-found rather than a silent no-op", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
			})
		})

		Convey("When the type or entity ID is malformed", func() {
			_, typeErr := it.Values().RemoveEntity(ctx, "nope", "p1")
			_, entityErr := it.Values().RemoveEntity(ctx, typeID, "")

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(entityErr), ShouldBeTrue)
			})
		})

		Convey("When another tenant removes the entity", func() {
			other := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := it.Values().RemoveEntity(other, typeID, "p1")

			Convey("Then nothing is visible to cascade", func() {
				So(domainerrors.IsNotFound(err), ShouldBeTrue)
				vals, lerr := it.Values().ListByEntity(ctx, typeID, "p1")
				So(lerr, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
			})
		})
	})
}

func TestApplySnapshotRestore(t *testing.T) {
	Convey("Given an entity whose values have drifted from a snapshot", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := flexitype.NewInMemory().Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		attr := func(name, dt string, scopable bool) string {
			snap, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
				DataType: dt, Scopable: scopable, Localizable: scopable,
			})
			So(e, ShouldBeNil)
			return snap.ID.String()
		}
		sku := attr("sku", "string", false)
		stock := attr("stock", "integer", false)
		note := attr("note", "string", true)

		set := func(attrID, locale string, v any) {
			raw, e := json.Marshal(v)
			So(e, ShouldBeNil)
			_, e = it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1",
				TypeDefinitionID: typeID, Locale: locale, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		set(sku, "", "NEW-SKU")
		set(stock, "", 5)
		set(note, "en_AU", "aussie note")

		Convey("When a snapshot naming only sku and a scoped note is applied", func() {
			err := it.Values().ApplySnapshot(ctx, typeID, "p1", []apprevision.SnapshotCell{
				{AttributeDefinitionID: sku, DataType: "string", Value: "OLD-SKU"},
				{AttributeDefinitionID: note, DataType: "string", Locale: "en_AU", Value: "old aussie note"},
			})

			Convey("Then named cells are restored and unnamed ones are archived", func() {
				So(err, ShouldBeNil)

				vals, err := it.Values().ListByEntity(ctx, typeID, "p1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 2)

				byAttr := map[string]string{}
				for _, v := range vals {
					byAttr[v.AttributeDefinitionID.String()+"|"+v.Locale] = v.Value.String()
				}
				So(byAttr[sku+"|"], ShouldEqual, "OLD-SKU")
				So(byAttr[note+"|en_AU"], ShouldEqual, "old aussie note")
				// stock was absent from the snapshot, so it is archived.
				So(byAttr, ShouldNotContainKey, stock+"|")
			})
		})

		Convey("When a scoped value is absent from the snapshot", func() {
			err := it.Values().ApplySnapshot(ctx, typeID, "p1", []apprevision.SnapshotCell{
				{AttributeDefinitionID: sku, DataType: "string", Value: "OLD-SKU"},
			})

			Convey("Then each scope is archived independently of the base value", func() {
				So(err, ShouldBeNil)
				vals, err := it.Values().ListByEntity(ctx, typeID, "p1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 1)
				So(vals[0].AttributeDefinitionID.String(), ShouldEqual, sku)
				So(vals[0].Locale, ShouldBeEmpty)
			})
		})

		Convey("When the snapshot names a malformed attribute", func() {
			err := it.Values().ApplySnapshot(ctx, typeID, "p1", []apprevision.SnapshotCell{
				{AttributeDefinitionID: "nope", DataType: "string", Value: "X"},
			})

			Convey("Then the restore is refused and nothing is archived", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				vals, lerr := it.Values().ListByEntity(ctx, typeID, "p1")
				So(lerr, ShouldBeNil)
				So(vals, ShouldHaveLength, 3)
			})
		})

		Convey("When a snapshot cell's value does not fit its declared type", func() {
			err := it.Values().ApplySnapshot(ctx, typeID, "p1", []apprevision.SnapshotCell{
				{AttributeDefinitionID: stock, DataType: "integer", Value: "not-a-number"},
			})

			Convey("Then the whole restore rolls back", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				vals, lerr := it.Values().ListByEntity(ctx, typeID, "p1")
				So(lerr, ShouldBeNil)
				So(vals, ShouldHaveLength, 3)
			})
		})

		Convey("When the type or entity ID is malformed", func() {
			typeErr := it.Values().ApplySnapshot(ctx, "nope", "p1", nil)
			entityErr := it.Values().ApplySnapshot(ctx, typeID, "", nil)

			Convey("Then each is rejected as validation", func() {
				So(domainerrors.IsValidation(typeErr), ShouldBeTrue)
				So(domainerrors.IsValidation(entityErr), ShouldBeTrue)
			})
		})

		Convey("When an empty snapshot is applied", func() {
			err := it.Values().ApplySnapshot(ctx, typeID, "p1", nil)

			Convey("Then every live value is archived", func() {
				So(err, ShouldBeNil)
				vals, lerr := it.Values().ListByEntity(ctx, typeID, "p1")
				So(lerr, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})
	})
}
