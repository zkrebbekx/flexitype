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
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestFacetsParityPostgres re-runs the facet suite
// (infrastructure/memory/facets_test.go) against Postgres, exercising the
// value-count aggregation SQL (grouped counts, highest-first ordering) over
// both the full population and a narrowed entity set.
func TestFacetsParityPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given products with a category over several entities (Postgres)", t, func() {
		truncateAll(t, pool)
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
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
