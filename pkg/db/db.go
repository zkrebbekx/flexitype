// Package db defines the narrow database interfaces the rest of flexitype
// programs against, plus a sqlx-backed Transactor implementation with
// pre-commit / post-commit / rollback hooks. Usecases own transaction
// boundaries; domain repositories only ever see an opaque Tx handle, which the
// backend that created it down-casts to the concrete executor it understands.
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

// QueryExecer is the complete SQL surface a PostgreSQL-backed repository runs
// queries through. It is satisfied by both the pool and an open transaction, so
// backend code is identical inside and outside a transaction. Domain
// repositories no longer depend on it: they receive the opaque Tx handle and the
// backend down-casts it back to a QueryExecer.
type QueryExecer interface {
	Querier
	Execer
	RawQuerier
}

// Tx is an opaque handle to an open transaction. The unit of work hands it to a
// domain repository's WithTx (and to the outbox and activity-log sinks); each
// backend down-casts it to the concrete transaction type it understands — the
// SQL executor (QueryExecer) for PostgreSQL, the in-memory transaction for the
// memory backend. Tx deliberately exposes no query methods, so a repository
// cannot run SQL through the handle it is given — the leak the old QueryExecer
// parameter allowed. The interface is sealed: only this package's transaction
// types, and out-of-package transaction types embedding TxMarker, satisfy it.
type Tx interface {
	isTx()
}

// TxMarker is embedded by transaction implementations outside this package (the
// in-memory backend's transaction types) to satisfy Tx. The SQL-backed
// transactors in this package embed it too.
type TxMarker struct{}

func (TxMarker) isTx() {}

// Hook is a function invoked around transaction completion. Pre-commit hooks
// run inside the transaction: returning an error aborts the commit and rolls
// back. Post-commit hooks run after a durable commit. Rollback hooks run
// after a rollback.
type Hook func(ctx context.Context) error

// Transactor provides transaction management and commit-lifecycle hooks. It is
// an opaque Tx handle, not a query executor: the concrete PostgreSQL transactor
// also implements QueryExecer (the outbox relay and migrator down-cast to it),
// but the interface stays query-free so the in-memory transactor need not
// pretend to run SQL.
type Transactor interface {
	Tx

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
