package postgres

import (
	"context"
	"strconv"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// openBackfillDB opens this package's scoped pool. It exists separately from
// the external suite's helper because this test lives inside the package, to
// reach the unexported backfill registry.
func openBackfillDB(t *testing.T) *sqlx.DB {
	t.Helper()
	return testdb.Open(t, "postgres_internal")
}

// TestEntitySummaryBackfillIntegration proves the projection converges without
// the whole-table aggregate that used to sit inside migration 000019's
// transaction.
//
// The property that matters is that correctness never depends on there being
// no writers: the trigger keeps new writes right from the moment it exists,
// and the backfill catches up history in bounded batches, resumably. So the
// test drops the projection to simulate pre-upgrade history, then checks that
// a batch at a time rebuilds it and that a re-run changes nothing.
func TestEntitySummaryBackfillIntegration(t *testing.T) {
	pool := openBackfillDB(t)
	defer func() { _ = pool.Close() }()
	ctx := context.Background()
	if err := Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given three entities whose value rows predate the projection", t, func() {
		testdb.TruncateTablesCascade(t, pool, "flexitype_attribute_value", "flexitype_attribute_definition", "flexitype_type_definition", "flexitype_entity_summary")

		typeID := ulid.New().String()
		pool.MustExec(`
			INSERT INTO flexitype_type_definition
			  (id, tenant_id, internal_name, display_name, created_at, updated_at)
			VALUES ($1, 'default', 'product', 'Product', now(), now())`, typeID)
		attrIDs := make([]string, 3)
		for i := range attrIDs {
			attrIDs[i] = ulid.New().String()
			pool.MustExec(`
				INSERT INTO flexitype_attribute_definition
				  (id, tenant_id, type_definition_id, internal_name, display_name, data_type, created_at, updated_at)
				VALUES ($1, 'default', $2, $3, $3, 'string', now(), now())`,
				attrIDs[i], typeID, "a"+strconv.Itoa(i))
		}

		// Insert value rows with the trigger disabled, which is exactly the
		// state a deployment is in for rows written before 000019 ran.
		pool.MustExec(`ALTER TABLE flexitype_attribute_value DISABLE TRIGGER USER`)
		for _, e := range []struct {
			entity string
			values int
		}{{"e1", 3}, {"e2", 1}, {"e3", 2}} {
			for i := 0; i < e.values; i++ {
				pool.MustExec(`
					INSERT INTO flexitype_attribute_value
					  (id, tenant_id, type_definition_id, attribute_definition_id, entity_id,
					   locale, channel, data_type, value_text, definition_version, created_at, updated_at)
					VALUES ($1, 'default', $2, $3, $4, '', '', 'string', 'v', 1, now(), now())`,
					ulid.New().String(), typeID, attrIDs[i], e.entity)
			}
		}
		pool.MustExec(`ALTER TABLE flexitype_attribute_value ENABLE TRIGGER USER`)
		pool.MustExec(`DELETE FROM flexitype_schema_backfill WHERE name = $1`, entitySummaryBackfill.name)

		count := func() int {
			var n int
			So(pool.Get(&n, `SELECT count(*) FROM flexitype_entity_summary`), ShouldBeNil)
			return n
		}
		So(count(), ShouldEqual, 0)

		Convey("When the backfill runs one entity at a time", func() {
			first, err := entitySummaryBackfill.step(ctx, pool, 1)
			So(err, ShouldBeNil)

			Convey("Then a bounded batch inserts only that batch", func() {
				So(first, ShouldEqual, 1)
				So(count(), ShouldEqual, 1)
			})

			Convey("Then running to exhaustion covers every entity", func() {
				for {
					n, err := entitySummaryBackfill.step(ctx, pool, 1)
					So(err, ShouldBeNil)
					if n == 0 {
						break
					}
				}
				So(count(), ShouldEqual, 3)

				var got int
				So(pool.Get(&got, `SELECT value_count FROM flexitype_entity_summary WHERE entity_id = 'e1'`), ShouldBeNil)
				So(got, ShouldEqual, 3)
				So(pool.Get(&got, `SELECT value_count FROM flexitype_entity_summary WHERE entity_id = 'e2'`), ShouldBeNil)
				So(got, ShouldEqual, 1)
			})
		})

		Convey("When the whole migrate path runs", func() {
			So(Migrate(ctx, db.NewTransactor(pool)), ShouldBeNil)

			Convey("Then the projection is complete", func() {
				So(count(), ShouldEqual, 3)
			})

			Convey("Then the step is recorded so it does not re-run", func() {
				var done bool
				So(pool.Get(&done, `SELECT EXISTS (SELECT 1 FROM flexitype_schema_backfill WHERE name = $1)`,
					entitySummaryBackfill.name), ShouldBeNil)
				So(done, ShouldBeTrue)
			})

			Convey("Then a second run is a no-op rather than a duplicate", func() {
				So(Migrate(ctx, db.NewTransactor(pool)), ShouldBeNil)
				So(count(), ShouldEqual, 3)
			})
		})

		Convey("When the backfill has already caught up", func() {
			for {
				n, err := entitySummaryBackfill.step(ctx, pool, 100)
				So(err, ShouldBeNil)
				if n == 0 {
					break
				}
			}

			Convey("Then a further step reports no work and changes nothing", func() {
				n, err := entitySummaryBackfill.step(ctx, pool, 100)
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 0)
				So(count(), ShouldEqual, 3)
			})
		})
	})
}
