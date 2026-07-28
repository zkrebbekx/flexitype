package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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

// TestConcurrentMigrateIntegration is the rolling-deploy shape: several
// replicas call Migrate at startup against the same empty schema.
//
// The runner used to take a SESSION-scoped advisory lock and hold it across
// the per-migration transactions. Through a transaction-mode pooler those
// transactions run on other backends, so the lock serialized nothing: five of
// six concurrent runs failed on raw DDL collisions ("relation already exists",
// or a unique violation on a catalogue index), and one lock was left held on
// an idle pooled backend where every later migration blocked on it forever.
//
// Correctness no longer rests on any lock: each migration claims its version
// row inside the transaction that applies it, so the database serializes the
// runners for exactly as long as the apply takes.
func TestConcurrentMigrateIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_concurrent")
	ctx := context.Background()

	Convey("Given six runners migrating one empty schema at the same time", t, func() {
		const runners = 6
		errs := make([]error, runners)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < runners; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = Migrate(ctx, db.NewTransactor(pool))
			}(i)
		}
		close(start)
		wg.Wait()

		Convey("Then every one of them succeeds", func() {
			for i, err := range errs {
				So(fmt.Sprintf("runner %d: %v", i, err), ShouldEqual, fmt.Sprintf("runner %d: %v", i, error(nil)))
			}
		})

		Convey("Then the schema is at the version the binary carries, applied once", func() {
			known, err := KnownSchemaVersion()
			So(err, ShouldBeNil)
			var applied, distinct int
			So(pool.Get(&applied, `SELECT count(*) FROM flexitype_schema_migrations`), ShouldBeNil)
			So(pool.Get(&distinct, `SELECT count(DISTINCT version) FROM flexitype_schema_migrations`), ShouldBeNil)
			So(applied, ShouldEqual, distinct)
			So(applied, ShouldEqual, known)
		})

		Convey("Then no session-scoped advisory lock is left held", func() {
			// The stranded lock was the part an operator could not recover
			// from: it sat on an idle pooled backend and blocked every later
			// migration with no diagnostic.
			var held int
			So(pool.Get(&held,
				`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'`), ShouldBeNil)
			So(held, ShouldEqual, 0)
		})

		Convey("Then the lease row is free, so the next run does not wait", func() {
			var holder string
			So(pool.Get(&holder, `SELECT holder FROM flexitype_schema_lock WHERE id = 1`), ShouldBeNil)
			So(holder, ShouldEqual, "")
		})
	})
}

// TestMigrationLeaseIntegration covers the lease's own behaviour: it excludes
// a second runner, expires rather than stranding, and refuses to continue if
// it is lost.
func TestMigrationLeaseIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_lease")
	ctx := context.Background()
	if err := Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Each leaf below manipulates the lock row directly, and goconvey re-runs
	// the enclosing closure once per leaf, so every one starts from a free
	// lease rather than inheriting the previous leaf's.
	freeLease := func() {
		pool.MustExec(`UPDATE flexitype_schema_lock SET holder = '', expires_at = now() WHERE id = 1`)
	}

	Convey("Given a lease held by a live runner", t, func() {
		freeLease()
		pool.MustExec(
			`UPDATE flexitype_schema_lock
			    SET holder = 'other-runner', acquired_at = now(), expires_at = now() + interval '10 minutes'
			  WHERE id = 1`)

		Convey("When another runner tries to take it", func() {
			l := &migrateLease{q: pool, holder: "me", now: time.Now}
			ok, err := l.tryAcquire(ctx)

			Convey("Then it is refused while the holder is live", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeFalse)
			})
		})

		Convey("When the holder's lease has expired", func() {
			// A runner that died mid-migration used to strand a session lock
			// that only a manual backend kill could clear. An expiry means
			// the fleet recovers on its own.
			pool.MustExec(`UPDATE flexitype_schema_lock SET expires_at = now() - interval '1 second' WHERE id = 1`)
			l := &migrateLease{q: pool, holder: "me", now: time.Now}
			ok, err := l.tryAcquire(ctx)

			Convey("Then the lease is taken over", func() {
				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
			})
		})
	})

	Convey("Given a runner whose lease was taken over mid-run", t, func() {
		freeLease()
		l := &migrateLease{q: pool, holder: "me", now: time.Now}
		ok, err := l.tryAcquire(ctx)
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)
		pool.MustExec(`UPDATE flexitype_schema_lock SET holder = 'someone-else' WHERE id = 1`)
		l.renewedAt = time.Now().Add(-2 * leaseRenewGap)

		Convey("When it renews before the next statement", func() {
			err := l.renew(ctx)

			Convey("Then it stops rather than applying DDL alongside the new holder", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "lost the migration lease")
			})
		})
	})

	Convey("Given a lease this runner holds", t, func() {
		freeLease()
		l := &migrateLease{q: pool, holder: "me", now: time.Now}
		ok, err := l.tryAcquire(ctx)
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)

		Convey("When renew is called well within the TTL", func() {
			err := l.renew(ctx)

			Convey("Then it is a no-op rather than a round trip per statement", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When it is released", func() {
			l.release(ctx)

			Convey("Then the row is free for the next runner", func() {
				var holder string
				So(pool.Get(&holder, `SELECT holder FROM flexitype_schema_lock WHERE id = 1`), ShouldBeNil)
				So(holder, ShouldEqual, "")
			})
		})

		Reset(func() { freeLease() })
	})
}
