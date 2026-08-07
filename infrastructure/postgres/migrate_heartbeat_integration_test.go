package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "github.com/lib/pq"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/internal/testdb"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// failingExecer stands in for a pool whose renewal queries fail transiently.
// Only GetContext is implemented, because a renewal is one GetContext call;
// the embedded nil interface panics on anything else, which would expose an
// unexpected call.
type failingExecer struct{ db.QueryExecer }

func (failingExecer) GetContext(context.Context, any, string, ...any) error {
	return errors.New("transient renewal failure")
}

// TestMigrationLeaseHeartbeatIntegration covers the mid-statement heartbeat:
// the lease is renewed WHILE a no-transaction statement runs, a lost lease
// cancels the statement, and a transient renewal failure does not.
//
// Before the heartbeat, renew ran only between statements, on the pinned
// connection the statement occupies. A CREATE INDEX CONCURRENTLY longer than
// the 15-minute TTL therefore lost the lease mid-build: the next replica took
// the lease, replayed the file, and reaped the in-flight index out from under
// the first builder — a rolling deploy that thrashed instead of finishing.
func TestMigrationLeaseHeartbeatIntegration(t *testing.T) {
	pool := testdb.Open(t, "postgres_heartbeat")
	ctx := context.Background()
	if err := ensureMigrationTables(ctx, pool); err != nil {
		t.Fatalf("ensure migration tables: %v", err)
	}
	freeLease := func() {
		pool.MustExec(`UPDATE flexitype_schema_lock SET holder = '', expires_at = now() WHERE id = 1`)
	}
	expiresAt := func() time.Time {
		var ts time.Time
		if err := pool.Get(&ts, `SELECT expires_at FROM flexitype_schema_lock WHERE id = 1`); err != nil {
			t.Fatalf("read expires_at: %v", err)
		}
		return ts
	}

	Convey("Given a held lease and a fast heartbeat", t, func() {
		freeLease()
		l := &migrateLease{
			q: pool, pool: pool, holder: "me", now: time.Now,
			heartbeatEvery: 100 * time.Millisecond,
		}
		ok, err := l.tryAcquire(ctx)
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)

		Convey("When a statement outlasts the heartbeat interval", func() {
			before := expiresAt()
			err := l.execWithHeartbeat(ctx, pool, `SELECT pg_sleep(1)`)

			Convey("Then the statement succeeds and the lease was renewed mid-statement", func() {
				So(err, ShouldBeNil)
				// Only a mid-statement renewal can move expires_at: nothing
				// else touches the row between the two reads.
				So(expiresAt().Sub(before), ShouldBeGreaterThan, 500*time.Millisecond)
			})
		})

		Convey("When another runner holds the lease while a statement runs", func() {
			pool.MustExec(`UPDATE flexitype_schema_lock SET holder = 'someone-else' WHERE id = 1`)
			start := time.Now()
			err := l.execWithHeartbeat(ctx, pool, `SELECT pg_sleep(30)`)
			elapsed := time.Since(start)

			Convey("Then the statement is cancelled promptly rather than finishing alongside the new holder", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "lost the migration lease mid-statement")
				So(elapsed, ShouldBeLessThan, 10*time.Second)
			})
		})

		Convey("When every renewal fails transiently", func() {
			l.pool = failingExecer{}

			err := l.execWithHeartbeat(ctx, pool, `SELECT pg_sleep(1)`)

			Convey("Then the statement is not killed: the lease row stays valid until expires_at", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When renewals keep failing past a full lease TTL", func() {
			l.pool = failingExecer{}
			l.renewedAt = time.Now().Add(-2 * leaseTTL)
			start := time.Now()
			err := l.execWithHeartbeat(ctx, pool, `SELECT pg_sleep(30)`)
			elapsed := time.Since(start)

			Convey("Then the statement is cancelled: the lease may genuinely have expired", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "no lease renewal succeeded")
				So(elapsed, ShouldBeLessThan, 10*time.Second)
			})
		})

		Convey("When no pooled executor is available", func() {
			l.pool = nil

			err := l.execWithHeartbeat(ctx, pool, `SELECT 1`)

			Convey("Then the statement still runs, with between-statement renewals only", func() {
				So(err, ShouldBeNil)
			})
		})

		Reset(func() { freeLease() })
	})

	Convey("Given the heartbeat timings", t, func() {
		Convey("Then the heartbeat interval stays comfortably below the lease TTL", func() {
			// The margin is what lets a transient renewal failure retry
			// instead of killing a long index build.
			So(int(leaseTTL/leaseHeartbeat), ShouldBeGreaterThanOrEqualTo, 10)
		})
	})
}
