package memory_test

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
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestFacets(t *testing.T) {
	Convey("Given products with a category over several entities", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		category, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: typeID, InternalName: "category", DisplayName: "Category", DataType: "string",
		})
		So(err, ShouldBeNil)

		set := func(entity, v string) {
			raw, _ := json.Marshal(v)
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: category.ID.String(), EntityID: entity, TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}
		set("a", "bike")
		set("b", "bike")
		set("c", "bike")
		set("d", "helmet")
		set("e", "glove")

		Convey("When facets are computed over all five entities", func() {
			out, err := it.Values().Facets(ctx, typeID, []string{"category"}, []string{"a", "b", "c", "d", "e"})

			Convey("Then value counts are returned, highest first", func() {
				So(err, ShouldBeNil)
				buckets := out.Facets["category"]
				So(buckets, ShouldHaveLength, 3)
				So(buckets[0].Value, ShouldEqual, "bike")
				So(buckets[0].Count, ShouldEqual, 3)
				So(buckets[1].Count, ShouldEqual, 1)
				So(buckets[2].Count, ShouldEqual, 1)
			})
		})

		Convey("When facets are computed over a narrowed set", func() {
			out, err := it.Values().Facets(ctx, typeID, []string{"category"}, []string{"d", "e"})

			Convey("Then only the values present in that set are counted", func() {
				So(err, ShouldBeNil)
				buckets := out.Facets["category"]
				So(buckets, ShouldHaveLength, 2)
				for _, b := range buckets {
					So(b.Count, ShouldEqual, 1)
					So(b.Value, ShouldBeIn, "helmet", "glove")
				}
			})
		})
	})
}
