package db

import (
	"context"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	. "github.com/smartystreets/goconvey/convey"
)

func newMockTransactor(t *testing.T) (Transactor, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = mockDB.Close() })
	return NewTransactor(sqlx.NewDb(mockDB, "sqlmock")), mock
}

func TestTransactorHooks(t *testing.T) {
	Convey("Given a transaction with pre/post/rollback hooks", t, func() {
		ctx := context.Background()

		Convey("When the transaction commits cleanly", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO audit").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			var order []string
			tx, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)

			tx.OnPreCommit(func(ctx context.Context) error {
				order = append(order, "pre")
				// Pre-commit hooks run inside the transaction: this write
				// must be part of it. The transactor is query-free at the
				// interface; the concrete sqlx transactor is the executor.
				_, execErr := tx.(QueryExecer).ExecContext(ctx, "INSERT INTO audit VALUES (1)")
				return execErr
			})
			tx.OnPostCommit(func(context.Context) error {
				order = append(order, "post")
				return nil
			})
			tx.OnRollback(func(context.Context) error {
				order = append(order, "rollback")
				return nil
			})

			So(tx.Commit(ctx), ShouldBeNil)

			Convey("Then pre-commit runs inside the transaction and post-commit after it", func() {
				So(order, ShouldResemble, []string{"pre", "post"})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When a pre-commit hook fails", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectRollback()

			var order []string
			tx, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)

			tx.OnPreCommit(func(context.Context) error { return fmt.Errorf("audit write failed") })
			tx.OnPostCommit(func(context.Context) error {
				order = append(order, "post")
				return nil
			})
			tx.OnRollback(func(context.Context) error {
				order = append(order, "rollback")
				return nil
			})

			commitErr := tx.Commit(ctx)

			Convey("Then the transaction rolls back, post-commit never fires and rollback hooks do", func() {
				So(commitErr, ShouldNotBeNil)
				So(commitErr.Error(), ShouldContainSubstring, "audit write failed")
				So(order, ShouldResemble, []string{"rollback"})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When the work function fails inside InTransaction", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectRollback()

			var rolledBack bool
			err := transactor.InTransaction(ctx, func(tx Transactor) error {
				tx.OnRollback(func(context.Context) error {
					rolledBack = true
					return nil
				})
				return fmt.Errorf("business rule violated")
			})

			Convey("Then the transaction rolls back and the error propagates", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "business rule violated")
				So(rolledBack, ShouldBeTrue)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When post-commit hooks fail after a durable commit", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectCommit()

			tx, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)
			tx.OnPostCommit(func(context.Context) error { return fmt.Errorf("dispatch failed") })

			commitErr := tx.Commit(ctx)

			Convey("Then the error is reported but the commit stands", func() {
				So(commitErr, ShouldNotBeNil)
				So(commitErr.Error(), ShouldContainSubstring, "dispatch failed")
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When transactions nest via Begin", func() {
			transactor, mock := newMockTransactor(t)
			mock.ExpectBegin()
			mock.ExpectCommit()

			outer, err := transactor.Begin(ctx)
			So(err, ShouldBeNil)
			inner, err := outer.Begin(ctx)
			So(err, ShouldBeNil)

			Convey("Then only the outermost commit actually commits", func() {
				So(inner.Commit(ctx), ShouldBeNil)             // nested: no real COMMIT
				So(mock.ExpectationsWereMet(), ShouldNotBeNil) // commit still pending
				So(outer.Commit(ctx), ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When hooks are registered on the pool-level transactor", func() {
			transactor, _ := newMockTransactor(t)

			Convey("Then it panics loudly — a programming error", func() {
				So(func() { transactor.OnPreCommit(func(context.Context) error { return nil }) }, ShouldPanic)
			})
		})
	})
}
