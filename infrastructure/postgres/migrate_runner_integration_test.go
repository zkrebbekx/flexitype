package postgres

import (
	"context"
	"testing"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// noPinTransactor hides WithConn from a pool-level transactor, so Migrate
// takes the fallback path meant for a transactor that cannot pin a connection.
type noPinTransactor struct{ db.Transactor }

func TestMigrateRunnerIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_runner")
	ctx := context.Background()

	Convey("Given the embedded migration set", t, func() {
		known, err := KnownSchemaVersion()
		So(err, ShouldBeNil)

		Convey("Then the binary reports the highest version it carries", func() {
			So(known, ShouldBeGreaterThan, 0)
			names, err := upMigrations()
			So(err, ShouldBeNil)
			last, err := migrationVersion(names[len(names)-1])
			So(err, ShouldBeNil)
			So(known, ShouldEqual, last)
		})

		Convey("When the schema has not been created yet", func() {
			newer, err := UnknownSchemaVersions(ctx, pool)

			Convey("Then no drift is reported rather than an error", func() {
				So(err, ShouldBeNil)
				So(newer, ShouldBeEmpty)
			})
		})

		Convey("When the schema is migrated to the version this binary carries", func() {
			So(Migrate(ctx, db.NewTransactor(pool)), ShouldBeNil)

			Convey("Then the binary reports no drift", func() {
				newer, err := UnknownSchemaVersions(ctx, pool)
				So(err, ShouldBeNil)
				So(newer, ShouldBeEmpty)
			})

			Convey("Then a second run applies nothing and still succeeds", func() {
				So(Migrate(ctx, db.NewTransactor(pool)), ShouldBeNil)
				pending, err := pendingMigrations(ctx, pool)
				So(err, ShouldBeNil)
				So(pending, ShouldBeEmpty)
			})

			Convey("Then a schema carrying a version the binary lacks is reported as drift", func() {
				// This is what the previous generation of a rolling deploy sees
				// once a newer pod has migrated.
				_, err := pool.ExecContext(ctx,
					`INSERT INTO flexitype_schema_migrations (version) VALUES ($1)`, known+1)
				So(err, ShouldBeNil)
				defer func() {
					_, _ = pool.ExecContext(ctx,
						`DELETE FROM flexitype_schema_migrations WHERE version = $1`, known+1)
				}()

				newer, err := UnknownSchemaVersions(ctx, pool)
				So(err, ShouldBeNil)
				So(newer, ShouldResemble, []int{known + 1})
			})

			Convey("Then a transactor that cannot pin a connection is refused, not silently downgraded", func() {
				// Migration 000018 must run outside a transaction. Applying it
				// inside one is exactly the write-blocking form its author ruled
				// out, so the fallback path reports the conflict instead.
				_, err := pool.ExecContext(ctx,
					`DELETE FROM flexitype_schema_migrations WHERE version = 18`)
				So(err, ShouldBeNil)
				defer func() {
					_, _ = pool.ExecContext(ctx,
						`INSERT INTO flexitype_schema_migrations (version) VALUES (18) ON CONFLICT DO NOTHING`)
				}()

				err = Migrate(ctx, noPinTransactor{db.NewTransactor(pool)})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "no-transaction")
			})
		})
	})
}
