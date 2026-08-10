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
			//
			// The count is scoped to an IDLE backend of this database, which
			// is that failure exactly. Counting every advisory lock in the
			// database instead made this assertion fail on a lock it does not
			// own: the media-blob GC takes pg_advisory_xact_lock per object
			// key and the relay takes one for outbox expansion, so a
			// concurrent transaction elsewhere in the suite failed a
			// migration test. A transaction-scoped lock cannot survive on an
			// idle backend, so a lock counted here is a stranded one.
			var held int
			So(pool.Get(&held, `
				SELECT count(*) FROM pg_locks l
				  JOIN pg_stat_activity a ON a.pid = l.pid
				 WHERE l.locktype = 'advisory'
				   AND a.datname = current_database()
				   AND a.state = 'idle'`), ShouldBeNil)
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

	// #603.4: the DOWN path never renewed between reverts, so a down-run past
	// the fifteen-minute TTL could be taken over while it was still reverting.
	// It renews at every version boundary now, WITHOUT the between-statement
	// rate limit: a revert is rare and coarse, so a round trip per version is
	// cheap and a takeover is caught at once rather than a minute later.
	Convey("Given a lease this runner holds and a revert about to start", t, func() {
		freeLease()
		l := &migrateLease{q: pool, holder: "me", now: time.Now}
		ok, err := l.tryAcquire(ctx)
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)

		Convey("When it renews at a version boundary", func() {
			pool.MustExec(`UPDATE flexitype_schema_lock SET expires_at = now() + interval '1 second' WHERE id = 1`)
			rerr := l.renewNow(ctx)

			Convey("Then the expiry moves out, unlike the rate-limited renew", func() {
				// renew would be a no-op here: it was acquired moments ago.
				So(rerr, ShouldBeNil)
				var seconds float64
				So(pool.Get(&seconds,
					`SELECT EXTRACT(EPOCH FROM (expires_at - now())) FROM flexitype_schema_lock WHERE id = 1`),
					ShouldBeNil)
				So(seconds, ShouldBeGreaterThan, 60)
			})
		})

		Convey("When another runner took the lease over first", func() {
			pool.MustExec(`UPDATE flexitype_schema_lock SET holder = 'someone-else' WHERE id = 1`)
			rerr := l.renewNow(ctx)

			Convey("Then the next version is not reverted underneath them", func() {
				So(rerr, ShouldNotBeNil)
				So(rerr.Error(), ShouldContainSubstring, "lost the migration lease")
			})
		})

		Reset(func() { freeLease() })
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

		// The commonest reason Migrate returns is that the caller's context
		// ended, and the release ran on that same context — a no-op, which
		// stranded the lease for the full TTL while the process was alive
		// and able to free it.
		Convey("When it is released on a context that has already been cancelled", func() {
			dead, cancel := context.WithCancel(ctx)
			cancel()
			l.release(dead)

			Convey("Then the row is still freed", func() {
				var holder string
				So(pool.Get(&holder, `SELECT holder FROM flexitype_schema_lock WHERE id = 1`), ShouldBeNil)
				So(holder, ShouldEqual, "")
			})
		})

		Reset(func() { freeLease() })
	})

	// A runner that dies holding the lease leaves a row with a live
	// expires_at that nobody renews. With leaseWait below leaseTTL, every
	// replica booting in that window gave up BEFORE the lease could expire —
	// inside a container startup path, so the orchestrator restarted it and
	// it repeated, and a whole generation could not boot.
	Convey("Given the lease timings", t, func() {
		Convey("Then a waiter outlasts an abandoned lease", func() {
			So(leaseWait, ShouldBeGreaterThan, leaseTTL)
		})
	})

	Convey("Given a lease abandoned by a dead runner", t, func() {
		freeLease()
		pool.MustExec(
			`UPDATE flexitype_schema_lock
			    SET holder = 'dead-runner', acquired_at = now(), expires_at = now() + interval '15 minutes'
			  WHERE id = 1`)

		Convey("When a survivor gives up waiting", func() {
			l := &migrateLease{q: pool, holder: "me", now: time.Now}
			holder, expires := l.currentHolder(ctx)

			Convey("Then the blocker and its expiry are named, so an operator can correlate it", func() {
				So(holder, ShouldEqual, "dead-runner")
				So(expires, ShouldNotEqual, "unknown")
				So(expires, ShouldNotBeEmpty)
			})
		})

		Reset(func() { freeLease() })
	})
}

// TestMigrateFallbackIntegration covers the single-transaction fallback, the
// path a transactor that cannot pin a connection takes.
//
// It is the one place a session-scoped advisory lock is still correct:
// pg_advisory_xact_lock is released by the transaction that took it.
func TestMigrateFallbackIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_fallback")
	ctx := context.Background()
	if err := Migrate(ctx, db.NewTransactor(pool)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	Convey("Given a fully migrated schema", t, func() {
		Convey("When the fallback path runs", func() {
			err := Migrate(ctx, noPinTransactor{db.NewTransactor(pool)})

			Convey("Then it finds nothing to do and succeeds", func() {
				So(err, ShouldBeNil)
			})
		})
	})

	Convey("Given one transactional migration recorded as unapplied", t, func() {
		// 000027 is transactional and idempotent (DROP/CREATE INDEX IF [NOT]
		// EXISTS), so re-applying it is safe and exercises the fallback's
		// apply-and-record loop.
		pool.MustExec(`DELETE FROM flexitype_schema_migrations WHERE version = 27`)

		Convey("When the fallback path runs", func() {
			err := Migrate(ctx, noPinTransactor{db.NewTransactor(pool)})

			Convey("Then it applies and records it in one transaction", func() {
				So(err, ShouldBeNil)
				var applied int
				So(pool.Get(&applied,
					`SELECT count(*) FROM flexitype_schema_migrations WHERE version = 27`), ShouldBeNil)
				So(applied, ShouldEqual, 1)
			})
		})

		Reset(func() {
			pool.MustExec(
				`INSERT INTO flexitype_schema_migrations (version) VALUES (27) ON CONFLICT DO NOTHING`)
		})
	})
}

// TestExtensionGatedMigration covers the requires-extension directive (#611).
//
// The pg_trgm indexes could not be built concurrently while the build had to
// be conditional in SQL: the only conditional form is a DO block, a DO block
// is a transaction, and CREATE INDEX CONCURRENTLY refuses to run inside one.
// So 000004 and 000021 built them plainly, on the largest table in the
// database, holding a lock that conflicts with every write for the whole
// build. The condition now sits in the runner instead.
func TestExtensionGatedMigration(t *testing.T) {
	pool := testdb.Open(t, "postgres_extension_gate")
	ctx := context.Background()

	Convey("Given a migration that declares an extension it needs", t, func() {
		gated, err := parseDirectives("000041_trgm.up.sql",
			"-- +flexitype:no-transaction\n-- +flexitype:requires-extension pg_trgm\nSELECT 1;")
		So(err, ShouldBeNil)

		Convey("Then the directive is read, alongside the others", func() {
			So(gated.RequiresExtension, ShouldEqual, "pg_trgm")
			So(gated.NoTransaction, ShouldBeTrue)
		})

		Convey("When the extension is installed", func() {
			// plpgsql is installed in every database by default, so this
			// branch is exercised on every machine.
			m := migration{name: "test.up.sql", directives: migrationDirectives{RequiresExtension: "plpgsql"}}
			skip, serr := extensionMissing(ctx, pool, m)

			Convey("Then the file is applied like any other", func() {
				So(serr, ShouldBeNil)
				So(skip, ShouldBeFalse)
			})
		})

		Convey("When the extension is absent", func() {
			m := migration{name: "test.up.sql", directives: migrationDirectives{RequiresExtension: "no_such_extension"}}
			skip, serr := extensionMissing(ctx, pool, m)

			Convey("Then the file is skipped rather than failed", func() {
				// Failing would wedge every later migration on a managed
				// provider that will not install the extension.
				So(serr, ShouldBeNil)
				So(skip, ShouldBeTrue)
			})
		})

		Convey("When a file declares nothing", func() {
			skip, serr := extensionMissing(ctx, pool, migration{name: "test.up.sql"})

			Convey("Then it costs no catalogue query", func() {
				So(serr, ShouldBeNil)
				So(skip, ShouldBeFalse)
			})
		})
	})

	Convey("Given a directive line that names anything but one extension", t, func() {
		for _, bad := range []string{
			"-- +flexitype:requires-extension",
			"-- +flexitype:requires-extension pg_trgm, pgcrypto",
			"-- +flexitype:requires-extension \"pg_trgm\"",
		} {
			_, err := parseDirectives("bad.up.sql", bad)

			Convey("Then "+bad+" is refused rather than skipping the file for ever", func() {
				// A name nobody has would read as "extension missing" and
				// silently skip the migration on every boot.
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires-extension")
			})
		}
	})

	Convey("Given a fully migrated database", t, func() {
		So(Migrate(ctx, db.NewTransactor(pool)), ShouldBeNil)

		var installed bool
		So(pool.Get(&installed,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`), ShouldBeNil)

		var recorded bool
		So(pool.Get(&recorded,
			`SELECT EXISTS (SELECT 1 FROM flexitype_schema_migrations WHERE version = 41)`), ShouldBeNil)

		Convey("Then the gated migration is recorded exactly when it could run", func() {
			// Recording a skipped file would mean a database that installs
			// pg_trgm later never builds the indexes, with nothing to say so.
			So(recorded, ShouldEqual, installed)
		})

		if installed {
			Convey("Then both trigram indexes exist and are VALID", func() {
				var valid int
				So(pool.Get(&valid, `
					SELECT count(*) FROM pg_class c
					  JOIN pg_index i ON i.indexrelid = c.oid
					  JOIN pg_namespace n ON n.oid = c.relnamespace
					 WHERE n.nspname = current_schema()
					   AND i.indisvalid
					   AND c.relname IN ('idx_flexitype_attribute_value_trgm',
					                     'idx_flexitype_attribute_value_trgm_lower')`), ShouldBeNil)
				So(valid, ShouldEqual, 2)
			})
		}

		Convey("Then the scope index split out of 000014 is there too", func() {
			var valid bool
			So(pool.Get(&valid, `
				SELECT EXISTS (
				    SELECT 1 FROM pg_class c
				      JOIN pg_index i ON i.indexrelid = c.oid
				      JOIN pg_namespace n ON n.oid = c.relnamespace
				     WHERE n.nspname = current_schema()
				       AND i.indisvalid
				       AND c.relname = 'idx_flexitype_attribute_value_scope')`), ShouldBeNil)
			So(valid, ShouldBeTrue)
		})

		Convey("When Migrate runs again", func() {
			rerr := Migrate(ctx, db.NewTransactor(pool))

			Convey("Then a gated file that already applied is not re-run", func() {
				So(rerr, ShouldBeNil)
			})
		})

		Convey("When a database already carries the index from the file it was split OUT of", func() {
			// The upgrade shape: 000004 or 000021 built these indexes, so they
			// exist and 000041 has never run. The split must be a no-op there,
			// not a duplicate build on the largest table in the database.
			if installed {
				pool.MustExec(`DELETE FROM flexitype_schema_migrations WHERE version IN (41, 42)`)
				rerr := Migrate(ctx, db.NewTransactor(pool))

				Convey("Then it applies cleanly and the indexes are untouched", func() {
					So(rerr, ShouldBeNil)
					var valid int
					So(pool.Get(&valid, `
						SELECT count(*) FROM pg_class c
						  JOIN pg_index i ON i.indexrelid = c.oid
						  JOIN pg_namespace n ON n.oid = c.relnamespace
						 WHERE n.nspname = current_schema()
						   AND i.indisvalid
						   AND c.relname IN ('idx_flexitype_attribute_value_trgm',
						                     'idx_flexitype_attribute_value_trgm_lower',
						                     'idx_flexitype_attribute_value_scope')`), ShouldBeNil)
					So(valid, ShouldEqual, 3)
				})
			}
		})

		Convey("When the split migrations are reverted and re-applied", func() {
			// A down file that still named an index its up file no longer
			// creates would leave the schema short of an index nothing rebuilds.
			So(MigrateDown(ctx, db.NewTransactor(pool), 40), ShouldBeNil)

			var present int
			So(pool.Get(&present, `
				SELECT count(*) FROM pg_class c
				  JOIN pg_namespace n ON n.oid = c.relnamespace
				 WHERE n.nspname = current_schema()
				   AND c.relname IN ('idx_flexitype_attribute_value_trgm',
				                     'idx_flexitype_attribute_value_trgm_lower',
				                     'idx_flexitype_attribute_value_scope')`), ShouldBeNil)

			uerr := Migrate(ctx, db.NewTransactor(pool))

			Convey("Then the revert drops them and the re-apply rebuilds them", func() {
				So(present, ShouldEqual, 0)
				So(uerr, ShouldBeNil)
				var valid int
				So(pool.Get(&valid, `
					SELECT count(*) FROM pg_class c
					  JOIN pg_index i ON i.indexrelid = c.oid
					  JOIN pg_namespace n ON n.oid = c.relnamespace
					 WHERE n.nspname = current_schema()
					   AND i.indisvalid
					   AND c.relname IN ('idx_flexitype_attribute_value_trgm',
					                     'idx_flexitype_attribute_value_trgm_lower',
					                     'idx_flexitype_attribute_value_scope')`), ShouldBeNil)
				So(valid, ShouldEqual, map[bool]int{true: 3, false: 1}[installed])
			})
		})
	})
}
