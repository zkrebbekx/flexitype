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
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestKeysetPaginationStableUnderInsert is the point of #42: a keyset page is
// stable when a row is inserted at the front between requests — no entity is
// skipped or returned twice, which offset pagination cannot guarantee.
func TestKeysetPaginationStableUnderInsert(t *testing.T) {
	Convey("Given a type with several entities, listed newest-first", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		nameA, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name", DataType: "string",
		})
		So(err, ShouldBeNil)

		set := func(entity, name string) {
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: nameA.ID.String(), EntityID: entity, TypeDefinitionID: product.ID.String(),
				Value: json.RawMessage(`"` + name + `"`),
			})
			So(e, ShouldBeNil)
		}
		for _, id := range []string{"e1", "e2", "e3", "e4"} {
			set(id, id)
		}

		limit := 2
		listPage := func(cursor string) (*appvalue.EntityListOutput, error) {
			pa := db.PageArgs{Limit: &limit}
			if cursor != "" {
				pa.Cursor = &cursor
			}
			return svc.Interactors(ctx).Values().ListEntities(ctx, product.ID.String(), false, pa)
		}

		Convey("When a new entity is inserted at the front between two pages", func() {
			page1, err := listPage("")
			So(err, ShouldBeNil)
			So(len(page1.Items), ShouldEqual, 2)
			So(page1.PageInfo.HasNextPage, ShouldBeTrue)
			So(page1.PageInfo.NextCursor, ShouldNotBeNil)

			seen := map[string]int{}
			for _, e := range page1.Items {
				seen[e.EntityID]++
			}

			// A brand-new entity sorts to the very front (newest); with offset
			// pagination this would shift the second page and duplicate a row.
			set("e5", "e5")

			cursor := *page1.PageInfo.NextCursor
			for {
				pg, err := listPage(cursor)
				So(err, ShouldBeNil)
				for _, e := range pg.Items {
					seen[e.EntityID]++
				}
				if !pg.PageInfo.HasNextPage || pg.PageInfo.NextCursor == nil {
					break
				}
				cursor = *pg.PageInfo.NextCursor
			}

			Convey("Then every original entity appears exactly once — no skip, no duplicate", func() {
				for _, id := range []string{"e1", "e2", "e3", "e4"} {
					So(seen[id], ShouldEqual, 1)
				}
				// e5 was inserted ahead of the cursor after we passed the front,
				// so it is not seen on the later pages (and never duplicated).
				So(seen["e5"], ShouldBeLessThanOrEqualTo, 1)
			})
		})

		Convey("When the total is not requested", func() {
			page1, err := listPage("")
			So(err, ShouldBeNil)
			Convey("Then total_count is omitted", func() {
				So(page1.PageInfo.TotalCount, ShouldBeNil)
			})
		})

		Convey("When the total is requested", func() {
			pa := db.PageArgs{Limit: &limit, WantTotal: true}
			out, err := svc.Interactors(ctx).Values().ListEntities(ctx, product.ID.String(), false, pa)
			So(err, ShouldBeNil)
			Convey("Then total_count reflects the full population", func() {
				So(out.PageInfo.TotalCount, ShouldNotBeNil)
				So(*out.PageInfo.TotalCount, ShouldEqual, 4)
			})
		})
	})
}
