package memory_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestAttributePresentationGrouping(t *testing.T) {
	Convey("Given a product type with grouped, ordered attributes and a subtype", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)

		mk := func(typeID, name, group string, order int, help string) {
			_, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string",
				Group: group, SortOrder: order, HelpText: help,
			})
			So(err, ShouldBeNil)
		}
		// Pricing: currency(1), price(2); Basics: name(1). Deliberately out of order.
		mk(product.ID.String(), "price", "Pricing", 2, "In cents")
		mk(product.ID.String(), "currency", "Pricing", 1, "")
		mk(product.ID.String(), "name", "Basics", 1, "Product name")

		book, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "book", DisplayName: "Book", ExtendsID: product.ID.String(),
		})
		So(err, ShouldBeNil)
		// Book's own attribute joins the inherited Pricing group.
		mk(book.ID.String(), "discount", "Pricing", 3, "")

		Convey("When effective-attributes are read for the subtype", func() {
			eff, err := it.TypeDefinitions().EffectiveAttributes(ctx, book.ID.String())
			So(err, ShouldBeNil)

			names := make([]string, len(eff))
			groups := make([]string, len(eff))
			for i, e := range eff {
				names[i] = e.Attribute.InternalName
				groups[i] = e.Attribute.Group
			}

			Convey("Then they are grouped and ordered, with inherited attributes merged", func() {
				// Basics (name) sorts before Pricing; within Pricing, order 1,2,3
				// with the subtype's discount merged after the inherited pair.
				So(names, ShouldResemble, []string{"name", "currency", "price", "discount"})
				So(groups, ShouldResemble, []string{"Basics", "Pricing", "Pricing", "Pricing"})
			})

			Convey("And help text is preserved", func() {
				byName := map[string]string{}
				for _, e := range eff {
					byName[e.Attribute.InternalName] = e.Attribute.HelpText
				}
				So(byName["price"], ShouldEqual, "In cents")
				So(byName["name"], ShouldEqual, "Product name")
			})
		})

		Convey("When an attribute's group/order is changed", func() {
			// Move name into Pricing at the front.
			eff, _ := it.TypeDefinitions().EffectiveAttributes(ctx, product.ID.String())
			var nameID string
			for _, e := range eff {
				if e.Attribute.InternalName == "name" {
					nameID = e.Attribute.ID.String()
				}
			}
			_, err := it.Attributes().Update(ctx, appattribute.UpdateInput{
				ID: nameID, DisplayName: "Name", Group: "Pricing", SortOrder: 0,
			})
			So(err, ShouldBeNil)

			Convey("Then the new grouping is reflected", func() {
				eff2, err := it.TypeDefinitions().EffectiveAttributes(ctx, product.ID.String())
				So(err, ShouldBeNil)
				So(eff2[0].Attribute.InternalName, ShouldEqual, "name")
				So(eff2[0].Attribute.Group, ShouldEqual, "Pricing")
			})
		})
	})
}
