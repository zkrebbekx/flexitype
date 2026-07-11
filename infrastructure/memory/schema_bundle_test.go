package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestSchemaBundleRoundTrip(t *testing.T) {
	Convey("Given a source instance with a schema (inheritance, constraints, a relationship and a dependency)", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		src := flexitype.NewInMemory()
		it := src.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		book, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "book", DisplayName: "Book", ExtendsID: product.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string", Required: true,
			Constraints: json.RawMessage(`[{"kind":"max_length","n":100}]`),
		})
		So(err, ShouldBeNil)
		fmtAttr, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "format", DisplayName: "Format", DataType: "string",
		})
		So(err, ShouldBeNil)
		pagesAttr, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: book.ID.String(), InternalName: "pages", DisplayName: "Pages", DataType: "integer",
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "sequel_of", DisplayName: "Sequel of",
			ParentTypeID: book.ID.String(), ChildTypeID: book.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = it.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: fmtAttr.ID.String(), TargetAttributeID: pagesAttr.ID.String(),
			Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"ebook"}}]`),
			Effect:     json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		bundle, err := it.Schema().Export(ctx)
		So(err, ShouldBeNil)

		Convey("When the bundle is imported into a fresh instance", func() {
			dst := flexitype.NewInMemory()
			res, err := dst.Interactors(ctx).Schema().Import(ctx, bundle)

			Convey("Then everything is created and a re-export matches the original", func() {
				So(err, ShouldBeNil)
				So(res.Types.Created, ShouldEqual, 2)
				So(res.Attributes.Created, ShouldEqual, 3)
				So(res.RelationshipDefinitions.Created, ShouldEqual, 1)
				So(res.Dependencies.Created, ShouldEqual, 1)

				reExport, err := dst.Interactors(ctx).Schema().Export(ctx)
				So(err, ShouldBeNil)
				orig, _ := json.Marshal(bundle)
				round, _ := json.Marshal(reExport)
				So(string(round), ShouldEqual, string(orig))
			})

			Convey("And re-importing is idempotent — nothing is created twice", func() {
				So(err, ShouldBeNil)
				res2, err := dst.Interactors(ctx).Schema().Import(ctx, bundle)
				So(err, ShouldBeNil)
				So(res2.Types.Created, ShouldEqual, 0)
				So(res2.Types.Skipped, ShouldEqual, 2)
				So(res2.Attributes.Created, ShouldEqual, 0)
				So(res2.Attributes.Skipped, ShouldEqual, 3)
				So(res2.RelationshipDefinitions.Created, ShouldEqual, 0)
				So(res2.Dependencies.Created, ShouldEqual, 0)
			})
		})

		Convey("When a bundle with an unsupported version is imported", func() {
			dst := flexitype.NewInMemory()
			bad := *bundle
			bad.Version = 999
			_, err := dst.Interactors(ctx).Schema().Import(ctx, &bad)
			Convey("Then it is rejected", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
