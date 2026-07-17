package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// seedSummarySchema creates the type + attribute a value row needs to satisfy
// the foreign keys, so the trigger-maintained projection can be exercised with
// raw value writes.
func seedSummarySchema(t *testing.T, pool *sqlx.DB, typeID, attrID string) {
	t.Helper()
	now := time.Now().UTC()
	pool.MustExec(
		`INSERT INTO flexitype_type_definition (id, tenant_id, internal_name, display_name, created_at, updated_at)
		 VALUES ($1, 'default', 'widget', 'Widget', $2, $2)`, typeID, now)
	pool.MustExec(
		`INSERT INTO flexitype_attribute_definition
		     (id, tenant_id, type_definition_id, internal_name, display_name, data_type, created_at, updated_at)
		 VALUES ($1, 'default', $2, 'size', 'Size', 'integer', $3, $3)`, attrID, typeID, now)
}

// insertLiveValue writes one live value row (exercising the AFTER INSERT
// trigger) and returns its id so later scenarios can update it directly.
func insertLiveValue(t *testing.T, pool *sqlx.DB, typeID, attrID, entity string, n int, updatedAt time.Time) string {
	t.Helper()
	id := ulid.New().String()
	pool.MustExec(
		`INSERT INTO flexitype_attribute_value
		     (id, tenant_id, type_definition_id, attribute_definition_id, entity_id, data_type,
		      value_int, definition_version, created_at, updated_at)
		 VALUES ($1, 'default', $2, $3, $4, 'integer', $5, 1, $6, $6)`,
		id, typeID, attrID, entity, n, updatedAt)
	return id
}

func summaryEntityIDs(sums []domainvalue.EntitySummary) []string {
	out := make([]string, len(sums))
	for i, s := range sums {
		out[i] = s.EntityID.String()
	}
	return out
}

// TestEntitySummaryProjectionIntegration proves the trigger-maintained
// flexitype_entity_summary projection (migration 000019) keeps ListEntities
// correct across inserts, archival and updates, and that the read path pages
// off the projection.
func TestEntitySummaryProjectionIntegration(t *testing.T) {
	pool := openIntegrationDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	transactor := db.NewTransactor(pool)

	if err := postgres.Migrate(ctx, transactor); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tenant := valueobjects.DefaultTenant
	typeID := valueobjects.NewTypeDefinitionID()
	attrID := ulid.New().String()
	typeIDs := []valueobjects.TypeDefinitionID{typeID}
	repo := postgres.NewAttributeValueRepository(pool)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	Convey("Given three entities with differing recency and value counts", t, func() {
		// goconvey re-runs this setup per leaf assertion; reset every table the
		// projection and its foreign keys touch (TRUNCATE does not fire the row
		// trigger, so the summary is cleared explicitly here).
		pool.MustExec(`TRUNCATE flexitype_attribute_value, flexitype_entity_summary,
			flexitype_attribute_definition, flexitype_type_definition CASCADE`)
		seedSummarySchema(t, pool, typeID.String(), attrID)

		// eA: 3 values, newest at +12m. eB: 2 values, newest at +21m.
		// eC: 1 value at +30m. Newest-first order is therefore eC, eB, eA.
		eAValues := []string{
			insertLiveValue(t, pool, typeID.String(), attrID, "eA", 1, base.Add(10*time.Minute)),
			insertLiveValue(t, pool, typeID.String(), attrID, "eA", 2, base.Add(11*time.Minute)),
			insertLiveValue(t, pool, typeID.String(), attrID, "eA", 3, base.Add(12*time.Minute)),
		}
		insertLiveValue(t, pool, typeID.String(), attrID, "eB", 4, base.Add(20*time.Minute))
		insertLiveValue(t, pool, typeID.String(), attrID, "eB", 5, base.Add(21*time.Minute))
		insertLiveValue(t, pool, typeID.String(), attrID, "eC", 6, base.Add(30*time.Minute))

		Convey("When the first page is listed newest-first with a total", func() {
			items, total, err := repo.ListEntities(ctx, tenant, typeIDs, db.Page{Limit: 10, WantTotal: true})
			So(err, ShouldBeNil)

			Convey("Then rows are ordered by last change with correct counts and total", func() {
				So(summaryEntityIDs(items), ShouldResemble, []string{"eC", "eB", "eA"})
				counts := map[string]int{}
				for _, s := range items {
					counts[s.EntityID.String()] = s.ValueCount
					So(s.TypeDefinitionID.String(), ShouldEqual, typeID.String())
				}
				So(counts["eA"], ShouldEqual, 3)
				So(counts["eB"], ShouldEqual, 2)
				So(counts["eC"], ShouldEqual, 1)
				So(total, ShouldEqual, 3)
			})
		})

		Convey("When paging one entity at a time via the keyset cursor", func() {
			// The repository over-fetches (limit+1); the first item of each page
			// is the next entity, and its (last_updated_at, entity_id) forms the
			// cursor for the following page — mirroring the interactor.
			walk := func(cursor string) (string, string) {
				page := db.Page{Limit: 1, Cursor: cursor}
				items, _, err := repo.ListEntities(ctx, tenant, typeIDs, page)
				So(err, ShouldBeNil)
				So(len(items), ShouldBeGreaterThan, 0)
				top := items[0]
				next := db.EncodeKeyset(top.LastUpdatedAt.UTC().Format(time.RFC3339Nano), top.EntityID.String())
				return top.EntityID.String(), next
			}

			Convey("Then the cursor walks eC, eB, eA in order", func() {
				first, c1 := walk("")
				So(first, ShouldEqual, "eC")
				second, c2 := walk(c1)
				So(second, ShouldEqual, "eB")
				third, _ := walk(c2)
				So(third, ShouldEqual, "eA")
			})
		})

		Convey("When every value of an entity is archived", func() {
			pool.MustExec(`UPDATE flexitype_attribute_value SET archived_at = now() WHERE entity_id = 'eC'`)

			Convey("Then that entity drops out of the listing and the total falls", func() {
				items, total, err := repo.ListEntities(ctx, tenant, typeIDs, db.Page{Limit: 10, WantTotal: true})
				So(err, ShouldBeNil)
				So(summaryEntityIDs(items), ShouldResemble, []string{"eB", "eA"})
				So(total, ShouldEqual, 2)

				var summaryRows int
				So(pool.Get(&summaryRows,
					`SELECT count(*) FROM flexitype_entity_summary WHERE entity_id = 'eC'`), ShouldBeNil)
				So(summaryRows, ShouldEqual, 0)
			})
		})

		Convey("When a value is updated to a newer timestamp", func() {
			// Bump one of eA's values past eC's recency; the trigger refreshes
			// eA's summary so it becomes the most-recently-changed entity.
			pool.MustExec(`UPDATE flexitype_attribute_value SET updated_at = $1 WHERE id = $2`,
				base.Add(40*time.Minute), eAValues[0])

			Convey("Then that entity moves to the front of the listing", func() {
				items, _, err := repo.ListEntities(ctx, tenant, typeIDs, db.Page{Limit: 10})
				So(err, ShouldBeNil)
				So(summaryEntityIDs(items), ShouldResemble, []string{"eA", "eC", "eB"})
			})
		})

		Convey("When every value of an entity is hard-deleted", func() {
			pool.MustExec(`DELETE FROM flexitype_attribute_value WHERE entity_id = 'eB'`)

			Convey("Then the entity's summary row is removed and it leaves the listing", func() {
				items, total, err := repo.ListEntities(ctx, tenant, typeIDs, db.Page{Limit: 10, WantTotal: true})
				So(err, ShouldBeNil)
				So(summaryEntityIDs(items), ShouldResemble, []string{"eC", "eA"})
				So(total, ShouldEqual, 2)
			})
		})
	})
}
