package testdb

import (
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"
)

// TestTruncateFailsFastOnAHeldLock covers the case Postgres never resolves for
// us.
//
// A worker holding a conflicting lock WITHOUT forming a cycle is not a
// deadlock, so nothing intervenes and TRUNCATE waits for as long as the lock
// is held. The package then dies on the CI -timeout with a goroutine dump,
// which says nothing about why. A lock timeout turns that into a bounded,
// named failure that names the table.
func TestTruncateFailsFastOnAHeldLock(t *testing.T) {
	pool := Open(t, "trunclock")
	if _, err := pool.Exec(`CREATE TABLE IF NOT EXISTS flexitype_lockprobe (id int)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DROP TABLE IF EXISTS flexitype_lockprobe`) })

	Convey("Given another session holding a conflicting lock", t, func() {
		holder, err := pool.Beginx()
		So(err, ShouldBeNil)
		// ACCESS EXCLUSIVE conflicts with the truncate's own, and nothing here
		// ever asks for a lock the truncate holds — so there is no cycle for
		// Postgres to detect and break.
		_, err = holder.Exec(`LOCK TABLE flexitype_lockprobe IN ACCESS EXCLUSIVE MODE`)
		So(err, ShouldBeNil)
		defer func() { _ = holder.Rollback() }()

		Convey("When the truncate runs", func() {
			start := time.Now()
			terr := truncateOnce(pool, `TRUNCATE flexitype_lockprobe CASCADE`)
			elapsed := time.Since(start)

			Convey("Then it gives up rather than waiting", func() {
				So(terr, ShouldNotBeNil)
				var pqErr *pq.Error
				So(errors.As(terr, &pqErr), ShouldBeTrue)
				So(string(pqErr.Code), ShouldEqual, lockNotAvailable)
			})

			Convey("Then it gives up within the timeout, not on the test deadline", func() {
				// The bound is what makes the failure a report rather than a
				// hang; without it this call does not return at all.
				So(elapsed, ShouldBeLessThan, 30*time.Second)
			})
		})
	})
}

// TestTruncateOnceLeavesNoTransactionOpen pins the scoping of SET LOCAL.
func TestTruncateOnceLeavesNoTransactionOpen(t *testing.T) {
	pool := Open(t, "trunclock2")
	if _, err := pool.Exec(`CREATE TABLE IF NOT EXISTS flexitype_lockprobe2 (id int)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DROP TABLE IF EXISTS flexitype_lockprobe2`) })

	Convey("Given a truncate that succeeded", t, func() {
		_, err := pool.Exec(`INSERT INTO flexitype_lockprobe2 VALUES (1)`)
		So(err, ShouldBeNil)
		So(truncateOnce(pool, `TRUNCATE flexitype_lockprobe2 CASCADE`), ShouldBeNil)

		Convey("Then it committed", func() {
			var n int
			So(pool.Get(&n, `SELECT count(*) FROM flexitype_lockprobe2`), ShouldBeNil)
			So(n, ShouldEqual, 0)
		})

		Convey("Then the connection carries no leftover lock_timeout", func() {
			// SET LOCAL dies with its transaction; a plain SET would ride the
			// pooled connection into whatever ran next.
			var got string
			So(pool.Get(&got, `SHOW lock_timeout`), ShouldBeNil)
			So(got, ShouldEqual, "0")
		})
	})
}

// TestTruncateTablesRefusesAnythingButATableName guards the one thing a
// table-name argument must not become.
//
// The names reach the statement by concatenation, because TRUNCATE takes no
// placeholders. That is safe only while they are bare identifiers, so it is
// checked rather than assumed.
func TestTruncateTablesRefusesAnythingButATableName(t *testing.T) {
	Convey("Given a name that is not a plain identifier", t, func() {
		for _, bad := range []string{
			"flexitype_a; DROP TABLE flexitype_b",
			"public.flexitype_a",
			`"flexitype_a"`,
			"flexitype_a --",
			"",
		} {
			Convey("Then "+bad+" is refused, and no statement is built", func() {
				stmt, err := truncateStatement([]string{"flexitype_ok", bad}, true)
				So(err, ShouldNotBeNil)
				So(stmt, ShouldBeEmpty)
			})
		}

		Convey("Then ordinary names build a sorted statement", func() {
			stmt, err := truncateStatement([]string{"flexitype_b", "flexitype_a"}, true)
			So(err, ShouldBeNil)
			So(stmt, ShouldEqual, "TRUNCATE flexitype_a, flexitype_b CASCADE")

			plain, perr := truncateStatement([]string{"flexitype_b", "flexitype_a"}, false)
			So(perr, ShouldBeNil)
			So(plain, ShouldEqual, "TRUNCATE flexitype_a, flexitype_b")
		})
	})
}

// TestTruncateTablesSortsItsArgument pins the ordering that keeps two callers
// from taking the same locks in opposite orders.
func TestTruncateTablesSortsItsArgument(t *testing.T) {
	pool := Open(t, "truncsort")
	for _, name := range []string{"flexitype_sortprobe_b", "flexitype_sortprobe_a"} {
		if _, err := pool.Exec(`CREATE TABLE IF NOT EXISTS ` + name + ` (id int)`); err != nil {
			t.Fatalf("create probe table: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DROP TABLE IF EXISTS flexitype_sortprobe_a, flexitype_sortprobe_b`)
	})

	Convey("Given two callers naming the same tables in opposite orders", t, func() {
		_, err := pool.Exec(`INSERT INTO flexitype_sortprobe_a VALUES (1)`)
		So(err, ShouldBeNil)
		_, err = pool.Exec(`INSERT INTO flexitype_sortprobe_b VALUES (1)`)
		So(err, ShouldBeNil)

		Convey("When each truncates", func() {
			TruncateTablesCascade(t, pool, "flexitype_sortprobe_b", "flexitype_sortprobe_a")
			TruncateTablesCascade(t, pool, "flexitype_sortprobe_a", "flexitype_sortprobe_b")

			Convey("Then both succeed and the tables are empty", func() {
				var n int
				So(pool.Get(&n, `SELECT count(*) FROM flexitype_sortprobe_a`), ShouldBeNil)
				So(n, ShouldEqual, 0)
				So(pool.Get(&n, `SELECT count(*) FROM flexitype_sortprobe_b`), ShouldBeNil)
				So(n, ShouldEqual, 0)
			})
		})
	})
}
