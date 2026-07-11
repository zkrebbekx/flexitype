package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zkrebbekx/flexitype/pkg/db"
)

// transactor satisfies db.Transactor without a database. It honours the
// commit-hook contract (pre-commit before commit, post-commit after,
// rollback hooks on abort) and — via the store snapshot taken at Begin —
// restores the store on rollback, so a failed unit of work leaves no
// partial data, matching PostgreSQL atomicity.
type transactor struct{ store *Store }

var errNoSQL = errors.New("memory: repositories do not execute SQL")

func (t *transactor) GetContext(context.Context, any, string, ...any) error    { return errNoSQL }
func (t *transactor) SelectContext(context.Context, any, string, ...any) error { return errNoSQL }
func (t *transactor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errNoSQL
}
func (t *transactor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errNoSQL
}
func (t *transactor) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func (t *transactor) Begin(context.Context) (db.Transactor, error) {
	tx := &memTx{store: t.store}
	if t.store != nil {
		snap := t.store.snapshot()
		tx.snapshot = &snap
	}
	return tx, nil
}

func (t *transactor) Commit(context.Context) error   { return db.ErrNotInTransaction }
func (t *transactor) Rollback(context.Context) error { return db.ErrNotInTransaction }

func (t *transactor) InTransaction(ctx context.Context, fn func(tx db.Transactor) error) error {
	tx, _ := t.Begin(ctx)
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func (t *transactor) OnPreCommit(db.Hook)  { panic("memory: OnPreCommit outside transaction") }
func (t *transactor) OnPostCommit(db.Hook) { panic("memory: OnPostCommit outside transaction") }
func (t *transactor) OnRollback(db.Hook)   { panic("memory: OnRollback outside transaction") }

// memTx is one logical transaction: hook bookkeeping plus the pre-write
// store snapshot used to undo data mutations on rollback.
type memTx struct {
	store    *Store
	snapshot *storeSnapshot
	depth    int
	done     bool
	pre      []db.Hook
	post     []db.Hook
	rollback []db.Hook
}

func (t *memTx) GetContext(context.Context, any, string, ...any) error    { return errNoSQL }
func (t *memTx) SelectContext(context.Context, any, string, ...any) error { return errNoSQL }
func (t *memTx) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errNoSQL
}
func (t *memTx) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errNoSQL
}
func (t *memTx) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func (t *memTx) Begin(context.Context) (db.Transactor, error) {
	if t.done {
		return nil, errors.New("memory: transaction already finished")
	}
	t.depth++
	return t, nil
}

func (t *memTx) Commit(ctx context.Context) error {
	if t.done {
		return errors.New("memory: transaction already finished")
	}
	if t.depth > 0 {
		t.depth--
		return nil
	}
	for _, h := range t.pre {
		if err := h(ctx); err != nil {
			_ = t.Rollback(ctx)
			return fmt.Errorf("pre-commit hook: %w", err)
		}
	}
	t.done = true
	var errs []error
	for _, h := range t.post {
		if err := h(ctx); err != nil {
			errs = append(errs, fmt.Errorf("post-commit hook: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (t *memTx) Rollback(ctx context.Context) error {
	if t.done {
		return nil
	}
	t.done = true
	// Undo any data written during the transaction, then run the rollback
	// observers.
	if t.store != nil && t.snapshot != nil {
		t.store.restore(*t.snapshot)
	}
	var errs []error
	for _, h := range t.rollback {
		if err := h(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *memTx) InTransaction(ctx context.Context, fn func(tx db.Transactor) error) error {
	tx, err := t.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func (t *memTx) OnPreCommit(h db.Hook)  { t.pre = append(t.pre, h) }
func (t *memTx) OnPostCommit(h db.Hook) { t.post = append(t.post, h) }
func (t *memTx) OnRollback(h db.Hook)   { t.rollback = append(t.rollback, h) }
