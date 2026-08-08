package flexitype_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// runStableSweepParity is the regression for #499.
//
// The FQL query paged newest-first on last_updated_at, unconditionally, and
// had no way to ask for anything else. A trigger rewrites that column on
// every value write, so an entity the sweep has not reached yet, written
// mid-sweep, jumps ahead of the cursor and can never satisfy the "strictly
// older" predicate again. It is dropped.
//
// The sweeps built on the FQL path are the grid's facet counts and a
// ?query=-filtered CSV export. Neither could set stable ordering, so the
// counts disagreed with the grid and the export lost rows — silently, since
// an export asks for no total.
func runStableSweepParity(t *testing.T, label string, setup func() *flexitype.Service) {
	t.Helper()

	Convey("Given entities matching a query, paged two at a time ("+label+")", t, func() {
		svc := setup()
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		status, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "status",
			DisplayName: "Status", DataType: "string",
		})
		So(err, ShouldBeNil)

		// Each write gets its own instant. Without the gap every entity
		// shares one last_updated_at, the unstable ordering falls through to
		// its entity_id tiebreaker, and the defect cannot appear at all.
		write := func(entity string) {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: status.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"live"`),
			})
			So(serr, ShouldBeNil)
			time.Sleep(2 * time.Millisecond)
		}
		note, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "note",
			DisplayName: "Note", DataType: "string",
		})
		So(err, ShouldBeNil)
		// touch moves an entity's last_updated_at WITHOUT changing whether it
		// matches the query. Rewriting the same value would not move it: an
		// unchanged write is a no-op.
		touched := 0
		touch := func(entity string) {
			touched++
			raw, merr := json.Marshal("touch-" + entity + "-" + strconv.Itoa(touched))
			So(merr, ShouldBeNil)
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: note.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(), Value: raw,
			})
			So(serr, ShouldBeNil)
			time.Sleep(2 * time.Millisecond)
		}
		for _, e := range []string{"e1", "e2", "e3", "e4"} {
			write(e)
		}

		sweep := func(stable bool, touchMidway string) []string {
			limit := 2
			var cursor *string
			seen := []string{}
			for {
				out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
					Type: "product", Query: `status = "live"`, Stable: stable,
					Page: db.PageArgs{Limit: &limit, Cursor: cursor},
				})
				So(qerr, ShouldBeNil)
				for _, r := range out.Items {
					seen = append(seen, r.EntityID)
				}
				if out.PageInfo.NextCursor == nil {
					return seen
				}
				cursor = out.PageInfo.NextCursor
				// A concurrent write lands between pages: it rewrites the
				// entity's last_updated_at, which is the unstable sort key.
				if touchMidway != "" {
					touch(touchMidway)
					touchMidway = ""
				}
			}
		}

		Convey("When a sweep runs with stable ordering and an entity is written mid-sweep", func() {
			seen := sweep(true, "e1")

			Convey("Then every entity is seen exactly once", func() {
				So(seen, ShouldHaveLength, 4)
				unique := map[string]bool{}
				for _, id := range seen {
					unique[id] = true
				}
				So(unique, ShouldHaveLength, 4)
			})
		})

		Convey("When a sweep runs with the default ordering and nothing is written", func() {
			seen := sweep(false, "")

			Convey("Then it still returns every entity: the ordering is only unsafe under writes", func() {
				So(seen, ShouldHaveLength, 4)
			})
		})

		Convey("When a stable page is requested without a cursor", func() {
			limit := 2
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: `status = "live"`, Stable: true,
				Page: db.PageArgs{Limit: &limit},
			})

			Convey("Then it orders by entity id, not by last update", func() {
				So(qerr, ShouldBeNil)
				ids := []string{}
				for _, r := range out.Items {
					ids = append(ids, r.EntityID)
				}
				So(ids, ShouldResemble, []string{"e1", "e2"})
			})
		})
	})
}

// TestFQLStableSweep runs the scenarios against the in-memory backend.
func TestFQLStableSweep(t *testing.T) {
	runStableSweepParity(t, "memory", func() *flexitype.Service {
		return flexitype.NewInMemory()
	})
}

// TestFQLStableSweepPostgres re-runs them against Postgres, where the
// ordering and the keyset predicate are SQL.
func TestFQLStableSweepPostgres(t *testing.T) {
	pool := openTestDB(t)
	defer func() { _ = pool.Close() }()

	svc := flexitype.New(pool)
	if err := svc.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runStableSweepParity(t, "postgres", func() *flexitype.Service {
		truncateAll(t, pool)
		return svc
	})
}
