package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// ErrNotInTransaction is returned when Commit or Rollback is called on the
// pool-level transactor.
var ErrNotInTransaction = errors.New("db: not in a transaction")

// sqlxTransactor is the pool-level Transactor. It executes directly against
// the pool; Begin returns a transaction-bound child.
type sqlxTransactor struct {
	db *sqlx.DB
}

// NewTransactor wraps a sqlx pool in a Transactor.
func NewTransactor(db *sqlx.DB) Transactor {
	return &sqlxTransactor{db: db}
}

func (t *sqlxTransactor) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	return t.db.GetContext(ctx, dest, query, args...)
}

func (t *sqlxTransactor) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	return t.db.SelectContext(ctx, dest, query, args...)
}

func (t *sqlxTransactor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.db.ExecContext(ctx, query, args...)
}

func (t *sqlxTransactor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.db.QueryContext(ctx, query, args...)
}

func (t *sqlxTransactor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.db.QueryRowContext(ctx, query, args...)
}

func (t *sqlxTransactor) Begin(ctx context.Context) (Transactor, error) {
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return &txTransactor{tx: tx}, nil
}

func (t *sqlxTransactor) Commit(context.Context) error   { return ErrNotInTransaction }
func (t *sqlxTransactor) Rollback(context.Context) error { return ErrNotInTransaction }

func (t *sqlxTransactor) InTransaction(ctx context.Context, fn func(tx Transactor) error) error {
	tx, err := t.Begin(ctx)
	if err != nil {
		return err
	}
	return runInTransaction(ctx, tx, fn)
}

// Pool-level hooks have no transaction to attach to; registering them is a
// programming error we surface loudly during development.
func (t *sqlxTransactor) OnPreCommit(Hook)  { panic("db: OnPreCommit outside transaction") }
func (t *sqlxTransactor) OnPostCommit(Hook) { panic("db: OnPostCommit outside transaction") }
func (t *sqlxTransactor) OnRollback(Hook)   { panic("db: OnRollback outside transaction") }

// txTransactor is a transaction-bound Transactor carrying lifecycle hooks.
type txTransactor struct {
	tx         *sqlx.Tx
	depth      int
	done       bool
	preCommit  []Hook
	postCommit []Hook
	rollback   []Hook
}

func (t *txTransactor) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	return t.tx.GetContext(ctx, dest, query, args...)
}

func (t *txTransactor) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	return t.tx.SelectContext(ctx, dest, query, args...)
}

func (t *txTransactor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *txTransactor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

func (t *txTransactor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

// Begin on an open transaction increments nesting depth; the transaction
// only commits when the outermost frame commits.
func (t *txTransactor) Begin(context.Context) (Transactor, error) {
	if t.done {
		return nil, errors.New("db: transaction already finished")
	}
	t.depth++
	return t, nil
}

func (t *txTransactor) Commit(ctx context.Context) error {
	if t.done {
		return errors.New("db: transaction already finished")
	}
	if t.depth > 0 {
		t.depth--
		return nil
	}

	for _, h := range t.preCommit {
		if err := h(ctx); err != nil {
			rbErr := t.Rollback(ctx)
			if rbErr != nil {
				return errors.Join(fmt.Errorf("pre-commit hook: %w", err), rbErr)
			}
			return fmt.Errorf("pre-commit hook: %w", err)
		}
	}

	if err := t.tx.Commit(); err != nil {
		t.done = true
		return fmt.Errorf("commit transaction: %w", err)
	}
	t.done = true

	var errs []error
	for _, h := range t.postCommit {
		if err := h(ctx); err != nil {
			errs = append(errs, fmt.Errorf("post-commit hook: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (t *txTransactor) Rollback(ctx context.Context) error {
	if t.done {
		return nil
	}
	if t.depth > 0 {
		t.depth--
		return errors.New("db: rollback inside nested transaction")
	}
	t.done = true

	err := t.tx.Rollback()

	var errs []error
	if err != nil {
		errs = append(errs, fmt.Errorf("rollback transaction: %w", err))
	}
	for _, h := range t.rollback {
		if hErr := h(ctx); hErr != nil {
			errs = append(errs, fmt.Errorf("rollback hook: %w", hErr))
		}
	}
	return errors.Join(errs...)
}

func (t *txTransactor) InTransaction(ctx context.Context, fn func(tx Transactor) error) error {
	tx, err := t.Begin(ctx)
	if err != nil {
		return err
	}
	return runInTransaction(ctx, tx, fn)
}

func (t *txTransactor) OnPreCommit(h Hook)  { t.preCommit = append(t.preCommit, h) }
func (t *txTransactor) OnPostCommit(h Hook) { t.postCommit = append(t.postCommit, h) }
func (t *txTransactor) OnRollback(h Hook)   { t.rollback = append(t.rollback, h) }

// runInTransaction commits on nil error and rolls back on error or panic.
func runInTransaction(ctx context.Context, tx Transactor, fn func(tx Transactor) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(ctx)
			panic(r)
		}
	}()

	if err = fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}
	return tx.Commit(ctx)
}
