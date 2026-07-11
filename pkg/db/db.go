// Package db defines the narrow database interfaces the rest of flexitype
// programs against, plus a sqlx-backed Transactor implementation with
// pre-commit / post-commit / rollback hooks. Usecases own transaction
// boundaries; repositories only ever see a QueryExecer.
package db

import (
	"context"
	"database/sql"
)

// Querier is the read-only slice of a SQL database.
type Querier interface {
	// GetContext executes a query returning a single value. If no result is
	// available sql.ErrNoRows is returned.
	GetContext(ctx context.Context, dest any, query string, args ...any) error

	// SelectContext executes a query returning zero or more values. dest is
	// expected to be a pointer to a slice.
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
}

// Execer is the write slice of a SQL database for statements with no rows.
type Execer interface {
	// ExecContext executes a statement returning no rows.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RawQuerier provides access to raw SQL interfaces for compatibility.
type RawQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// QueryExecer is the complete interface repositories depend on. It is
// satisfied by both the pool and an open transaction, so repository code is
// identical inside and outside a transaction.
type QueryExecer interface {
	Querier
	Execer
	RawQuerier
}

// Hook is a function invoked around transaction completion. Pre-commit hooks
// run inside the transaction: returning an error aborts the commit and rolls
// back. Post-commit hooks run after a durable commit. Rollback hooks run
// after a rollback.
type Hook func(ctx context.Context) error

// Transactor provides transaction management, query execution and
// commit-lifecycle hooks.
type Transactor interface {
	QueryExecer

	// Begin starts a transaction. Calling Begin on an open transaction
	// returns the same transaction with an incremented nesting depth; only
	// the outermost Commit actually commits.
	Begin(ctx context.Context) (Transactor, error)

	// Commit runs pre-commit hooks (an error rolls back), commits, then runs
	// post-commit hooks. Post-commit hook errors are returned but the commit
	// is already durable.
	Commit(ctx context.Context) error

	// Rollback aborts the transaction and runs rollback hooks.
	Rollback(ctx context.Context) error

	// InTransaction executes fn inside a transaction, committing on nil and
	// rolling back on error or panic.
	InTransaction(ctx context.Context, fn func(tx Transactor) error) error

	// OnPreCommit registers a hook that runs inside the transaction, before
	// COMMIT. Ideal for same-transaction writes such as activity logs.
	OnPreCommit(h Hook)

	// OnPostCommit registers a hook that runs after a successful COMMIT.
	// Ideal for dispatching domain events to external subscribers.
	OnPostCommit(h Hook)

	// OnRollback registers a hook that runs after the transaction rolls back.
	OnRollback(h Hook)
}
