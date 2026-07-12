package memory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/blob"
)

// TestPurgeEntity proves the per-entity hard delete removes every trace of an
// entity — live and archived values, media blobs, revisions and links — while
// leaving a sibling entity intact.
func TestPurgeEntity(t *testing.T) {
	Convey("Given e1 with values, a media blob, an archived value, revisions and a link to e2", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		store := blob.NewMemoryStore()
		svc := flexitype.NewInMemory(flexitype.WithBlobStore(store), flexitype.WithSearchIndex())
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		name, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		sku, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		image, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "image", DisplayName: "Image", DataType: "media",
		})
		So(err, ShouldBeNil)

		// e1: a live value, a media value (blob), and a value we archive.
		_, err = it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: name.ID.String(), EntityID: "e1", Value: json.RawMessage(`"Widget"`)})
		So(err, ShouldBeNil)
		png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 50)...)
		mediaSnap, err := it.Values().UploadMedia(ctx, typeID, "e1", image.ID.String(), bytes.NewReader(png), "logo.png")
		So(err, ShouldBeNil)
		blobKey := mediaSnap.Value.Media().ObjectKey
		rc, _, err := store.Open(ctx, blobKey)
		So(err, ShouldBeNil)
		_ = rc.Close()

		skuSnap, err := it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: sku.ID.String(), EntityID: "e1", Value: json.RawMessage(`"SKU-1"`)})
		So(err, ShouldBeNil)
		_, err = it.Values().Remove(ctx, skuSnap.ID.String()) // soft-remove: archived row remains
		So(err, ShouldBeNil)

		// Two revisions of e1.
		_, err = it.Revisions().Create(ctx, typeID, "e1", "v1")
		So(err, ShouldBeNil)
		_, err = it.Revisions().Create(ctx, typeID, "e1", "v2")
		So(err, ShouldBeNil)

		// A link e1 -> e2.
		relDef, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "related", DisplayName: "Related", ParentTypeID: typeID, ChildTypeID: typeID,
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: relDef.ID.String(), ParentEntity: "e1", ChildEntity: "e2",
		})
		So(err, ShouldBeNil)

		// e2 has its own value; it must survive the purge of e1.
		_, err = it.Values().Set(ctx, appvalue.SetInput{AttributeDefinitionID: name.ID.String(), EntityID: "e2", Value: json.RawMessage(`"Gadget"`)})
		So(err, ShouldBeNil)

		Convey("When e1 is purged", func() {
			report, err := it.Values().PurgeEntity(ctx, typeID, "e1")
			So(err, ShouldBeNil)

			Convey("Then the report counts every erased trace", func() {
				So(report.EntityID, ShouldEqual, "e1")
				So(report.ValuesPurged, ShouldEqual, 3) // name + media + archived sku
				So(report.RevisionsPurged, ShouldEqual, 2)
				So(report.RelationshipsGone, ShouldEqual, 1)
				So(report.MediaBlobsPurged, ShouldEqual, 1)
			})

			Convey("Then e1's live and archived values are gone", func() {
				live, err := it.Values().ListByEntity(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(live, ShouldBeEmpty)
				all, err := it.Values().List(ctx, appvalue.ListInput{EntityID: "e1", IncludeArchived: true})
				So(err, ShouldBeNil)
				So(all.Items, ShouldBeEmpty)
			})

			Convey("Then e1's revisions are gone", func() {
				revs, err := it.Revisions().List(ctx, typeID, "e1")
				So(err, ShouldBeNil)
				So(revs, ShouldBeEmpty)
			})

			Convey("Then the link is gone from both sides", func() {
				links, err := it.Relationships().ListByEntity(ctx, "e2")
				So(err, ShouldBeNil)
				So(links, ShouldBeEmpty)
			})

			Convey("Then the media blob is gone from the store", func() {
				_, _, err := store.Open(ctx, blobKey)
				So(err, ShouldNotBeNil)
			})

			Convey("Then e2 is untouched", func() {
				vals, err := it.Values().ListByEntity(ctx, typeID, "e2")
				So(err, ShouldBeNil)
				So(vals, ShouldNotBeEmpty)
			})
		})

		Convey("When an entity with nothing to erase is purged", func() {
			_, err := it.Values().PurgeEntity(ctx, typeID, "ghost")

			Convey("Then it is reported NotFound", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}

// TestPurgeTenant proves the per-tenant hard delete erases one tenant's entity
// data while another tenant's data is left intact.
func TestPurgeTenant(t *testing.T) {
	Convey("Given tenant A and tenant B each with entity data over a shared store", t, func() {
		store := blob.NewMemoryStore()
		svc := flexitype.NewInMemory(flexitype.WithBlobStore(store), flexitype.WithSearchIndex())

		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))
		a := svc.Interactors(ctxA)
		b := svc.Interactors(ctxB)

		// Tenant A schema + data.
		typeA, err := a.TypeDefinitions().Create(ctxA, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		nameA, err := a.Attributes().Create(ctxA, appattribute.CreateInput{
			TypeDefinitionID: typeA.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = a.Values().Set(ctxA, appvalue.SetInput{AttributeDefinitionID: nameA.ID.String(), EntityID: "a1", Value: json.RawMessage(`"A-Widget"`)})
		So(err, ShouldBeNil)
		_, err = a.Revisions().Create(ctxA, typeA.ID.String(), "a1", "v1")
		So(err, ShouldBeNil)
		relDefA, err := a.Relationships().CreateDefinition(ctxA, apprelationship.CreateDefinitionInput{
			InternalName: "related", DisplayName: "Related", ParentTypeID: typeA.ID.String(), ChildTypeID: typeA.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = a.Relationships().Link(ctxA, apprelationship.LinkInput{DefinitionID: relDefA.ID.String(), ParentEntity: "a1", ChildEntity: "a2"})
		So(err, ShouldBeNil)

		// Tenant B schema + data.
		typeB, err := b.TypeDefinitions().Create(ctxB, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		nameB, err := b.Attributes().Create(ctxB, appattribute.CreateInput{
			TypeDefinitionID: typeB.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = b.Values().Set(ctxB, appvalue.SetInput{AttributeDefinitionID: nameB.ID.String(), EntityID: "b1", Value: json.RawMessage(`"B-Widget"`)})
		So(err, ShouldBeNil)
		_, err = b.Revisions().Create(ctxB, typeB.ID.String(), "b1", "v1")
		So(err, ShouldBeNil)

		Convey("When tenant A's data is purged", func() {
			report, err := a.Values().PurgeTenant(ctxA)
			So(err, ShouldBeNil)

			Convey("Then A's report shows what was erased", func() {
				So(report.EntityID, ShouldBeBlank)
				So(report.ValuesPurged, ShouldEqual, 1)
				So(report.RevisionsPurged, ShouldEqual, 1)
				So(report.RelationshipsGone, ShouldEqual, 1)
			})

			Convey("Then A's entity data is gone", func() {
				vals, err := a.Values().ListByEntity(ctxA, typeA.ID.String(), "a1")
				So(err, ShouldBeNil)
				So(vals, ShouldBeEmpty)
				revs, err := a.Revisions().List(ctxA, typeA.ID.String(), "a1")
				So(err, ShouldBeNil)
				So(revs, ShouldBeEmpty)
				links, err := a.Relationships().ListByEntity(ctxA, "a1")
				So(err, ShouldBeNil)
				So(links, ShouldBeEmpty)
			})

			Convey("Then A's type definition (schema) survives", func() {
				got, err := a.TypeDefinitions().Get(ctxA, typeA.ID.String())
				So(err, ShouldBeNil)
				So(got.DisplayName, ShouldEqual, "Product")
			})

			Convey("Then tenant B's data is untouched", func() {
				vals, err := b.Values().ListByEntity(ctxB, typeB.ID.String(), "b1")
				So(err, ShouldBeNil)
				So(vals, ShouldNotBeEmpty)
				revs, err := b.Revisions().List(ctxB, typeB.ID.String(), "b1")
				So(err, ShouldBeNil)
				So(revs, ShouldNotBeEmpty)
			})
		})
	})
}
