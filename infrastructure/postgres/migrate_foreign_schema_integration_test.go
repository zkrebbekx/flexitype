package postgres

import (
	"context"
	"testing"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestForeignSchemaInvalidIndexIntegration proves that migration 000030
// replays cleanly in one schema while ANOTHER schema of the same database
// holds an INVALID index with the same name.
//
// Multi-schema-per-database is supported: separate deployments share one
// database, and this test suite gives each package its own schema. Migration
// 000030 used to carry a DO-block guard that matched pg_class.relname without
// a pg_namespace join. A foreign schema's invalid index made the guard fire
// here, and the guard's unqualified DROP INDEX then resolved through
// search_path: it failed with 42704 when this schema had no such index — a
// permanent boot loop — or it dropped this schema's VALID index under an
// ACCESS EXCLUSIVE lock. The runner's reapInvalidIndexes covers the intended
// case and is scoped to current_schema(), so the file no longer carries a
// guard of its own.
func TestForeignSchemaInvalidIndexIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_foreign")
	ctx := context.Background()
	if err := Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const foreign = "flexitype_test_foreign_a476"
	const index = "idx_flexitype_attribute_value_uniq_float"

	// foreignIndexState reads the namesake in the FOREIGN schema only.
	foreignIndexState := func() (exists, valid bool) {
		var row struct {
			Valid bool `db:"indisvalid"`
		}
		err := pool.Get(&row, `SELECT i.indisvalid FROM pg_index i
			  JOIN pg_class c ON c.oid = i.indexrelid
			  JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE c.relname = $1 AND n.nspname = $2`, index, foreign)
		if err != nil {
			return false, false
		}
		return true, row.Valid
	}

	Convey("Given a foreign schema holding an INVALID index named "+index, t, func() {
		pool.MustExec(`DROP SCHEMA IF EXISTS ` + foreign + ` CASCADE`)
		pool.MustExec(`CREATE SCHEMA ` + foreign)
		pool.MustExec(`CREATE TABLE ` + foreign + `.probe (a float8)`)
		pool.MustExec(`CREATE INDEX ` + index + ` ON ` + foreign + `.probe (a)`)
		// Fabricate the aftermath of an interrupted CREATE INDEX CONCURRENTLY
		// in the foreign schema: mark the index INVALID in the catalogue.
		pool.MustExec(`UPDATE pg_index SET indisvalid = false
			WHERE indexrelid = '` + foreign + `.` + index + `'::regclass`)
		exists, valid := foreignIndexState()
		So(exists, ShouldBeTrue)
		So(valid, ShouldBeFalse)

		Convey("When migration 000030 replays in this schema", func() {
			// The replay of an interrupted no-transaction file: the version
			// row is absent and this schema's index does not exist yet.
			pool.MustExec(`DELETE FROM flexitype_schema_migrations WHERE version = 30`)
			pool.MustExec(`DROP INDEX IF EXISTS ` + index)
			err := Migrate(ctx, db.NewTransactor(pool))

			Convey("Then the replay succeeds instead of failing on the foreign index", func() {
				So(err, ShouldBeNil)
			})

			Convey("Then this schema's index is rebuilt and valid", func() {
				So(err, ShouldBeNil)
				exists, valid := indexValidity(t, pool, index)
				So(exists, ShouldBeTrue)
				So(valid, ShouldBeTrue)
			})

			Convey("Then the foreign schema's invalid index is untouched", func() {
				So(err, ShouldBeNil)
				exists, valid := foreignIndexState()
				So(exists, ShouldBeTrue)
				So(valid, ShouldBeFalse)
			})
		})

		Reset(func() {
			pool.MustExec(`DROP SCHEMA IF EXISTS ` + foreign + ` CASCADE`)
		})
	})
}
