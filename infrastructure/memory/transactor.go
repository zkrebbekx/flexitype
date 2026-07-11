package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zkrebbekx/flexitype/pkg/db"
)

// transactor satisfies db.Transactor without a database. Data writes apply
// immediately; the value it provides is the commit-hook contract the unit
// of work depends on: pre-commit hooks run before "commit" (an error
// aborts and fires rollback hooks), post-commit hooks after.
type transactor struct{}

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

func (t *transactor) Begin(context.Context) (db.Transactor, error) { return &memTx{}, nil }

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

// memTx is one logical transaction: hook bookkeeping only.
type memTx struct {
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
