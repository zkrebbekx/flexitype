package memory_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
)

// TestMediaKeyLockMemory is the in-memory twin of TestMediaKeyLockPostgres
// (issue #484): the per-key media lock requires a transaction, is re-entrant
// inside one, and makes a second transaction wait for the holder's commit —
// mirroring pg_advisory_xact_lock.
func TestMediaKeyLockMemory(t *testing.T) {
	Convey("Given the in-memory value repository", t, func() {
		ctx := context.Background()
		store := memory.NewStore()
		repo, ok := store.Repositories().Values.(domainvalue.Repository)
		So(ok, ShouldBeTrue)
		transactor := store.Transactor()

		Convey("When the lock is taken outside a transaction", func() {
			err := repo.LockMediaKey(ctx, "k1")

			Convey("Then it is refused", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "requires a transaction")
			})
		})

		Convey("When one transaction holds the lock", func() {
			tx1, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)
			bound := repo.WithTx(tx1)
			So(bound.LockMediaKey(ctx, "k1"), ShouldBeNil)

			Convey("Then re-locking inside the same transaction returns at once", func() {
				So(bound.LockMediaKey(ctx, "k1"), ShouldBeNil)
				So(tx1.Rollback(ctx), ShouldBeNil)
			})

			Convey("Then a waiter with an expired context gives up instead of hanging", func() {
				short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				tx2, terr := transactor.Begin(ctx)
				So(terr, ShouldBeNil)
				defer func() { _ = tx2.Rollback(ctx) }()
				werr := repo.WithTx(tx2).LockMediaKey(short, "k1")
				So(werr, ShouldNotBeNil)
				So(tx1.Rollback(ctx), ShouldBeNil)
			})

			Convey("Then a second transaction waits for the holder's commit", func() {
				var committed atomic.Bool
				sawCommit := make(chan bool, 1)
				go func() {
					tx2, gerr := transactor.Begin(ctx)
					if gerr != nil {
						sawCommit <- false
						return
					}
					defer func() { _ = tx2.Rollback(ctx) }()
					if lerr := repo.WithTx(tx2).LockMediaKey(ctx, "k1"); lerr != nil {
						sawCommit <- false
						return
					}
					sawCommit <- committed.Load()
				}()

				// Give the waiter time to block on the held lock, then commit.
				time.Sleep(50 * time.Millisecond)
				committed.Store(true)
				So(tx1.Commit(ctx), ShouldBeNil)

				Convey("And the waiter acquired it only after that commit", func() {
					So(<-sawCommit, ShouldBeTrue)
				})
			})
		})
	})
}
