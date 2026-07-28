package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/testdb"
)

// indexValidity reports whether the named index exists and whether Postgres
// considers it usable.
func indexValidity(t *testing.T, pool *sqlx.DB, name string) (exists, valid bool) {
	t.Helper()
	var row struct {
		Valid bool `db:"indisvalid"`
	}
	err := pool.Get(&row, `SELECT i.indisvalid FROM pg_index i
		  JOIN pg_class c ON c.oid = i.indexrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relname = $1 AND n.nspname = current_schema()`, name)
	if err != nil {
		return false, false
	}
	return true, row.Valid
}

// leaveInvalidIndex builds an invalid index the way production does: a
// CREATE INDEX CONCURRENTLY interrupted part-way. An open transaction on the
// table makes the build wait, and a statement timeout then cancels it.
func leaveInvalidIndex(t *testing.T, pool *sqlx.DB, table, index string) {
	t.Helper()
	blocker, err := pool.Beginx()
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.Exec(`INSERT INTO ` + table + ` (a) VALUES (99)`); err != nil {
		t.Fatalf("blocker insert: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, cerr := pool.Connx(context.Background())
		if cerr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(context.Background(), `SET statement_timeout = '1s'`); err != nil {
			return
		}
		// Cancelled by the timeout while it waits for the blocker: the index
		// is left behind, INVALID.
		_, _ = conn.ExecContext(context.Background(),
			`CREATE INDEX CONCURRENTLY IF NOT EXISTS `+index+` ON `+table+` (a)`)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out building the invalid index")
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("commit blocker: %v", err)
	}
}

// TestReapInvalidIndexIntegration proves a replayed no-transaction migration
// rebuilds an index a previous attempt left INVALID.
//
// A failed CREATE INDEX CONCURRENTLY leaves an invalid index. The name then
// exists, so `IF NOT EXISTS` skipped the rebuild for ever — and migration
// 000028 also drops the index it replaces, so the replay removed the working
// index and left only the invalid one. Every by-entity revision read then
// sequentially scanned the table, permanently, while the schema version read
// as current. The trigger is an ordinary pod lifecycle event during a deploy.
func TestReapInvalidIndexIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres")
	defer func() { _ = pool.Close() }()
	ctx := context.Background()

	Convey("Given an index a previous attempt left invalid", t, func() {
		pool.MustExec(`DROP TABLE IF EXISTS reap_probe`)
		pool.MustExec(`CREATE TABLE reap_probe (a int)`)
		pool.MustExec(`INSERT INTO reap_probe (a) SELECT g FROM generate_series(1, 200) g`)
		leaveInvalidIndex(t, pool, "reap_probe", "reap_probe_a_idx")

		exists, valid := indexValidity(t, pool, "reap_probe_a_idx")
		So(exists, ShouldBeTrue)
		So(valid, ShouldBeFalse)

		m := migration{
			name:    "000999_reap_probe.up.sql",
			version: 999, //nolint:mnd // a version number outside the shipped range
			body: "CREATE INDEX CONCURRENTLY IF NOT EXISTS reap_probe_a_idx ON reap_probe (a);\n" +
				"DROP INDEX CONCURRENTLY IF EXISTS reap_probe_old;\n",
		}

		Convey("When the migration is replayed", func() {
			So(reapInvalidIndexes(ctx, pool, m), ShouldBeNil)

			Convey("Then the invalid index is reaped, so the rebuild is not skipped", func() {
				exists, _ := indexValidity(t, pool, "reap_probe_a_idx")
				So(exists, ShouldBeFalse)

				pool.MustExec(`CREATE INDEX CONCURRENTLY IF NOT EXISTS reap_probe_a_idx ON reap_probe (a)`)
				exists, valid := indexValidity(t, pool, "reap_probe_a_idx")
				So(exists, ShouldBeTrue)
				So(valid, ShouldBeTrue)
			})
		})

		Convey("When the index is valid", func() {
			pool.MustExec(`DROP INDEX IF EXISTS reap_probe_a_idx`)
			pool.MustExec(`CREATE INDEX reap_probe_a_idx ON reap_probe (a)`)
			So(reapInvalidIndexes(ctx, pool, m), ShouldBeNil)

			Convey("Then it is left alone: reaping touches only invalid indexes", func() {
				exists, valid := indexValidity(t, pool, "reap_probe_a_idx")
				So(exists, ShouldBeTrue)
				So(valid, ShouldBeTrue)
			})
		})

		Reset(func() {
			pool.MustExec(`DROP TABLE IF EXISTS reap_probe`)
		})
	})
}

// TestConcurrentIndexNamesMatchesEveryForm pins the statement shapes the reap
// has to recognise, including the ones in migrations 000018, 000021 and
// 000025 rather than only the one that exposed the hazard.
func TestConcurrentIndexNamesMatchesEveryForm(t *testing.T) {
	Convey("Given the concurrent-index statement forms the migrations use", t, func() {
		names := func(body string) []string {
			var out []string
			for _, m := range concurrentIndexNames.FindAllStringSubmatch(body, -1) {
				out = append(out, m[1])
			}
			return out
		}

		Convey("Then each form yields its index name", func() {
			So(names("CREATE INDEX CONCURRENTLY IF NOT EXISTS a_idx ON t (c);"),
				ShouldResemble, []string{"a_idx"})
			So(names("CREATE INDEX CONCURRENTLY b_idx ON t (c);"),
				ShouldResemble, []string{"b_idx"})
			So(names("CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS c_idx ON t (c);"),
				ShouldResemble, []string{"c_idx"})
			So(names("create index\n  concurrently\n  if not exists d_idx on t (c);"),
				ShouldResemble, []string{"d_idx"})
		})

		Convey("Then a non-concurrent create is not matched: it is transactional", func() {
			So(names("CREATE INDEX e_idx ON t (c);"), ShouldBeEmpty)
		})

		Convey("Then every shipped no-transaction migration is covered", func() {
			files, err := upMigrations()
			So(err, ShouldBeNil)
			found := 0
			for _, name := range files {
				body, rerr := migrationsFS.ReadFile("migrations/" + name)
				So(rerr, ShouldBeNil)
				d, derr := parseDirectives(name, string(body))
				So(derr, ShouldBeNil)
				if !d.NoTransaction {
					continue
				}
				found += len(names(string(body)))
			}
			// 000018, 000021, 000025 and 000028 build indexes concurrently.
			So(found, ShouldBeGreaterThanOrEqualTo, 4)
		})
	})
}
