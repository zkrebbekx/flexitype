package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestTxMarkerSealsTx(t *testing.T) {
	Convey("Given the sealed Tx interface", t, func() {
		Convey("When a type embeds TxMarker", func() {
			type outOfPackageTx struct{ TxMarker }
			var handle Tx = outOfPackageTx{}

			Convey("Then it satisfies Tx through the marker method", func() {
				So(handle, ShouldNotBeNil)
				So(func() { TxMarker{}.isTx() }, ShouldNotPanic)
			})
		})

		Convey("When the sqlx transactors are used as handles", func() {
			transactor, _ := newMockTransactor(t)

			Convey("Then both the pool and a transaction are valid Tx handles", func() {
				var poolHandle Tx = transactor
				So(poolHandle, ShouldNotBeNil)
			})
		})
	})
}

func TestPoolTransactorQueries(t *testing.T) {
	ctx := context.Background()

	Convey("Given the pool-level transactor", t, func() {
		transactor, mock := newMockTransactor(t)
		exec := transactor.(QueryExecer)

		Convey("When a single row is fetched with GetContext", func() {
			mock.ExpectQuery("SELECT count").
				WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(7)))

			var n int
			err := exec.GetContext(ctx, &n, "SELECT count(*) AS n FROM entities")

			Convey("Then the value is scanned straight off the pool", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 7)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When no row matches a GetContext query", func() {
			mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"n"}))

			var n int
			err := exec.GetContext(ctx, &n, "SELECT count(*) AS n FROM entities")

			Convey("Then sql.ErrNoRows is surfaced unwrapped", func() {
				So(errors.Is(err, sql.ErrNoRows), ShouldBeTrue)
			})
		})

		Convey("When many rows are fetched with SelectContext", func() {
			mock.ExpectQuery("SELECT id").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a").AddRow("b"))

			var ids []string
			err := exec.SelectContext(ctx, &ids, "SELECT id FROM entities")

			Convey("Then every row lands in the destination slice", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"a", "b"})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When a statement is run with ExecContext", func() {
			mock.ExpectExec("DELETE FROM entities").
				WithArgs("acme").
				WillReturnResult(sqlmock.NewResult(0, 3))

			res, err := exec.ExecContext(ctx, "DELETE FROM entities WHERE tenant = $1", "acme")

			Convey("Then the driver result reports the affected rows", func() {
				So(err, ShouldBeNil)
				affected, affErr := res.RowsAffected()
				So(affErr, ShouldBeNil)
				So(affected, ShouldEqual, 3)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When ExecContext fails", func() {
			boom := fmt.Errorf("deadlock detected")
			mock.ExpectExec("DELETE FROM entities").WillReturnError(boom)

			_, err := exec.ExecContext(ctx, "DELETE FROM entities")

			Convey("Then the driver error is returned as-is", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
			})
		})

		Convey("When raw rows are iterated with QueryContext", func() {
			mock.ExpectQuery("SELECT id").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a").AddRow("b"))

			rows, err := exec.QueryContext(ctx, "SELECT id FROM entities")
			So(err, ShouldBeNil)

			var got []string
			for rows.Next() {
				var id string
				So(rows.Scan(&id), ShouldBeNil)
				got = append(got, id)
			}

			Convey("Then the raw rows carry the same data", func() {
				So(rows.Err(), ShouldBeNil)
				So(rows.Close(), ShouldBeNil)
				So(got, ShouldResemble, []string{"a", "b"})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When a single raw row is read with QueryRowContext", func() {
			mock.ExpectQuery("SELECT name").
				WithArgs("e1").
				WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("widget"))

			var name string
			err := exec.QueryRowContext(ctx, "SELECT name FROM entities WHERE id = $1", "e1").Scan(&name)

			Convey("Then the row scans into the destination", func() {
				So(err, ShouldBeNil)
				So(name, ShouldEqual, "widget")
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When Commit or Rollback is called without a transaction", func() {
			commitErr := transactor.Commit(ctx)
			rollbackErr := transactor.Rollback(ctx)

			Convey("Then both report ErrNotInTransaction by identity", func() {
				So(errors.Is(commitErr, ErrNotInTransaction), ShouldBeTrue)
				So(errors.Is(rollbackErr, ErrNotInTransaction), ShouldBeTrue)
			})
		})

		Convey("When post-commit or rollback hooks are registered on the pool", func() {
			Convey("Then each panics — hooks need a transaction to attach to", func() {
				So(func() { transactor.OnPostCommit(func(context.Context) error { return nil }) }, ShouldPanic)
				So(func() { transactor.OnRollback(func(context.Context) error { return nil }) }, ShouldPanic)
			})
		})

		Convey("When BEGIN fails at the driver", func() {
			boom := fmt.Errorf("too many connections")
			mock.ExpectBegin().WillReturnError(boom)

			tx, err := transactor.Begin(ctx)

			Convey("Then Begin wraps the driver error", func() {
				So(tx, ShouldBeNil)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "begin transaction")
				So(errors.Is(err, boom), ShouldBeTrue)
			})
		})

		Convey("When InTransaction cannot begin", func() {
			boom := fmt.Errorf("too many connections")
			mock.ExpectBegin().WillReturnError(boom)

			var ran bool
			err := transactor.InTransaction(ctx, func(Transactor) error {
				ran = true
				return nil
			})

			Convey("Then the work function never runs and the error propagates", func() {
				So(ran, ShouldBeFalse)
				So(errors.Is(err, boom), ShouldBeTrue)
			})
		})
	})
}

func TestTxTransactorQueries(t *testing.T) {
	ctx := context.Background()

	Convey("Given an open transaction", t, func() {
		transactor, mock := newMockTransactor(t)
		mock.ExpectBegin()
		tx, err := transactor.Begin(ctx)
		So(err, ShouldBeNil)
		exec := tx.(QueryExecer)

		Convey("When queries run through the transaction-bound executor", func() {
			mock.ExpectQuery("SELECT count").
				WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(2)))
			mock.ExpectQuery("SELECT id").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a"))
			mock.ExpectExec("UPDATE entities").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT raw").
				WillReturnRows(sqlmock.NewRows([]string{"raw"}).AddRow("r1"))
			mock.ExpectQuery("SELECT one").
				WillReturnRows(sqlmock.NewRows([]string{"one"}).AddRow("solo"))
			mock.ExpectCommit()

			var n int
			So(exec.GetContext(ctx, &n, "SELECT count(*) AS n FROM entities"), ShouldBeNil)

			var ids []string
			So(exec.SelectContext(ctx, &ids, "SELECT id FROM entities"), ShouldBeNil)

			res, execErr := exec.ExecContext(ctx, "UPDATE entities SET name = 'x'")
			So(execErr, ShouldBeNil)

			rows, queryErr := exec.QueryContext(ctx, "SELECT raw FROM entities")
			So(queryErr, ShouldBeNil)
			So(rows.Next(), ShouldBeTrue)
			var raw string
			So(rows.Scan(&raw), ShouldBeNil)
			So(rows.Close(), ShouldBeNil)

			var one string
			rowErr := exec.QueryRowContext(ctx, "SELECT one FROM entities").Scan(&one)

			Convey("Then every wrapper reads and writes inside that transaction", func() {
				So(n, ShouldEqual, 2)
				So(ids, ShouldResemble, []string{"a"})
				affected, _ := res.RowsAffected()
				So(affected, ShouldEqual, 1)
				So(raw, ShouldEqual, "r1")
				So(rowErr, ShouldBeNil)
				So(one, ShouldEqual, "solo")

				So(tx.Commit(ctx), ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When the transaction has already committed", func() {
			mock.ExpectCommit()
			So(tx.Commit(ctx), ShouldBeNil)

			Convey("Then Begin and Commit refuse, and Rollback is a silent no-op", func() {
				nested, beginErr := tx.Begin(ctx)
				So(nested, ShouldBeNil)
				So(beginErr, ShouldNotBeNil)
				So(beginErr.Error(), ShouldEqual, "db: transaction already finished")

				commitErr := tx.Commit(ctx)
				So(commitErr, ShouldNotBeNil)
				So(commitErr.Error(), ShouldEqual, "db: transaction already finished")

				So(tx.Rollback(ctx), ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When COMMIT fails at the driver", func() {
			boom := fmt.Errorf("serialization failure")
			mock.ExpectCommit().WillReturnError(boom)

			var postRan bool
			tx.OnPostCommit(func(context.Context) error {
				postRan = true
				return nil
			})
			commitErr := tx.Commit(ctx)

			Convey("Then the error is wrapped and post-commit hooks never fire", func() {
				So(commitErr, ShouldNotBeNil)
				So(commitErr.Error(), ShouldContainSubstring, "commit transaction")
				So(errors.Is(commitErr, boom), ShouldBeTrue)
				So(postRan, ShouldBeFalse)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When ROLLBACK fails at the driver", func() {
			boom := fmt.Errorf("connection lost")
			mock.ExpectRollback().WillReturnError(boom)

			var hookRan bool
			tx.OnRollback(func(context.Context) error {
				hookRan = true
				return nil
			})
			rbErr := tx.Rollback(ctx)

			Convey("Then the error is wrapped and rollback hooks still fire", func() {
				So(rbErr, ShouldNotBeNil)
				So(rbErr.Error(), ShouldContainSubstring, "rollback transaction")
				So(errors.Is(rbErr, boom), ShouldBeTrue)
				So(hookRan, ShouldBeTrue)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When a rollback hook itself fails", func() {
			mock.ExpectRollback()
			tx.OnRollback(func(context.Context) error { return fmt.Errorf("cache invalidation failed") })

			rbErr := tx.Rollback(ctx)

			Convey("Then the hook error is reported after the rollback", func() {
				So(rbErr, ShouldNotBeNil)
				So(rbErr.Error(), ShouldContainSubstring, "rollback hook")
				So(rbErr.Error(), ShouldContainSubstring, "cache invalidation failed")
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When Rollback is called from inside a nested frame", func() {
			nested, beginErr := tx.Begin(ctx)
			So(beginErr, ShouldBeNil)

			rbErr := nested.Rollback(ctx)

			Convey("Then it refuses rather than aborting the outer transaction", func() {
				So(rbErr, ShouldNotBeNil)
				So(rbErr.Error(), ShouldEqual, "db: rollback inside nested transaction")

				// No ROLLBACK reached the driver; the outer frame still owns it.
				mock.ExpectCommit()
				So(tx.Commit(ctx), ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When a pre-commit hook fails and the rollback fails too", func() {
			mock.ExpectRollback().WillReturnError(fmt.Errorf("connection lost"))
			tx.OnPreCommit(func(context.Context) error { return fmt.Errorf("audit write failed") })

			commitErr := tx.Commit(ctx)

			Convey("Then both failures are joined into the returned error", func() {
				So(commitErr, ShouldNotBeNil)
				So(commitErr.Error(), ShouldContainSubstring, "pre-commit hook")
				So(commitErr.Error(), ShouldContainSubstring, "audit write failed")
				So(commitErr.Error(), ShouldContainSubstring, "rollback transaction")
				So(commitErr.Error(), ShouldContainSubstring, "connection lost")
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When InTransaction is called on the open transaction", func() {
			mock.ExpectExec("INSERT INTO entities").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			var innerIsOuter bool
			inTxErr := tx.InTransaction(ctx, func(inner Transactor) error {
				innerIsOuter = inner == tx
				_, err := inner.(QueryExecer).ExecContext(ctx, "INSERT INTO entities VALUES (1)")
				return err
			})

			Convey("Then it reuses the outer transaction and only the outer frame commits", func() {
				So(inTxErr, ShouldBeNil)
				So(innerIsOuter, ShouldBeTrue)
				// The nested frame's Commit merely decremented the depth.
				So(mock.ExpectationsWereMet(), ShouldNotBeNil)

				So(tx.Commit(ctx), ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})
}

func TestRunInTransaction(t *testing.T) {
	ctx := context.Background()

	Convey("Given work run through InTransaction on the pool", t, func() {
		Convey("When the work function succeeds", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO entities").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			var order []string
			err := transactor.InTransaction(ctx, func(tx Transactor) error {
				tx.OnPostCommit(func(context.Context) error {
					order = append(order, "post")
					return nil
				})
				tx.OnRollback(func(context.Context) error {
					order = append(order, "rollback")
					return nil
				})
				_, execErr := tx.(QueryExecer).ExecContext(ctx, "INSERT INTO entities VALUES (1)")
				return execErr
			})

			Convey("Then it commits and only post-commit hooks fire", func() {
				So(err, ShouldBeNil)
				So(order, ShouldResemble, []string{"post"})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When the work function fails and the rollback fails too", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectRollback().WillReturnError(fmt.Errorf("connection lost"))

			workErr := fmt.Errorf("business rule violated")
			err := transactor.InTransaction(ctx, func(Transactor) error { return workErr })

			Convey("Then both errors are joined, with the original still identifiable", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, workErr), ShouldBeTrue)
				So(err.Error(), ShouldContainSubstring, "connection lost")
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When the work function panics", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectRollback()

			var rolledBack bool
			run := func() {
				_ = transactor.InTransaction(ctx, func(tx Transactor) error {
					tx.OnRollback(func(context.Context) error {
						rolledBack = true
						return nil
					})
					panic("unexpected nil dereference")
				})
			}

			Convey("Then the transaction rolls back and the panic still propagates", func() {
				So(run, ShouldPanicWith, "unexpected nil dereference")
				So(rolledBack, ShouldBeTrue)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})
}

func TestPageFetchLimitAndTotal(t *testing.T) {
	Convey("Given a resolved page", t, func() {
		Convey("When a keyset query asks the database for rows", func() {
			page := Page{Limit: 20}

			Convey("Then it over-fetches by one to detect a following page", func() {
				So(page.FetchLimit(), ShouldEqual, 21)
				So(Page{Limit: 1}.FetchLimit(), ShouldEqual, 2)
			})
		})

		Convey("When the caller did not request a total", func() {
			total := KeysetTotal(Page{WantTotal: false}, 42)

			Convey("Then no count rides along in PageInfo", func() {
				So(total, ShouldBeNil)
			})
		})

		Convey("When the caller did request a total", func() {
			total := KeysetTotal(Page{WantTotal: true}, 42)

			Convey("Then the computed count is returned by pointer", func() {
				So(total, ShouldNotBeNil)
				So(*total, ShouldEqual, 42)
			})
		})

		Convey("When a zero count is requested", func() {
			total := KeysetTotal(Page{WantTotal: true}, 0)

			Convey("Then zero is reported rather than omitted", func() {
				So(total, ShouldNotBeNil)
				So(*total, ShouldEqual, 0)
			})
		})
	})
}

func TestResolveCursor(t *testing.T) {
	strp := func(s string) *string { return &s }

	Convey("Given client pagination args carrying a cursor", t, func() {
		Convey("When the cursor is a well-formed keyset", func() {
			cursor := EncodeKeyset("2026-01-01T00:00:00Z", "e5")
			p, err := PageArgs{Cursor: strp(cursor)}.Resolve()

			Convey("Then it passes through opaquely alongside the default limit", func() {
				So(err, ShouldBeNil)
				So(p.Cursor, ShouldEqual, cursor)
				So(p.Limit, ShouldEqual, defaultPageSize)
			})
		})

		Convey("When the cursor is present but empty", func() {
			p, err := PageArgs{Cursor: strp("")}.Resolve()

			Convey("Then it is treated as the first page", func() {
				So(err, ShouldBeNil)
				So(p.Cursor, ShouldEqual, "")
			})
		})

		Convey("When the cursor is not valid base64", func() {
			_, err := PageArgs{Cursor: strp("not-base64!!")}.Resolve()

			Convey("Then it is rejected before reaching a repository", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid cursor")
			})
		})

		Convey("When the cursor is base64 but not a JSON string array", func() {
			_, err := PageArgs{Cursor: strp(EncodeKeyset()[:4] + "!!")}.Resolve()

			Convey("Then it is rejected as an invalid cursor", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid cursor")
			})
		})
	})

	Convey("Given a keyset cursor decoded directly", t, func() {
		Convey("When it carries the wrong number of columns for the ordering", func() {
			cols := []KeysetColumn{{Expr: "updated_at"}, {Expr: "id"}}
			_, _, err := KeysetPredicate(cols, EncodeKeyset("only-one"))

			Convey("Then the predicate builder rejects it", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid cursor")
			})
		})

		Convey("When the payload is valid base64 of non-array JSON", func() {
			_, err := DecodeKeyset(EncodeKeyset("a")[:0] + "eyJhIjoxfQ==")

			Convey("Then decoding fails as an invalid cursor", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "invalid cursor")
			})
		})
	})
}
