package memory_test

import (
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
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestBulkOperationsAndEntityLifecycle(t *testing.T) {
	Convey("Given a product type with name and price attributes", t, func() {
		svc := flexitype.NewInMemory()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		it := svc.Interactors(ctx)

		pt, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		nameAttr, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: pt.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)
		priceAttr, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: pt.ID.String(), InternalName: "price", DisplayName: "Price", DataType: "integer",
		})
		So(err, ShouldBeNil)

		Convey("When a batch of valid values is set", func() {
			out, err := it.Values().SetBatch(ctx, appvalue.BatchSetInput{Items: []appvalue.SetInput{
				{AttributeDefinitionID: nameAttr.ID.String(), EntityID: "e1", Value: json.RawMessage(`"Widget"`)},
				{AttributeDefinitionID: priceAttr.ID.String(), EntityID: "e1", Value: json.RawMessage(`42`)},
			}})

			Convey("Then every value is written in one unit of work", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 2)
				vals, err := it.Values().ListByEntity(ctx, pt.ID.String(), "e1")
				So(err, ShouldBeNil)
				So(vals, ShouldHaveLength, 2)
			})
		})

		Convey("When a batch contains one invalid item", func() {
			_, err := it.Values().SetBatch(ctx, appvalue.BatchSetInput{Items: []appvalue.SetInput{
				{AttributeDefinitionID: nameAttr.ID.String(), EntityID: "e2", Value: json.RawMessage(`"Gadget"`)},
				{AttributeDefinitionID: priceAttr.ID.String(), EntityID: "e2", Value: json.RawMessage(`"not-an-int"`)},
			}})

			Convey("Then the whole batch rolls back and nothing is written", func() {
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				vals, verr := it.Values().ListByEntity(ctx, pt.ID.String(), "e2")
				So(verr, ShouldBeNil)
				So(vals, ShouldBeEmpty)
			})
		})

		Convey("When an empty batch is submitted", func() {
			_, err := it.Values().SetBatch(ctx, appvalue.BatchSetInput{})
			Convey("Then it is rejected as validation", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("Given an entity with values and a relationship", func() {
			_, err := it.Values().SetBatch(ctx, appvalue.BatchSetInput{Items: []appvalue.SetInput{
				{AttributeDefinitionID: nameAttr.ID.String(), EntityID: "e1", Value: json.RawMessage(`"Widget"`)},
				{AttributeDefinitionID: priceAttr.ID.String(), EntityID: "e1", Value: json.RawMessage(`42`)},
			}})
			So(err, ShouldBeNil)
			relDef, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
				InternalName: "uses", DisplayName: "Uses", ParentTypeID: pt.ID.String(), ChildTypeID: pt.ID.String(),
			})
			So(err, ShouldBeNil)
			_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{
				DefinitionID: relDef.ID.String(), ParentEntity: "e1", ChildEntity: "e2",
			})
			So(err, ShouldBeNil)

			Convey("When the entity is removed", func() {
				out, err := it.Values().RemoveEntity(ctx, pt.ID.String(), "e1")

				Convey("Then its values are archived and its links unlinked in one cascade", func() {
					So(err, ShouldBeNil)
					So(out.ValuesRemoved, ShouldEqual, 2)
					So(out.RelationshipsGone, ShouldEqual, 1)

					vals, verr := it.Values().ListByEntity(ctx, pt.ID.String(), "e1")
					So(verr, ShouldBeNil)
					So(vals, ShouldBeEmpty)
					links, lerr := it.Relationships().ListByEntity(ctx, "e1")
					So(lerr, ShouldBeNil)
					So(links, ShouldBeEmpty)
				})
			})

			Convey("When an entity with no values and no links is removed", func() {
				_, err := it.Values().RemoveEntity(ctx, pt.ID.String(), "ghost")
				Convey("Then it is reported NotFound", func() {
					So(domainerrors.IsNotFound(err), ShouldBeTrue)
				})
			})
		})
	})
}
