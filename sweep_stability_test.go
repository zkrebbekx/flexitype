package flexitype_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestSweepSkipsNothingUnderConcurrentWrites covers the rows a full sweep used
// to lose.
//
// Keyset pagination promised stability "under concurrent inserts and deletes —
// no skipped or duplicated rows", and the entity listing ordered newest-first
// on last_updated_at, which a trigger rewrites on every value write. An entity
// the sweep had not reached yet, written mid-sweep, jumped to now() — ahead of
// a cursor selecting strictly older rows — and could never satisfy that
// predicate again. It was dropped, silently, from a reindex, a CSV export, a
// completeness score or a recompute.
//
// A sweep now pages on the entity id, which never changes.
func TestSweepSkipsNothingUnderConcurrentWrites(t *testing.T) {
	Convey("Given a type with more entities than fit one page", t, func() {
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

		write := func(entity, v string) {
			raw, _ := json.Marshal(v)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: sku.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
		}

		const total = 25
		for i := 0; i < total; i++ {
			write(fmt.Sprintf("p%02d", i), fmt.Sprintf("SKU-%02d", i))
		}

		// sweep pages with the given lister, touching an unvisited entity
		// after each page — the write that used to make a row vanish.
		sweep := func(stable bool) map[string]bool {
			seen := map[string]bool{}
			limit := 5
			var cursor *string
			for {
				var page *appvalue.EntityListOutput
				var lerr error
				if stable {
					page, lerr = svc.Interactors(ctx).Values().ListEntitiesStable(
						ctx, product.ID.String(), false, db.PageArgs{Limit: &limit, Cursor: cursor})
				} else {
					page, lerr = svc.Interactors(ctx).Values().ListEntities(
						ctx, product.ID.String(), false, db.PageArgs{Limit: &limit, Cursor: cursor})
				}
				So(lerr, ShouldBeNil)
				for _, e := range page.Items {
					seen[e.EntityID] = true
				}
				// Touch an entity the sweep has not reached yet.
				for i := total - 1; i >= 0; i-- {
					id := fmt.Sprintf("p%02d", i)
					if !seen[id] {
						write(id, "touched")
						break
					}
				}
				if !page.PageInfo.HasNextPage || page.PageInfo.NextCursor == nil {
					return seen
				}
				cursor = page.PageInfo.NextCursor
			}
		}

		Convey("When a stable sweep runs while entities are written", func() {
			seen := sweep(true)

			Convey("Then every entity is visited exactly once", func() {
				So(len(seen), ShouldEqual, total)
				for i := 0; i < total; i++ {
					So(seen[fmt.Sprintf("p%02d", i)], ShouldBeTrue)
				}
			})
		})

		Convey("When the newest-first listing is swept the same way", func() {
			seen := sweep(false)

			Convey("Then it loses rows: this is why sweeps need the stable order", func() {
				// Pinned as a property of the ordering, not an aspiration:
				// a browser listing is allowed to reorder under writes.
				So(len(seen), ShouldBeLessThan, total)
			})
		})
	})
}
